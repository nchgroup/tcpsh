package session

import (
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// Session represents a single accepted TCP connection on a listener port.
type Session struct {
	ID          int
	Port        int
	RemoteAddr  string
	Conn        net.Conn
	ConnectedAt time.Time

	// Metrics — updated atomically.
	BytesTX atomic.Int64
	BytesRX atomic.Int64

	mu      sync.Mutex
	state   State
	history []string // per-session command history (in-memory)

	// RX buffer: all data read from Conn is funnelled through StartRxLoop()
	// so that only one goroutine ever reads from Conn.
	rxMu   sync.Mutex
	rxCond *sync.Cond
	rxBuf  []byte
}

func newSession(id, port int, conn net.Conn) *Session {
	s := &Session{
		ID:          id,
		Port:        port,
		RemoteAddr:  conn.RemoteAddr().String(),
		Conn:        conn,
		ConnectedAt: time.Now(),
		state:       StateActive,
	}
	s.rxCond = sync.NewCond(&s.rxMu)
	return s
}

// State returns the current state (thread-safe).
func (s *Session) State() State {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}

// SetState updates the state (thread-safe).
func (s *Session) SetState(st State) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state = st
}

// AddHistory appends a command to the per-session in-memory history.
func (s *Session) AddHistory(line string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.history = append(s.history, line)
}

// History returns a copy of the per-session command history.
func (s *Session) History() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := make([]string, len(s.history))
	copy(cp, s.history)
	return cp
}

// Duration returns time elapsed since connection.
func (s *Session) Duration() time.Duration {
	return time.Since(s.ConnectedAt)
}

// StartRxLoop starts a goroutine that continuously reads from s.Conn and
// appends data to the internal RX buffer. It is the sole reader of s.Conn,
// preventing concurrent read races when the session is in foreground mode.
// The goroutine exits when the connection is closed (Read returns an error).
func (s *Session) StartRxLoop() {
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := s.Conn.Read(buf)
			if n > 0 {
				s.BytesRX.Add(int64(n))
				chunk := make([]byte, n)
				copy(chunk, buf[:n])
				s.rxMu.Lock()
				s.rxBuf = append(s.rxBuf, chunk...)
				s.rxCond.Broadcast()
				s.rxMu.Unlock()
			}
			if err != nil {
				if s.State() != StateDead {
					s.SetState(StateDead)
				}
				// Wake any waiter so it can detect the dead state.
				s.rxCond.Broadcast()
				return
			}
		}
	}()
}

// ReadBuffered drains and returns all data currently in the RX buffer.
// Returns nil if the buffer is empty.
func (s *Session) ReadBuffered() []byte {
	s.rxMu.Lock()
	defer s.rxMu.Unlock()
	if len(s.rxBuf) == 0 {
		return nil
	}
	out := make([]byte, len(s.rxBuf))
	copy(out, s.rxBuf)
	s.rxBuf = s.rxBuf[:0]
	return out
}

// RxMu returns the mutex that guards the RX buffer. Used by callers that need
// to hold the lock across multiple operations (e.g. cond-wait loops).
func (s *Session) RxMu() *sync.Mutex { return &s.rxMu }

// RxCond returns the condition variable for the RX buffer.
func (s *Session) RxCond() *sync.Cond { return s.rxCond }

// RxBuf returns the current RX buffer slice (caller must hold RxMu).
func (s *Session) RxBuf() []byte { return s.rxBuf }

// DrainRxBuf drains and returns all RX buffer contents (caller must hold RxMu).
func (s *Session) DrainRxBuf() []byte {
	if len(s.rxBuf) == 0 {
		return nil
	}
	out := make([]byte, len(s.rxBuf))
	copy(out, s.rxBuf)
	s.rxBuf = s.rxBuf[:0]
	return out
}

// Write sends data to the remote TCP connection and updates BytesTX.
func (s *Session) Write(data []byte) (int, error) {
	n, err := s.Conn.Write(data)
	s.BytesTX.Add(int64(n))
	return n, err
}

// WaitDead blocks until the session transitions to StateDead.
// It uses the rxCond broadcast that StartRxLoop sends when the connection drops.
func (s *Session) WaitDead() {
	s.rxMu.Lock()
	defer s.rxMu.Unlock()
	for s.State() != StateDead {
		s.rxCond.Wait()
	}
}

// Close terminates the connection with a clean FIN (half-close then full close).
func (s *Session) Close() {
	if tc, ok := s.Conn.(*net.TCPConn); ok {
		_ = tc.CloseWrite()
	}
	_ = s.Conn.Close()
	s.SetState(StateDead)
}

// ForceClose terminates the connection with RST by setting linger to 0.
func (s *Session) ForceClose() {
	if tc, ok := s.Conn.(*net.TCPConn); ok {
		_ = tc.SetLinger(0)
	}
	_ = s.Conn.Close()
	s.SetState(StateDead)
}
