package forward

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Entry holds either a *Forwarder or a *Proxy for a local port.
type Entry struct {
	Rule  Rule
	fwd   *Forwarder
	proxy *Proxy
}

// IsProxy reports whether this entry is a proxy (vs transparent forward).
func (e *Entry) IsProxy() bool { return e.Rule.IsProxy }

// Close stops the underlying forwarder or proxy.
func (e *Entry) Close() {
	if e.fwd != nil {
		e.fwd.Close()
	}
	if e.proxy != nil {
		e.proxy.Close()
	}
}

// Stats returns (tx, rx) bytes for forwarders; (0,0) for proxies (proxy logs inline).
func (e *Entry) Stats() (tx, rx int64) {
	if e.fwd != nil {
		return e.fwd.Stats()
	}
	return 0, 0
}

// SetLogFile changes the log destination for a proxy entry.
func (e *Entry) SetLogFile(path string) error {
	if e.proxy == nil {
		return fmt.Errorf("not a proxy")
	}
	return e.proxy.SetLogFile(path)
}

// Manager owns all active forwarders and proxies.
type Manager struct {
	mu      sync.RWMutex
	entries map[int]*Entry // keyed by local port
	timeout time.Duration
}

// NewManager creates a forward Manager with the given dial timeout (seconds).
func NewManager(dialTimeoutSecs int) *Manager {
	return &Manager{
		entries: make(map[int]*Entry),
		timeout: time.Duration(dialTimeoutSecs) * time.Second,
	}
}

// Open starts a transparent TCP forwarder on localPort → remoteHost:remotePort.
func (m *Manager) Open(ctx context.Context, localPort int, remoteHost string, remotePort int) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.entries[localPort]; exists {
		return fmt.Errorf("port %d already in use by a forward/proxy", localPort)
	}

	rule := Rule{LocalPort: localPort, RemoteHost: remoteHost, RemotePort: remotePort, IsProxy: false}
	f, err := startForwarder(ctx, rule, m.timeout)
	if err != nil {
		return err
	}
	m.entries[localPort] = &Entry{Rule: rule, fwd: f}
	return nil
}

// OpenProxy starts a logging proxy on localPort → remoteHost:remotePort.
func (m *Manager) OpenProxy(ctx context.Context, localPort int, remoteHost string, remotePort int, logFile string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.entries[localPort]; exists {
		return fmt.Errorf("port %d already in use by a forward/proxy", localPort)
	}

	rule := Rule{LocalPort: localPort, RemoteHost: remoteHost, RemotePort: remotePort, IsProxy: true, LogFile: logFile}
	p, err := startProxy(ctx, rule, m.timeout)
	if err != nil {
		return err
	}
	m.entries[localPort] = &Entry{Rule: rule, proxy: p}
	return nil
}

// Close stops a forwarder or proxy by local port.
func (m *Manager) Close(localPort int) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	e, ok := m.entries[localPort]
	if !ok {
		return fmt.Errorf("no forward/proxy on port %d", localPort)
	}
	e.Close()
	delete(m.entries, localPort)
	return nil
}

// Get returns the Entry for a local port, or nil.
func (m *Manager) Get(localPort int) *Entry {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.entries[localPort]
}

// All returns a snapshot of all entries (both fwd and proxy).
func (m *Manager) All() []*Entry {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]*Entry, 0, len(m.entries))
	for _, e := range m.entries {
		result = append(result, e)
	}
	return result
}

// Forwards returns entries that are transparent forwarders only.
func (m *Manager) Forwards() []*Entry {
	all := m.All()
	var result []*Entry
	for _, e := range all {
		if !e.IsProxy() {
			result = append(result, e)
		}
	}
	return result
}

// Proxies returns entries that are proxies only.
func (m *Manager) Proxies() []*Entry {
	all := m.All()
	var result []*Entry
	for _, e := range all {
		if e.IsProxy() {
			result = append(result, e)
		}
	}
	return result
}

// CloseAll stops every forwarder and proxy.
func (m *Manager) CloseAll() {
	m.mu.Lock()
	ports := make([]int, 0, len(m.entries))
	for p := range m.entries {
		ports = append(ports, p)
	}
	m.mu.Unlock()
	for _, p := range ports {
		_ = m.Close(p)
	}
}
