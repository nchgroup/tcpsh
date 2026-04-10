// Package server implements the tcpsh -server mode.
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
	"sync/atomic"
	"syscall"

	"github.com/nchgroup/tcpsh/internal/config"
	"github.com/nchgroup/tcpsh/internal/console"
	"github.com/nchgroup/tcpsh/internal/executor"
	"github.com/nchgroup/tcpsh/internal/forward"
	"github.com/nchgroup/tcpsh/internal/listener"
	"github.com/nchgroup/tcpsh/internal/proto"
	"github.com/nchgroup/tcpsh/internal/session"
	"github.com/nchgroup/tcpsh/pkg/ui"

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
// be supplied to `tcpsh -client` via -token or TCPSH_TOKEN.
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
		"  tcpsh -client " + s.bind + " -token " + tokenStyle.Render("<TOKEN>"),
		"  TCPSH_TOKEN=" + tokenStyle.Render("<TOKEN>") + " tcpsh -client " + s.bind,
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

// outMsg is a typed message sent from the server to the connected client.
type outMsg struct {
	typ  byte
	text string
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
	// outCh carries typed messages to send back to the client.
	outCh := make(chan outMsg, 64)

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

	// Event forwarder: listener events → outCh tagged as FrameEvent.
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
					case outCh <- outMsg{proto.FrameEvent, msg}:
					case <-ctx.Done():
						return
					}
				}
			}
		}
	}()

	// Writer goroutine: encrypt and send typed frames from outCh to client.
	go func() {
		for {
			select {
			case msg, ok := <-outCh:
				if !ok {
					return
				}
				if err := proto.WriteTypedFrame(conn, s.key, msg.typ, []byte(msg.text)); err != nil {
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
					_ = proto.WriteTypedFrame(conn, s.key, proto.FrameResponse, []byte("Bye."))
					return
				}
				response = "[server] special commands are not supported in server mode"
			case console.CmdTool:
				if cmd.Verb == "exit" {
					_ = proto.WriteTypedFrame(conn, s.key, proto.FrameResponse, []byte("Bye."))
					return
				}
				if cmd.Verb == "use" {
					s.serveSession(cmd.Args, cmdCh, outCh, ctx)
					continue
				}
				response = dispatcher.Dispatch(cmd)
			}
			if response != "" {
				select {
				case outCh <- outMsg{proto.FrameResponse, response}:
				case <-ctx.Done():
					return
				}
			}
		case <-ctx.Done():
			return
		}
	}
}

// serveSession handles the `use` command in server mode.
// It streams session RX data to the client as FrameEvent frames and forwards
// lines from the client to the session until +back / +bg / +exit.
func (s *Server) serveSession(args []string, cmdCh <-chan string, outCh chan<- outMsg, ctx context.Context) {
	send := func(typ byte, text string) {
		select {
		case outCh <- outMsg{typ, text}:
		case <-ctx.Done():
		}
	}

	if len(args) == 0 {
		send(proto.FrameResponse, ui.Errorf("usage: use <port>[:<idx>]"))
		return
	}

	port, idx, err := console.ParsePortIdx(args[0])
	if err != nil {
		send(proto.FrameResponse, ui.Errorf("%v", err))
		return
	}

	sessions := s.sessions.ByPort(port)
	if len(sessions) == 0 {
		send(proto.FrameResponse, ui.Errorf("no active connections on port %d", port))
		return
	}

	var sess *session.Session
	var selectedIdx int
	if idx == 0 {
		if len(sessions) > 1 {
			var sb strings.Builder
			sb.WriteString(fmt.Sprintf("  Multiple sessions on port %d — specify an index:\n", port))
			for i, ss := range sessions {
				sb.WriteString(fmt.Sprintf("    use %d:%d  (%s)\n", port, i+1, ss.RemoteAddr))
			}
			send(proto.FrameResponse, sb.String())
			return
		}
		sess = sessions[0]
		selectedIdx = 1
	} else {
		if idx > len(sessions) {
			send(proto.FrameResponse, ui.Errorf("index %d out of range", idx))
			return
		}
		sess = sessions[idx-1]
		selectedIdx = idx
	}

	// Tell the client to switch to session-mode prompt.
	send(proto.FrameSessionStart, fmt.Sprintf("%d:%d:%s", sess.Port, selectedIdx, sess.RemoteAddr))

	sess.SetState(session.StateForeground)
	defer func() {
		if sess.State() == session.StateForeground {
			sess.SetState(session.StateBackground)
		}
	}()

	// rxDone is closed by the RX goroutine when the session dies.
	rxDone := make(chan struct{})
	// exitFlag tells the RX goroutine to stop when we exit via +back/+bg.
	var exitFlag atomic.Bool

	// RX goroutine: drain session buffer → client as FrameEvent.
	go func() {
		defer close(rxDone)
		for {
			sess.RxMu().Lock()
			for len(sess.RxBuf()) == 0 && sess.State() != session.StateDead && !exitFlag.Load() {
				sess.RxCond().Wait()
			}
			data := sess.DrainRxBuf()
			isDead := sess.State() == session.StateDead
			done := exitFlag.Load()
			sess.RxMu().Unlock()

			if len(data) > 0 {
				send(proto.FrameEvent, string(data))
			}
			if isDead {
				send(proto.FrameEvent, fmt.Sprintf("[!] Session %d closed by remote.", sess.ID))
				send(proto.FrameResponse, "  Returning to menu.")
				return
			}
			if done {
				return
			}
		}
	}()

	// Passthrough loop: client lines → session TX.
	for {
		select {
		case <-rxDone:
			// Session died; RX goroutine already sent FrameResponse.
			return
		case line, ok := <-cmdCh:
			if !ok {
				exitFlag.Store(true)
				sess.RxCond().Broadcast()
				return
			}
			switch strings.TrimSpace(line) {
			case "+back":
				exitFlag.Store(true)
				sess.RxCond().Broadcast()
				send(proto.FrameResponse, "  Returning to menu. Session is still open.")
				return
			case "+bg", "+background":
				exitFlag.Store(true)
				sess.RxCond().Broadcast()
				sess.SetState(session.StateBackground)
				send(proto.FrameResponse, fmt.Sprintf("  Session %d sent to background.", sess.ID))
				return
			case "+exit", "exit":
				exitFlag.Store(true)
				sess.RxCond().Broadcast()
				send(proto.FrameResponse, "Bye.")
				return
			default:
				if sess.State() == session.StateDead {
					// rxDone will fire in the next select iteration.
					continue
				}
				// !<cmd> runs locally on the server, output sent back as FrameEvent.
				if strings.HasPrefix(line, "!") && len(line) > 1 {
					out, err := executor.Run(line[1:])
					if err != nil {
						out = ui.Errorf("system: %v", err)
					}
					send(proto.FrameEvent, out)
					continue
				}
				if _, writeErr := sess.Write([]byte(line + "\n")); writeErr != nil {
					sess.SetState(session.StateDead)
					sess.RxCond().Broadcast()
				}
				// Output will arrive via FrameEvent — no FrameResponse sent here.
			}
		case <-ctx.Done():
			exitFlag.Store(true)
			sess.RxCond().Broadcast()
			return
		}
	}
}
