package console

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"tcpsh/internal/executor"
	"tcpsh/internal/forward"
	"tcpsh/internal/listener"
	"tcpsh/internal/session"
	"tcpsh/pkg/ui"
	"tcpsh/pkg/ui/views"
)

// Dispatcher routes parsed commands to the appropriate handler.
type Dispatcher struct {
	ctx       context.Context
	sessions  *session.Manager
	listeners *listener.Manager
	forwards  *forward.Manager
}

// NewDispatcher creates a Dispatcher wired to the given managers.
func NewDispatcher(ctx context.Context, s *session.Manager, l *listener.Manager, f *forward.Manager) *Dispatcher {
	return &Dispatcher{ctx: ctx, sessions: s, listeners: l, forwards: f}
}

// Dispatch executes a tool command and returns output to print.
func (d *Dispatcher) Dispatch(cmd Cmd) string {
	switch cmd.Verb {
	case "open":
		return d.doOpen(cmd.Args)
	case "close":
		return d.doClose(cmd.Args)
	case "kill":
		return d.doKill(cmd.Args)
	case "list":
		return d.doList(cmd.Args)
	case "info":
		return d.doInfo(cmd.Args)
	case "log":
		return d.doLog(cmd.Args)
	case "fwd":
		return d.doFwd(cmd.Args)
	case "proxy":
		return d.doProxy(cmd.Args)
	case "send":
		return d.doSend(cmd.Args)
	case "read":
		return d.doRead(cmd.Args)
	case "help":
		return d.doHelp(cmd.Args)
	case "clear":
		return "\033[H\033[2J"
	default:
		return ui.Errorf("unknown command %q — type 'help' for usage", cmd.Verb)
	}
}

// RunSystem executes a shell command (! prefix) and returns output.
func (d *Dispatcher) RunSystem(cmdStr string) string {
	out, err := executor.Run(cmdStr)
	if err != nil {
		return ui.Errorf("system: %v", err)
	}
	return out
}

// --- open ---

func (d *Dispatcher) doOpen(args []string) string {
	if len(args) == 0 {
		return ui.Errorf("usage: open <port> [host]")
	}
	port, err := strconv.Atoi(args[0])
	if err != nil || port < 1 || port > 65535 {
		return ui.Errorf("invalid port: %s", args[0])
	}
	host := ""
	if len(args) > 1 {
		host = args[1]
	}
	if err := d.listeners.Open(d.ctx, port, host); err != nil {
		return ui.Errorf("%v", err)
	}
	addr := fmt.Sprintf("%s:%d", host, port)
	if host == "" {
		addr = fmt.Sprintf("0.0.0.0:%d", port)
	}
	return ui.StyleActive.Render(fmt.Sprintf("[+] Listening on %s", addr))
}

// --- close ---

func (d *Dispatcher) doClose(args []string) string {
	if len(args) == 0 {
		return ui.Errorf("usage: close <port>")
	}
	port, err := strconv.Atoi(args[0])
	if err != nil {
		return ui.Errorf("invalid port: %s", args[0])
	}
	if err := d.listeners.Close(port); err != nil {
		return ui.Errorf("%v", err)
	}
	return ui.StyleMuted.Render(fmt.Sprintf("[-] Port %d closed.", port))
}

// --- kill ---

func (d *Dispatcher) doKill(args []string) string {
	force := false
	filtered := args[:0]
	for _, a := range args {
		if a == "-f" {
			force = true
		} else {
			filtered = append(filtered, a)
		}
	}
	args = filtered

	if len(args) == 0 {
		return ui.Errorf("usage: kill [-f] <port>[:<idx>]")
	}

	port, idx, err := ParsePortIdx(args[0])
	if err != nil {
		return ui.Errorf("%v", err)
	}

	sessions := d.sessions.ByPort(port)
	if len(sessions) == 0 {
		return ui.Errorf("no active connections on port %d", port)
	}

	var targets []*session.Session
	if idx == 0 {
		targets = sessions
	} else {
		if idx > len(sessions) {
			return ui.Errorf("index %d out of range (port %d has %d connection(s))", idx, port, len(sessions))
		}
		targets = []*session.Session{sessions[idx-1]}
	}

	for _, s := range targets {
		if force {
			s.ForceClose()
		} else {
			s.Close()
		}
		d.sessions.Remove(s.ID)
	}

	method := "FIN"
	if force {
		method = "RST"
	}
	return ui.StyleWarn.Render(fmt.Sprintf("[~] %d connection(s) terminated on port %d (%s)", len(targets), port, method))
}

