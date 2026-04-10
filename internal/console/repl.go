package console

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"syscall"
	"github.com/nchgroup/tcpsh/internal/config"
	"github.com/nchgroup/tcpsh/internal/forward"
	"github.com/nchgroup/tcpsh/internal/history"
	"github.com/nchgroup/tcpsh/internal/listener"
	"github.com/nchgroup/tcpsh/internal/session"
	"github.com/nchgroup/tcpsh/pkg/ui"

	"github.com/chzyer/readline"
)

// REPL is the interactive shell for tcpsh.
type REPL struct {
	cfg        *config.Config
	sessions   *session.Manager
	listeners  *listener.Manager
	forwards   *forward.Manager
	dispatcher *Dispatcher
	signals    *SignalHandler
	hist       *history.Global
	rl         *readline.Instance
	ctx        context.Context
	cancel     context.CancelFunc
}

// New creates a REPL. Call Run() to start the interactive loop.
func New(cfg *config.Config) (*REPL, error) {
	ctx, cancel := context.WithCancel(context.Background())

	sessMgr := session.NewManager()
	fwdMgr := forward.NewManager(cfg.DialTimeout)

	hist, err := history.NewGlobal(cfg.HistoryFile)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("history: %w", err)
	}

	rl, err := readline.NewEx(&readline.Config{
		Prompt:          cfg.Prompt,
		HistoryFile:     hist.Path(),
		HistoryLimit:    cfg.HistorySize,
		InterruptPrompt: "", // Ctrl+C returns readline.ErrInterrupt, handled below
		EOFPrompt:       "exit",
		AutoComplete:    buildCompleter(),
	})
	if err != nil {
		cancel()
		return nil, fmt.Errorf("readline: %w", err)
	}

	// Create listener manager after readline so it can emit to same context.
	listenMgr := listener.NewManager(sessMgr)

	r := &REPL{
		cfg:       cfg,
		sessions:  sessMgr,
		listeners: listenMgr,
		forwards:  fwdMgr,
		signals:   NewSignalHandler(),
		hist:      hist,
		rl:        rl,
		ctx:       ctx,
		cancel:    cancel,
	}
	r.dispatcher = NewDispatcher(ctx, sessMgr, listenMgr, fwdMgr)

	// Start background event printer.
	go r.eventLoop()

	return r, nil
}

// Run starts the interactive REPL. Blocks until the user exits.
func (r *REPL) Run() error {
	if !r.cfg.Quiet {
		fmt.Print(ui.Banner("1.0.0"))
		fmt.Println(ui.StyleMuted.Render("  Type 'help' for commands. Ctrl+C will not exit — use '+exit' or 'exit'."))
		fmt.Println()
	}

	for {
		line, err := r.rl.Readline()
		if err != nil {
			if err == readline.ErrInterrupt {
				// Ctrl+C in menu mode — do not exit.
				fmt.Println(ui.StyleMuted.Render("  [Tip: use 'exit' or '+exit' to quit]"))
				continue
			}
			if err == io.EOF {
				return r.doExit(false)
			}
			return err
		}

		cmd := Parse(line, false)
		switch cmd.Kind {
		case CmdEmpty:
			continue
		case CmdSystem:
			out := r.dispatcher.RunSystem(cmd.Args[0])
			fmt.Print(out)
			if len(out) > 0 && out[len(out)-1] != '\n' {
				fmt.Println()
			}
		case CmdSpecial:
			if cmd.Verb == "+exit" {
				return r.doExit(true)
			}
			// Other special commands are session-mode only.
			fmt.Println(ui.StyleMuted.Render("  Special commands (+back, +bg) are only available in session mode."))
		case CmdTool:
			if cmd.Verb == "exit" {
				return r.doExit(true)
			}
			if cmd.Verb == "use" {
				r.doUse(cmd.Args)
				continue
			}
			out := r.dispatcher.Dispatch(cmd)
			fmt.Println(out)
		}
	}
}

