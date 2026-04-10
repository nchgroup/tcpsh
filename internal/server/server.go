// Package server implements the tcpsh --server mode.
//
// The server listens on a TCP address, accepts one client at a time, performs
// a ChaCha20-Poly1305 encrypted handshake using a one-time token, and then
// acts as a headless REPL: it reads command frames from the client, dispatches
// them through the same console.Dispatcher used in console mode, and sends the
// response frames back.  Listener events (new connections, disconnections) are
// pushed to the client as unsolicited event frames.
//
// State (open listeners, sessions, forwards) is preserved across client
// disconnects — the server keeps running and waits for the next client.
package server

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"github.com/nchgroup/tcpsh/internal/config"
	"github.com/nchgroup/tcpsh/internal/console"
	"github.com/nchgroup/tcpsh/internal/forward"
	"github.com/nchgroup/tcpsh/internal/listener"
	"github.com/nchgroup/tcpsh/internal/proto"
	"github.com/nchgroup/tcpsh/internal/session"

	"github.com/charmbracelet/lipgloss"
)

// Server is a headless tcpsh daemon that serves one CLI client at a time.
type Server struct {
	cfg       *config.Config
	bind      string
	key       [32]byte
	token     string
	sessions  *session.Manager
	listeners *listener.Manager
	forwards  *forward.Manager
}

// New creates a Server.  If token is non-empty it is used as-is (hardcoded
// mode); otherwise a fresh random 32-char token is generated.  The token must
// be supplied to `tcpsh --client` via --token or TCPSH_TOKEN.
func New(cfg *config.Config, bind string, token string) (*Server, error) {
	var err error
	if token == "" {
		token, err = proto.GenerateToken()
		if err != nil {
			return nil, fmt.Errorf("server: %w", err)
		}
	} else if err := proto.ValidateToken(token); err != nil {
		return nil, fmt.Errorf("server: invalid token: %w", err)
	}
	key := proto.TokenToKey(token)

	sessMgr := session.NewManager()
	fwdMgr := forward.NewManager(cfg.DialTimeout)
	listenMgr := listener.NewManager(sessMgr)

	s := &Server{
		cfg:       cfg,
		bind:      bind,
		key:       key,
		token:     token,
		sessions:  sessMgr,
		listeners: listenMgr,
		forwards:  fwdMgr,
	}
	return s, nil
}

// Token returns the one-time authentication token.
func (s *Server) Token() string { return s.token }

// PrintToken prints the server address and token to stdout using lipgloss so
// that the box proportions are always correct regardless of terminal width.
func (s *Server) PrintToken() {
	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("12")) // bright blue

	tokenStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("11")) // bright yellow

	dimStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("8")) // dark grey

	borderStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("6")). // cyan
		Padding(1, 3)

	body := strings.Join([]string{
		titleStyle.Render("tcpsh server listening on " + s.bind),
		"",
		"TOKEN  " + tokenStyle.Render(s.token),
		"",
		dimStyle.Render("Connect:"),
		"  tcpsh --client " + s.bind + " --token " + tokenStyle.Render("<TOKEN>"),
		"  TCPSH_TOKEN=" + tokenStyle.Render("<TOKEN>") + " tcpsh --client " + s.bind,
		"",
		dimStyle.Render("Keep this token secret — it encrypts all traffic."),
	}, "\n")

	fmt.Println(borderStyle.Render(body))
}

// Run starts listening and enters the accept loop.  It blocks until the
// process receives SIGINT or SIGTERM.
func (s *Server) Run() error {
	s.PrintToken()

	ln, err := net.Listen("tcp", s.bind)
	if err != nil {
		return fmt.Errorf("server: listen %s: %w", s.bind, err)
	}
	defer ln.Close()

	// Handle OS signals for graceful shutdown.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		fmt.Println("\n  [server] shutting down…")
		s.listeners.CloseAll()
		s.forwards.CloseAll()
		ln.Close()
		os.Exit(0)
	}()

	fmt.Printf("  [server] waiting for client connection on %s\n", s.bind)

	for {
		conn, err := ln.Accept()
		if err != nil {
			// ln.Close() was called by the signal handler.
			return nil
		}

		if err := s.authenticate(conn); err != nil {
			fmt.Fprintf(os.Stderr, "  [server] auth failed from %s: %v\n", conn.RemoteAddr(), err)
			conn.Close()
			continue
		}

		fmt.Printf("  [server] client connected: %s\n", conn.RemoteAddr())
		s.serveClient(conn)
		fmt.Printf("  [server] client disconnected: %s — waiting for next connection\n", conn.RemoteAddr())
	}
}