// --- list ---

func (d *Dispatcher) doList(args []string) string {
	sub := "all"
	if len(args) > 0 {
		sub = strings.ToLower(args[0])
	}

	switch sub {
	case "ports":
		return d.renderPorts()
	case "conn", "connections":
		return views.RenderSessions(d.sessions.All())
	case "fwd", "forwards":
		return views.RenderForwards(d.forwards.Forwards())
	case "proxy", "proxies":
		return views.RenderForwards(d.forwards.Proxies())
	case "all":
		var sb strings.Builder
		sb.WriteString(ui.StyleBold.Render("Listeners") + "\n")
		sb.WriteString(d.renderPorts())
		sb.WriteString("\n")
		sb.WriteString(ui.StyleBold.Render("Connections") + "\n")
		sb.WriteString(views.RenderSessions(d.sessions.All()))
		sb.WriteString("\n")
		sb.WriteString(ui.StyleBold.Render("Forwards & Proxies") + "\n")
		sb.WriteString(views.RenderForwards(d.forwards.All()))
		return sb.String()
	default:
		return ui.Errorf("usage: list [ports|conn|fwd|proxy|all]")
	}
}

func (d *Dispatcher) renderPorts() string {
	ports := d.listeners.OpenPorts()
	sort.Ints(ports)
	rows := make([]views.PortRow, 0, len(ports))
	for _, p := range ports {
		l := d.listeners.Get(p)
		host := ""
		if l != nil {
			host = l.Host
		}
		rows = append(rows, views.PortRow{
			Port:     p,
			Host:     host,
			Sessions: len(d.sessions.ByPort(p)),
		})
	}
	return views.RenderPorts(rows)
}

// --- info ---

func (d *Dispatcher) doInfo(args []string) string {
	if len(args) == 0 {
		return ui.Errorf("usage: info <port>[:<idx>]")
	}
	port, idx, err := ParsePortIdx(args[0])
	if err != nil {
		return ui.Errorf("%v", err)
	}

	sessions := d.sessions.ByPort(port)
	if len(sessions) == 0 {
		return ui.Errorf("no active connections on port %d", port)
	}

	targets := sessions
	if idx > 0 {
		if idx > len(sessions) {
			return ui.Errorf("index %d out of range", idx)
		}
		targets = []*session.Session{sessions[idx-1]}
	}

	var sb strings.Builder
	for _, s := range targets {
		sb.WriteString(fmt.Sprintf(
			"  ID:       %s\n  Port:     %d\n  Remote:   %s\n  State:    %s\n  TX:       %d bytes\n  RX:       %d bytes\n  Duration: %s\n\n",
			ui.StyleBold.Render(strconv.Itoa(s.ID)),
			s.Port,
			s.RemoteAddr,
			s.State().String(),
			s.BytesTX.Load(),
			s.BytesRX.Load(),
			s.Duration().Round(1e9),
		))
	}
	return sb.String()
}

// --- log ---

func (d *Dispatcher) doLog(args []string) string {
	if len(args) < 2 {
		return ui.Errorf("usage: log <port>[:<idx>] <file>")
	}
	// Logging for raw sessions is not currently buffered server-side (data flows
	// through the REPL). Return a helpful note.
	return ui.Warnf("Traffic logging for session data is available on proxy entries.\nUse 'proxy log <local-port> <file>' for proxy traffic logging.")
}

// --- fwd ---

