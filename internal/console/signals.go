package console

import (
	"os"
	"os/signal"
	"syscall"
)

// SignalHandler intercepts SIGINT and SIGTERM, preventing them from killing
// the process directly. Instead they send a value on the returned channel so
// the REPL can decide what to do.
type SignalHandler struct {
	ch chan os.Signal
}

// NewSignalHandler installs custom handlers for SIGINT and SIGTERM.
func NewSignalHandler() *SignalHandler {
	ch := make(chan os.Signal, 4)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	return &SignalHandler{ch: ch}
}

// Chan returns the channel on which intercepted signals are delivered.
func (h *SignalHandler) Chan() <-chan os.Signal {
	return h.ch
}

// Stop unregisters the signal handler.
func (h *SignalHandler) Stop() {
	signal.Stop(h.ch)
}