// authenticate performs the encrypted handshake.  Returns nil on success.
func (s *Server) authenticate(conn net.Conn) error {
	br := proto.NewBufReader(conn)
	return proto.ReceiveHandshake(conn, br, s.key)
}

// serveClient handles one connected CLI client.  It returns when the client
// disconnects.  All state (listeners, sessions, forwards) persists after
// serveClient returns.
func (s *Server) serveClient(conn net.Conn) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	br := proto.NewBufReader(conn)
	dispatcher := console.NewDispatcher(ctx, s.sessions, s.listeners, s.forwards)

	// cmdCh carries decrypted command lines from the client.
	cmdCh := make(chan string, 8)
	// outCh carries text to send back to the client.
	outCh := make(chan string, 64)

	// Reader goroutine: decrypt frames from client → cmdCh.
	go func() {
		defer close(cmdCh)
		for {
			plain, err := proto.ReadFrame(br, s.key)
			if err != nil {
				return
			}
			line := strings.TrimRight(string(plain), "\r\n")
			select {
			case cmdCh <- line:
			case <-ctx.Done():
				return
			}
		}
	}()

	// Event forwarder: listener events → outCh.
	go func() {
		evCh := s.listeners.Events()
		for {
			select {
			case <-ctx.Done():
				return
			case ev, ok := <-evCh:
				if !ok {
					return
				}
				var msg string
				switch ev.Type {
				case listener.EventNewConn:
					msg = fmt.Sprintf("[+] New connection on :%d from %s (session %d)",
						ev.Port, ev.Session.RemoteAddr, ev.Session.ID)
				case listener.EventConnDead:
					msg = fmt.Sprintf("[-] Session %d (%s) disconnected from :%d",
						ev.Session.ID, ev.Session.RemoteAddr, ev.Port)
					s.sessions.Remove(ev.Session.ID)
				case listener.EventError:
					msg = fmt.Sprintf("[!] Listener :%d error: %v", ev.Port, ev.Err)
				}
				if msg != "" {
					select {
					case outCh <- msg:
					case <-ctx.Done():
						return
					}
				}
			}
		}
	}()

	// Writer goroutine: encrypt and send frames from outCh to client.
	go func() {
		for {
			select {
			case msg, ok := <-outCh:
				if !ok {
					return
				}
				if err := proto.WriteFrame(conn, s.key, []byte(msg)); err != nil {
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	// Main dispatch loop.
	for {
		select {
		case line, ok := <-cmdCh:
			if !ok {
				// Client disconnected.
				return
			}
			if line == "" {
				continue
			}
			cmd := console.Parse(line, false)
			var response string
			switch cmd.Kind {
			case console.CmdEmpty:
				continue
			case console.CmdSystem:
				response = dispatcher.RunSystem(cmd.Args[0])
			case console.CmdSpecial:
				if cmd.Verb == "+exit" || cmd.Verb == "exit" {
					_ = proto.WriteFrame(conn, s.key, []byte("Bye."))
					return
				}
				response = "[server] special commands are not supported in server mode"
			case console.CmdTool:
				if cmd.Verb == "exit" {
					_ = proto.WriteFrame(conn, s.key, []byte("Bye."))
					return
				}
				response = dispatcher.Dispatch(cmd)
			}
			if response != "" {
				select {
				case outCh <- response:
				case <-ctx.Done():
					return
				}
			}
		case <-ctx.Done():
			return
		}
	}
}