// doExit performs a graceful shutdown.
func (r *REPL) doExit(confirm bool) error {
	active := r.sessions.All()
	if confirm && len(active) > 0 {
		fmt.Printf(ui.StyleWarn.Render("  [!] %d active session(s) will be lost. Confirm exit? [y/N] "), len(active))
		var answer string
		fmt.Scanln(&answer)
		if strings.ToLower(strings.TrimSpace(answer)) != "y" {
			return nil // stay running
		}
	}
	r.signals.Stop()
	r.cancel()
	r.listeners.CloseAll()
	r.forwards.CloseAll()
	_ = r.rl.Close()
	fmt.Println(ui.StyleMuted.Render("Bye."))
	return nil
}

// doUse enters session mode for a specific connection.
func (r *REPL) doUse(args []string) {
	if len(args) == 0 {
		fmt.Println(ui.Errorf("usage: use <port>[:<idx>]"))
		return
	}

	port, idx, err := ParsePortIdx(args[0])
	if err != nil {
		fmt.Println(ui.Errorf("%v", err))
		return
	}

	sessions := r.sessions.ByPort(port)
	if len(sessions) == 0 {
		fmt.Println(ui.Errorf("no active connections on port %d", port))
		return
	}

	var s *session.Session
	var selectedIdx int
	if idx == 0 {
		if len(sessions) > 1 {
			// Show numbered list and prompt for selection.
			fmt.Printf(ui.StyleBold.Render("  Multiple connections on port %d:\n"), port)
			for i, sess := range sessions {
				fmt.Printf("  %s  %s\n",
					ui.StyleBold.Render(strconv.Itoa(i+1)),
					sess.RemoteAddr,
				)
			}
			fmt.Print(ui.StylePrompt.Render("Select [1]: "))
			var choice string
			fmt.Scanln(&choice)
			choice = strings.TrimSpace(choice)
			if choice == "" {
				choice = "1"
			}
			n, err := strconv.Atoi(choice)
			if err != nil || n < 1 || n > len(sessions) {
				fmt.Println(ui.Errorf("invalid selection"))
				return
			}
			s = sessions[n-1]
			selectedIdx = n
		} else {
			s = sessions[0]
			selectedIdx = 1
		}
	} else {
		if idx > len(sessions) {
			fmt.Println(ui.Errorf("index %d out of range", idx))
			return
		}
		s = sessions[idx-1]
		selectedIdx = idx
	}

	r.enterSession(s, selectedIdx)
}

// enterSession switches the REPL to session mode for the given session.
// idx is the 1-based position of s within its port's session list (for the prompt).
func (r *REPL) enterSession(s *session.Session, idx int) {
	s.SetState(session.StateForeground)
	defer func() {
		if s.State() == session.StateForeground {
			s.SetState(session.StateBackground)
		}
		r.rl.SetPrompt(r.cfg.Prompt)
	}()

	prompt := fmt.Sprintf("[%d:%d]> ", s.Port, idx)
	r.rl.SetPrompt(prompt)

	fmt.Println(ui.StyleInfo.Render(fmt.Sprintf(
		"  Entering session %d (%s). Type '+back' to return, '+bg' to background, '+exit' to quit.",
		s.ID, s.RemoteAddr,
	)))

	// Goroutine: drain RX buffer → stdout.
	// Reads from the session's rxBuf (populated by StartRxLoop) rather than
	// directly from s.Conn, so there is only one goroutine reading the conn.
	remoteDone := make(chan struct{})
	go func() {
		defer close(remoteDone)
		for {
			s.RxMu().Lock()
			for len(s.RxBuf()) == 0 && s.State() != session.StateDead {
				s.RxCond().Wait()
			}
			data := s.DrainRxBuf()
			isDead := s.State() == session.StateDead
			s.RxMu().Unlock()

			if len(data) > 0 {
				os.Stdout.Write(data)
			}
			if isDead {
				if s.State() == session.StateDead {
					fmt.Println(ui.Warnf("Session %d closed by remote.", s.ID))
				}
				return
			}
		}
	}()

	// Main loop: stdin (via readline) → remote
	for {
		line, err := r.rl.Readline()

		if err == readline.ErrInterrupt {
			// Ctrl+C: do NOT close session.
			fmt.Println(ui.StyleMuted.Render("  [Use '+back' to return to menu, '+exit' to quit]"))
			continue
		}
		if err == io.EOF {
			break
		}

		cmd := Parse(line, true)
		switch cmd.Kind {
		case CmdSpecial:
			switch cmd.Verb {
			case "+back":
				fmt.Println(ui.StyleMuted.Render("  Returning to menu. Session is still open."))
				return
			case "+bg", "+background":
				s.SetState(session.StateBackground)
				fmt.Println(ui.StyleBackground.Render(fmt.Sprintf("  Session %d sent to background.", s.ID)))
				return
			case "+exit":
				r.doExit(true)
				os.Exit(0)
			}
		case CmdSystem:
			out := r.dispatcher.RunSystem(cmd.Args[0])
			fmt.Print(out)
			if len(out) > 0 && out[len(out)-1] != '\n' {
				fmt.Println()
			}
		case CmdPassthrough, CmdTool, CmdEmpty:
			// Send raw line + newline to the remote connection.
			payload := line + "\n"
			if cmd.Kind == CmdEmpty {
				payload = "\n"
			}
			if s.State() == session.StateDead {
				fmt.Println(ui.Errorf("Session is dead. Use '+back' to return to menu."))
				continue
			}
			_, err := s.Write([]byte(payload))
			if err != nil {
				s.SetState(session.StateDead)
				fmt.Println(ui.Errorf("Write error: %v", err))
				return
			}
			s.AddHistory(line)
		}

		// Check if remote side closed while we were reading.
		select {
		case <-remoteDone:
			return
		default:
		}
	}
}