func (d *Dispatcher) doFwd(args []string) string {
	if len(args) == 0 {
		return ui.Errorf("usage: fwd <local-port> <remote-host>:<remote-port>  |  fwd list  |  fwd close <local-port>")
	}

	switch strings.ToLower(args[0]) {
	case "list":
		return views.RenderForwards(d.forwards.Forwards())
	case "close":
		if len(args) < 2 {
			return ui.Errorf("usage: fwd close <local-port>")
		}
		port, err := strconv.Atoi(args[1])
		if err != nil {
			return ui.Errorf("invalid port: %s", args[1])
		}
		if err := d.forwards.Close(port); err != nil {
			return ui.Errorf("%v", err)
		}
		return ui.StyleMuted.Render(fmt.Sprintf("[-] Forward on :%d closed.", port))
	}

	// fwd <local-port> <remote-host>:<remote-port>
	if len(args) < 2 {
		return ui.Errorf("usage: fwd <local-port> <remote-host>:<remote-port>")
	}
	localPort, err := strconv.Atoi(args[0])
	if err != nil {
		return ui.Errorf("invalid local port: %s", args[0])
	}
	remoteHost, remotePort, err := ParseRemote(args[1])
	if err != nil {
		return ui.Errorf("%v", err)
	}
	if err := d.forwards.Open(d.ctx, localPort, remoteHost, remotePort); err != nil {
		return ui.Errorf("%v", err)
	}
	return ui.StyleForward.Render(fmt.Sprintf("[fwd] :%d  ──►  %s:%d", localPort, remoteHost, remotePort))
}

// --- proxy ---

func (d *Dispatcher) doProxy(args []string) string {
	if len(args) == 0 {
		return ui.Errorf("usage: proxy <local-port> <remote-host>:<remote-port>  |  proxy list  |  proxy close <local-port>  |  proxy log <local-port> <file>")
	}

	switch strings.ToLower(args[0]) {
	case "list":
		return views.RenderForwards(d.forwards.Proxies())
	case "close":
		if len(args) < 2 {
			return ui.Errorf("usage: proxy close <local-port>")
		}
		port, err := strconv.Atoi(args[1])
		if err != nil {
			return ui.Errorf("invalid port: %s", args[1])
		}
		if err := d.forwards.Close(port); err != nil {
			return ui.Errorf("%v", err)
		}
		return ui.StyleMuted.Render(fmt.Sprintf("[-] Proxy on :%d closed.", port))
	case "log":
		if len(args) < 3 {
			return ui.Errorf("usage: proxy log <local-port> <file>")
		}
		port, err := strconv.Atoi(args[1])
		if err != nil {
			return ui.Errorf("invalid port: %s", args[1])
		}
		entry := d.forwards.Get(port)
		if entry == nil {
			return ui.Errorf("no proxy on port %d", port)
		}
		if err := entry.SetLogFile(args[2]); err != nil {
			return ui.Errorf("%v", err)
		}
		return ui.StyleProxy.Render(fmt.Sprintf("[proxy :%d] logging to %s", port, args[2]))
	}

	// proxy <local-port> <remote-host>:<remote-port> [logfile]
	if len(args) < 2 {
		return ui.Errorf("usage: proxy <local-port> <remote-host>:<remote-port> [logfile]")
	}
	localPort, err := strconv.Atoi(args[0])
	if err != nil {
		return ui.Errorf("invalid local port: %s", args[0])
	}
	remoteHost, remotePort, err := ParseRemote(args[1])
	if err != nil {
		return ui.Errorf("%v", err)
	}
	logFile := ""
	if len(args) > 2 {
		logFile = args[2]
	}
	if err := d.forwards.OpenProxy(d.ctx, localPort, remoteHost, remotePort, logFile); err != nil {
		return ui.Errorf("%v", err)
	}
	return ui.StyleProxy.Render(fmt.Sprintf("[proxy] :%d  ──►  %s:%d", localPort, remoteHost, remotePort))
}

// --- send ---

// doSend sends data to a session: send <port>[:<idx>] <data...>
// A newline is appended automatically.
func (d *Dispatcher) doSend(args []string) string {
	if len(args) < 2 {
		return ui.Errorf("usage: send <port>[:<idx>] <data>")
	}
	port, idx, err := ParsePortIdx(args[0])
	if err != nil {
		return ui.Errorf("%v", err)
	}
	sessions := d.sessions.ByPort(port)
	if len(sessions) == 0 {
		return ui.Errorf("no active connections on port %d", port)
	}
	if idx == 0 {
		idx = 1
	}
	if idx > len(sessions) {
		return ui.Errorf("index %d out of range (port %d has %d connection(s))", idx, port, len(sessions))
	}
	s := sessions[idx-1]
	if s.State() == session.StateDead {
		return ui.Errorf("session is dead")
	}
	payload := strings.Join(args[1:], " ") + "\n"
	n, err := s.Write([]byte(payload))
	if err != nil {
		return ui.Errorf("write error: %v", err)
	}
	return ui.StyleMuted.Render(fmt.Sprintf("[>] Sent %d bytes to session %d on port %d", n, s.ID, port))
}

