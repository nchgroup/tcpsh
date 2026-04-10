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
	sessionStartCh := make(chan string, 1) // payload = "port:idx:remote"
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
			case proto.FrameSessionStart:
				sessionStartCh <- string(payload)
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

		// If this was a `use` command, wait for either FrameSessionStart
		// (enter session mode) or FrameResponse (error — stay in menu mode).
		fields := strings.Fields(trimmed)
		if len(fields) > 0 && strings.ToLower(fields[0]) == "use" {
			select {
			case info := <-sessionStartCh:
				// info = "port:idx:remote"
				parts := strings.SplitN(info, ":", 3)
				sessionPrompt := prompt
				if len(parts) >= 2 {
					sessionPrompt = fmt.Sprintf("[%s:%s]> ", parts[0], parts[1])
				}
				mu.Lock()
				rl.Clean()
				remote := ""
				if len(parts) >= 3 {
					remote = parts[2]
				}
				fmt.Printf("  Entering session %s:%s (%s). Type '+back' to return, '+bg' to background, '+exit' to quit.\n",
					parts[0], parts[1], remote)
				rl.SetPrompt(sessionPrompt)
				rl.Refresh()
				mu.Unlock()

				// Session passthrough loop.
			sessionLoop:
				for {
					sline, serr := rl.Readline()
					if serr != nil {
						if serr == readline.ErrInterrupt {
							mu.Lock()
							rl.Clean()
							fmt.Println("  [Use '+back' to return to menu, '+exit' to quit]")
							rl.Refresh()
							mu.Unlock()
							continue
						}
						break sessionLoop
					}
					strimmed := strings.TrimSpace(sline)
					if strimmed == "" {
						strimmed = "\n"
					}
					if err2 := proto.WriteFrame(conn, c.key, []byte(strimmed)); err2 != nil {
						return fmt.Errorf("client: send: %w", err2)
					}
					// Wait for FrameResponse only on special exit commands.
					// Otherwise just fire-and-forget; output arrives via FrameEvent.
					switch strings.TrimSpace(strimmed) {
					case "+back", "+bg", "+background", "+exit", "exit":
						select {
						case resp := <-responseCh:
							mu.Lock()
							rl.Clean()
							fmt.Println(resp)
							rl.SetPrompt(prompt)
							rl.Refresh()
							mu.Unlock()
							if strings.TrimSpace(strimmed) == "+exit" || strings.TrimSpace(strimmed) == "exit" {
								return nil
							}
						case err2 := <-readErrCh:
							_ = err2
							fmt.Println("\n  [!] Server disconnected.")
							return nil
						}
						break sessionLoop
					}
				}
				continue
			case resp := <-responseCh:
				// Server returned an error for the use command.
				mu.Lock()
				rl.Clean()
				fmt.Println(resp)
				rl.Refresh()
				mu.Unlock()
			case err2 := <-readErrCh:
				if err2 != io.EOF && !isClosedErr(err2) {
					fmt.Fprintf(os.Stderr, "\n  [!] Server disconnected: %v\n", err2)
				} else {
					fmt.Println("\n  [!] Server disconnected.")
				}
				return nil
			}
			continue
		}

		// Wait for the server's response frame (or a read error).
		select {
		case resp := <-responseCh:
			mu.Lock()
			rl.Clean()
			fmt.Println(resp)
			rl.Refresh()
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
