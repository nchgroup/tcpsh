package session

import (
	"net"
	"sync"
	"sync/atomic"
)

// Manager tracks all active sessions across all listener ports.
type Manager struct {
	mu       sync.RWMutex
	sessions map[int]*Session
	counter  atomic.Int32
}

// NewManager creates an empty Manager.
func NewManager() *Manager {
	return &Manager{
		sessions: make(map[int]*Session),
	}
}

// Accept creates a new Session from an incoming connection and registers it.
func (m *Manager) Accept(port int, conn net.Conn) *Session {
	id := int(m.counter.Add(1))
	s := newSession(id, port, conn)
	m.mu.Lock()
	m.sessions[id] = s
	m.mu.Unlock()
	return s
}

// Get returns a session by ID, or nil if not found.
func (m *Manager) Get(id int) *Session {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.sessions[id]
}

// ByPort returns all live sessions for a given listener port.
func (m *Manager) ByPort(port int) []*Session {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var result []*Session
	for _, s := range m.sessions {
		if s.Port == port && s.State() != StateDead {
			result = append(result, s)
		}
	}
	return result
}

// All returns a snapshot of all non-dead sessions.
func (m *Manager) All() []*Session {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]*Session, 0, len(m.sessions))
	for _, s := range m.sessions {
		if s.State() != StateDead {
			result = append(result, s)
		}
	}
	return result
}

// Remove deletes a session record by ID.
func (m *Manager) Remove(id int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sessions, id)
}

// CloseAll closes every session for the given port (used when closing a listener).
func (m *Manager) CloseAll(port int) {
	sessions := m.ByPort(port)
	for _, s := range sessions {
		s.Close()
	}
}