// --- read ---

// doRead drains and returns buffered RX data from a session: read <port>[:<idx>]
func (d *Dispatcher) doRead(args []string) string {
	if len(args) == 0 {
		return ui.Errorf("usage: read <port>[:<idx>]")
	}
	port, idx, err := ParsePortIdx(args[0])
	if err != nil {
		return ui.Errorf("%v", err)
	}
	sessions := d.sessions.ByPort(port)
	if len(sessions) == 0 {
		return ui.Errorf("no active connections on port %d", port)
	}
	if idx == 0 {
		idx = 1
	}
	if idx > len(sessions) {
		return ui.Errorf("index %d out of range (port %d has %d connection(s))", idx, port, len(sessions))
	}
	s := sessions[idx-1]
	data := s.ReadBuffered()
	if data == nil {
		return ui.StyleMuted.Render(fmt.Sprintf("(no data buffered for session %d on port %d)", s.ID, port))
	}
	return string(data)
}

// --- help ---

func (d *Dispatcher) doHelp(args []string) string {
	if len(args) > 0 {
		return helpFor(args[0])
	}
	return helpGeneral()
}

func helpGeneral() string {
	return `
` + ui.StyleBold.Render("LISTENER MANAGEMENT") + `
  open <port> [host]          Open a TCP listener
  close <port>                Close listener and all connections
  kill [-f] <port>[:<idx>]    Terminate connection (FIN or RST with -f)

` + ui.StyleBold.Render("SESSION INTERACTION") + `
  use <port>[:<idx>]          Attach to a session (send/receive)
  info <port>[:<idx>]         Show session details and metrics
  send <port>[:<idx>] <data>  Send data to a session (appends newline)
  read <port>[:<idx>]         Read and drain buffered RX data from a session

` + ui.StyleBold.Render("LISTING") + `
  list ports                  Show open ports
  list conn                   Show active connections
  list fwd                    Show active forwards
  list proxy                  Show active proxies
  list all                    Show everything

` + ui.StyleBold.Render("FORWARDING & PROXY") + `
  fwd <lport> <host:rport>    Transparent TCP forward
  fwd list                    List forwards
  fwd close <lport>           Stop a forward
  proxy <lport> <host:rport>  TCP proxy with traffic logging
  proxy list                  List proxies
  proxy close <lport>         Stop a proxy
  proxy log <lport> <file>    Redirect proxy log to file

` + ui.StyleBold.Render("SESSION MODE COMMANDS") + `
  +back                       Return to menu (keep session open)
  +bg / +background           Send session to background
  +exit                       Exit tcpsh
  !<cmd>                      Run local system command

` + ui.StyleBold.Render("OTHER") + `
  help [cmd]                  Show help
  clear                       Clear screen
`
}

func helpFor(verb string) string {
	switch strings.ToLower(verb) {
	case "open":
		return "  open <port> [host]\n  Open a TCP listener. host defaults to 0.0.0.0 (all interfaces)."
	case "close":
		return "  close <port>\n  Stop the listener and close all its sessions."
	case "kill":
		return "  kill [-f] <port>[:<idx>]\n  -f forces RST; default sends FIN. idx selects a specific session."
	case "use":
		return "  use <port>[:<idx>]\n  Enter session mode. Raw input is sent to the TCP connection."
	case "fwd":
		return "  fwd <local-port> <host:port>   Start transparent forward.\n  fwd list | fwd close <lport>"
	case "proxy":
		return "  proxy <local-port> <host:port> [logfile]   Start proxy with logging.\n  proxy list | proxy close <lport> | proxy log <lport> <file>"
	default:
		return helpGeneral()
	}
}