// eventLoop prints listener events (new connections, disconnections) inline.
func (r *REPL) eventLoop() {
	for {
		select {
		case <-r.ctx.Done():
			return
		case ev, ok := <-r.listeners.Events():
			if !ok {
				return
			}
			switch ev.Type {
			case listener.EventNewConn:
				msg := ui.StyleActive.Render(fmt.Sprintf(
					"\r[+] New connection on :%d from %s (session %d)\n",
					ev.Port, ev.Session.RemoteAddr, ev.Session.ID,
				))
				fmt.Print(msg)
				r.rl.Refresh()
			case listener.EventConnDead:
				msg := ui.StyleMuted.Render(fmt.Sprintf(
					"\r[-] Session %d (%s) disconnected from :%d\n",
					ev.Session.ID, ev.Session.RemoteAddr, ev.Port,
				))
				fmt.Print(msg)
				r.rl.Refresh()
				r.sessions.Remove(ev.Session.ID)
			case listener.EventError:
				fmt.Print(ui.Errorf("\r[listener :%d] %v\n", ev.Port, ev.Err))
				r.rl.Refresh()
			}
		case sig := <-r.signals.Chan():
			if sig == syscall.SIGTERM {
				// Graceful shutdown on SIGTERM.
				r.doExit(false)
				os.Exit(0)
			}
			// SIGINT is also handled per readline loop above; this is a fallback.
		}
	}
}

// buildCompleter returns tab-completion suggestions.
func buildCompleter() readline.AutoCompleter {
	return readline.NewPrefixCompleter(
		readline.PcItem("open"),
		readline.PcItem("close"),
		readline.PcItem("kill",
			readline.PcItem("-f"),
		),
		readline.PcItem("use"),
		readline.PcItem("info"),
		readline.PcItem("log"),
		readline.PcItem("list",
			readline.PcItem("ports"),
			readline.PcItem("conn"),
			readline.PcItem("fwd"),
			readline.PcItem("proxy"),
			readline.PcItem("all"),
		),
		readline.PcItem("fwd",
			readline.PcItem("list"),
			readline.PcItem("close"),
		),
		readline.PcItem("proxy",
			readline.PcItem("list"),
			readline.PcItem("close"),
			readline.PcItem("log"),
		),
		readline.PcItem("help",
			readline.PcItem("open"),
			readline.PcItem("close"),
			readline.PcItem("kill"),
			readline.PcItem("use"),
			readline.PcItem("fwd"),
			readline.PcItem("proxy"),
		),
		readline.PcItem("exit"),
		readline.PcItem("clear"),
	)
}
