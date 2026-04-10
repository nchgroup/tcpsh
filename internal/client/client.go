// Package client implements the tcpsh --client mode.
//
// The client connects to a running tcpsh --server instance, performs the
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

	// done is closed when the server disconnects or we exit.
	done := make(chan struct{})

	// Reader goroutine: receive server frames → stdout.
	go func() {
		defer close(done)
		for {
			plain, err := proto.ReadFrame(br, c.key)
			if err != nil {
				if err != io.EOF && !isClosedErr(err) {
					fmt.Fprintf(os.Stderr, "\n  [!] Server disconnected: %v\n", err)
				} else {
					fmt.Println("\n  [!] Server disconnected.")
				}
				return
			}
			msg := string(plain)
			fmt.Println(msg)
		}
	}()

	// Local readline loop — no history file (client is a thin terminal).
	rl, err := readline.NewEx(&readline.Config{
		Prompt:          prompt,
		InterruptPrompt: "",
		EOFPrompt:       "exit",
	})
	if err != nil {
		return fmt.Errorf("client: readline: %w", err)
	}
	defer rl.Close()

	for {
		// Exit immediately if the server disconnected.
		select {
		case <-done:
			return nil
		default:
		}

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

		lower := strings.ToLower(trimmed)
		if lower == "exit" || lower == "+exit" {
			_ = proto.WriteFrame(conn, c.key, []byte(trimmed))
			return nil
		}

		if err := proto.WriteFrame(conn, c.key, []byte(trimmed)); err != nil {
			return fmt.Errorf("client: send: %w", err)
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
