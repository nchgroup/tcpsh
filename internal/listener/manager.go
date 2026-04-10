package listener

import (
	"context"
	"fmt"
	"sync"
	"github.com/nchgroup/tcpsh/internal/session"
)

// Manager owns all active TCP listeners.
type Manager struct {
	mu        sync.RWMutex
	listeners map[int]*Listener // keyed by port
	sessions  *session.Manager
	events    chan Event
}

// NewManager creates a Manager. Call Events() to receive listener events.
func NewManager(sessions *session.Manager) *Manager {
	return &Manager{
		listeners: make(map[int]*Listener),
		sessions:  sessions,
		events:    make(chan Event, 64),
	}
}

// Events returns the channel on which this manager emits Events.
func (m *Manager) Events() <-chan Event {
	return m.events
}

// Open starts a TCP listener on the given port and optional host (empty = all interfaces).
func (m *Manager) Open(ctx context.Context, port int, host string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.listeners[port]; exists {
		return fmt.Errorf("port %d is already open", port)
	}

	l, err := Start(ctx, port, host, m.sessions, m.events)
	if err != nil {
		return err
	}
	m.listeners[port] = l
	return nil
}

// Close stops a listener and terminates all its sessions.
func (m *Manager) Close(port int) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	l, ok := m.listeners[port]
	if !ok {
		return fmt.Errorf("port %d is not open", port)
	}

	m.sessions.CloseAll(port)
	l.Close()
	delete(m.listeners, port)
	return nil
}

// IsOpen reports whether a port has an active listener.
func (m *Manager) IsOpen(port int) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.listeners[port]
	return ok
}

// OpenPorts returns a slice of currently open port numbers.
func (m *Manager) OpenPorts() []int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ports := make([]int, 0, len(m.listeners))
	for p := range m.listeners {
		ports = append(ports, p)
	}
	return ports
}

// Get returns the Listener for a port, or nil if not open.
func (m *Manager) Get(port int) *Listener {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.listeners[port]
}

// CloseAll shuts down every listener.
func (m *Manager) CloseAll() {
	m.mu.Lock()
	ports := make([]int, 0, len(m.listeners))
	for p := range m.listeners {
		ports = append(ports, p)
	}
	m.mu.Unlock()
	for _, p := range ports {
		_ = m.Close(p)
	}
}
