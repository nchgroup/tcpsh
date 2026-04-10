package listener

import (
	"context"
	"fmt"
	"net"

	"github.com/nchgroup/tcpsh/internal/session"
)

// Event is emitted by a Listener when something noteworthy happens.
type Event struct {
	Port    int
	Type    EventType
	Session *session.Session // non-nil for NewConn / ConnDead
	Err     error
}

// EventType categorises a listener Event.
type EventType int

const (
	EventNewConn  EventType = iota // a new connection was accepted
	EventConnDead                  // a session's connection died
	EventClosed                    // the listener itself was closed
	EventError                     // non-fatal listener error
)

// Listener wraps a net.Listener and runs an Accept loop in a goroutine.
type Listener struct {
	Port     int
	Host     string
	ln       net.Listener
	sessions *session.Manager
	events   chan<- Event
	cancel   context.CancelFunc
}

// resolveHost converts host to a bind address string.
// If host is empty it returns "" (bind all interfaces).
// If host is a valid IP it is returned as-is.
// Otherwise it is treated as a network interface name and its first IPv4
// address is returned, e.g. "tun0" → "10.10.14.3".
func resolveHost(host string) (string, error) {
	if host == "" {
		return "", nil
	}
	if net.ParseIP(host) != nil {
		return host, nil
	}
	iface, err := net.InterfaceByName(host)
	if err != nil {
		return "", fmt.Errorf("unknown host or interface %q: %w", host, err)
	}
	addrs, err := iface.Addrs()
	if err != nil {
		return "", fmt.Errorf("interface %s: %w", host, err)
	}
	for _, a := range addrs {
		var ip net.IP
		switch v := a.(type) {
		case *net.IPNet:
			ip = v.IP
		case *net.IPAddr:
			ip = v.IP
		}
		if ip4 := ip.To4(); ip4 != nil {
			return ip4.String(), nil
		}
	}
	return "", fmt.Errorf("interface %s has no IPv4 address", host)
}

// Start opens a TCP listener on host:port and begins accepting connections.
// host may be an IP address, a network interface name (e.g. "tun0"), or empty
// to bind all interfaces. Accepted sessions are registered in sessions; events
// are sent on events.
func Start(ctx context.Context, port int, host string, sessions *session.Manager, events chan<- Event) (*Listener, error) {
	bindIP, err := resolveHost(host)
	if err != nil {
		return nil, err
	}
	addr := fmt.Sprintf("%s:%d", bindIP, port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("listen %s: %w", addr, err)
	}

	ctx, cancel := context.WithCancel(ctx)
	l := &Listener{
		Port:     port,
		Host:     bindIP,
		ln:       ln,
		sessions: sessions,
		events:   events,
		cancel:   cancel,
	}

	go l.acceptLoop(ctx)
	return l, nil
}

// Close stops the listener and cleans up all associated sessions.
func (l *Listener) Close() {
	l.cancel()
	_ = l.ln.Close()
}

func (l *Listener) acceptLoop(ctx context.Context) {
	defer func() {
		l.events <- Event{Port: l.Port, Type: EventClosed}
	}()

	for {
		conn, err := l.ln.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return
			default:
				l.events <- Event{Port: l.Port, Type: EventError, Err: err}
				return
			}
		}

		s := l.sessions.Accept(l.Port, conn)
		s.StartRxLoop() // single owner of conn reads
		l.events <- Event{Port: l.Port, Type: EventNewConn, Session: s}

		// Spawn a goroutine to watch for connection closure.
		go l.watchSession(ctx, s)
	}
}

func (l *Listener) watchSession(ctx context.Context, s *session.Session) {
	// Wait until the session transitions to Dead (detected by StartRxLoop) or
	// the listener context is cancelled.
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.WaitDead()
	}()

	select {
	case <-ctx.Done():
	case <-done:
		if s.State() == session.StateDead {
			l.events <- Event{Port: l.Port, Type: EventConnDead, Session: s}
		}
	}
}
