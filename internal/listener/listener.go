package listener

import (
	"context"
	"fmt"
	"net"
	"tcpsh/internal/session"
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

// Start opens a TCP listener on host:port and begins accepting connections.
// Accepted sessions are registered in sessions; events are sent on events.
func Start(ctx context.Context, port int, host string, sessions *session.Manager, events chan<- Event) (*Listener, error) {
	addr := fmt.Sprintf("%s:%d", host, port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("listen %s: %w", addr, err)
	}

	ctx, cancel := context.WithCancel(ctx)
	l := &Listener{
		Port:     port,
		Host:     host,
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
