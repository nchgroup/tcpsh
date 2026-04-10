// Package client implements the tcpsh -client mode.
//
// The client connects to a running tcpsh -server instance, performs the
// ChaCha20-Poly1305 encrypted handshake, and then provides a readline-based
// interactive REPL that sends encrypted command frames to the server and
// prints the server's encrypted responses.
package client

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"sync"

	"github.com/nchgroup/tcpsh/internal/proto"

	"github.com/chzyer/readline"
)

const prompt = "tcpsh> "

// Client connects to a tcpsh server and provides an interactive shell.
type Client struct {
	addr string
	key  [32]byte
}

// New creates a Client.  token is derived into a 32-byte key via SHA-256.
func New(addr, token string) *Client {
	return &Client{
		addr: addr,
		key:  proto.TokenToKey(token),
	}
}

// Run connects to the server, authenticates, and enters the interactive loop.
// It blocks until the user types exit/+exit or the server disconnects.
func (c *Client) Run() error {
	conn, err := net.Dial("tcp", c.addr)
	if err != nil {
		return fmt.Errorf("client: connect %s: %w", c.addr, err)
	}
	defer conn.Close()

	br := bufio.NewReaderSize(conn, 64*1024)

	if err := proto.SendHandshake(conn, br, c.key); err != nil {
		return fmt.Errorf("client: authentication failed — wrong token? (%w)", err)
	}

	fmt.Printf("  Connected to tcpsh server at %s\n", c.addr)
	fmt.Printf("  Type 'help' for commands. Type 'exit' or '+exit' to disconnect.\n\n")

	rl, err := readline.NewEx(&readline.Config{
		Prompt:          prompt,
		InterruptPrompt: "",
		EOFPrompt:       "exit",
	})
	if err != nil {
		return fmt.Errorf("client: readline: %w", err)
	}
	defer rl.Close()

	// mu serializes all terminal output so events and responses never interleave.
	var mu sync.Mutex

	eventCh := make(chan string, 32)
	responseCh := make(chan string, 1)
	readErrCh := make(chan error, 1)

	// Reader goroutine: continuously reads typed frames and routes them.
	go func() {
		for {
			typ, payload, err := proto.ReadTypedFrame(br, c.key)
			if err != nil {
				readErrCh <- err
				return
			}
			switch typ {
			case proto.FrameEvent:
				eventCh <- string(payload)
			default:
				responseCh <- string(payload)
			}
		}
	}()

	// Event goroutine: prints push events the moment they arrive.
	go func() {
		for msg := range eventCh {
			mu.Lock()
			rl.Clean()
			fmt.Println(msg)
			rl.Refresh()
			mu.Unlock()
		}
	}()

	for {
		line, err := rl.Readline()
		if err != nil {
			if err == readline.ErrInterrupt {
				fmt.Println("  [Tip: use 'exit' or '+exit' to disconnect]")
				continue
			}
			if err == io.EOF {
				break
			}
			return err
		}

		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		if err := proto.WriteFrame(conn, c.key, []byte(trimmed)); err != nil {
			return fmt.Errorf("client: send: %w", err)
		}

		// Wait for the server's response frame (or a read error).
		select {
		case resp := <-responseCh:
			mu.Lock()
			rl.Clean()
			fmt.Println(resp)
			mu.Unlock()
		case err := <-readErrCh:
			if err != io.EOF && !isClosedErr(err) {
				fmt.Fprintf(os.Stderr, "\n  [!] Server disconnected: %v\n", err)
			} else {
				fmt.Println("\n  [!] Server disconnected.")
			}
			return nil
		}

		lower := strings.ToLower(trimmed)
		if lower == "exit" || lower == "+exit" {
			return nil
		}
	}

	return nil
}

// isClosedErr reports whether err is a "use of closed network connection"
// or similar benign disconnect error.
func isClosedErr(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "use of closed") ||
		strings.Contains(s, "EOF") ||
		strings.Contains(s, "connection reset")
}
