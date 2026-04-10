package history

import "sync"

// SessionHistory holds a per-session in-memory command log.
type SessionHistory struct {
	mu      sync.Mutex
	entries []string
}

// Add appends a command line to this session's history.
func (h *SessionHistory) Add(line string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.entries = append(h.entries, line)
}

// All returns a copy of all recorded commands.
func (h *SessionHistory) All() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	cp := make([]string, len(h.entries))
	copy(cp, h.entries)
	return cp
}

// Len returns the number of recorded commands.
func (h *SessionHistory) Len() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.entries)
}
