package forward

import (
	"context"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// PipeMetrics holds byte counters for a single piped connection.
type PipeMetrics struct {
	BytesTX atomic.Int64
	BytesRX atomic.Int64
}

// Rule describes a forwarding or proxy configuration.
type Rule struct {
	LocalPort  int
	RemoteHost string
	RemotePort int
	IsProxy    bool   // if true, traffic is also logged
	LogFile    string // path for proxy log output (empty = stdout)
}

// RemoteAddr returns the formatted remote address.
func (r *Rule) RemoteAddr() string {
	return fmt.Sprintf("%s:%d", r.RemoteHost, r.RemotePort)
}

// Forwarder accepts connections on a local port and pipes them to a remote TCP address.
type Forwarder struct {
	Rule        Rule
	ln          net.Listener
	cancel      context.CancelFunc
	mu          sync.Mutex
	pipes       []*PipeMetrics
	totalTX     atomic.Int64
	totalRX     atomic.Int64
	dialTimeout time.Duration
}

// startForwarder creates a Forwarder or Proxy (based on rule.IsProxy) and begins accepting.
func startForwarder(ctx context.Context, rule Rule, dialTimeout time.Duration) (*Forwarder, error) {
	addr := fmt.Sprintf(":%d", rule.LocalPort)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("forward listen :%d: %w", rule.LocalPort, err)
	}

	ctx, cancel := context.WithCancel(ctx)
	f := &Forwarder{
		Rule:        rule,
		ln:          ln,
		cancel:      cancel,
		dialTimeout: dialTimeout,
	}
	go f.acceptLoop(ctx)
	return f, nil
}

func (f *Forwarder) acceptLoop(ctx context.Context) {
	for {
		conn, err := f.ln.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return
			default:
				return
			}
		}
		go f.handle(ctx, conn)
	}
}

func (f *Forwarder) handle(ctx context.Context, local net.Conn) {
	defer local.Close()

	dialer := &net.Dialer{Timeout: f.dialTimeout}
	remote, err := dialer.DialContext(ctx, "tcp", f.Rule.RemoteAddr())
	if err != nil {
		fmt.Printf("[fwd :%d] dial failed: %v\n", f.Rule.LocalPort, err)
		return
	}
	defer remote.Close()

	pm := &PipeMetrics{}
	f.mu.Lock()
	f.pipes = append(f.pipes, pm)
	f.mu.Unlock()

	var once sync.Once
	done := make(chan struct{})
	closeAll := func() {
		once.Do(func() {
			_ = local.Close()
			_ = remote.Close()
			close(done)
		})
	}

	// local → remote
	go func() {
		n, _ := io.Copy(remote, local)
		pm.BytesTX.Add(n)
		f.totalTX.Add(n)
		closeAll()
	}()

	// remote → local
	go func() {
		n, _ := io.Copy(local, remote)
		pm.BytesRX.Add(n)
		f.totalRX.Add(n)
		closeAll()
	}()

	select {
	case <-done:
	case <-ctx.Done():
		closeAll()
	}
}

// Close stops the forwarder.
func (f *Forwarder) Close() {
	f.cancel()
	_ = f.ln.Close()
}

// Stats returns cumulative bytes transferred.
func (f *Forwarder) Stats() (tx, rx int64) {
	return f.totalTX.Load(), f.totalRX.Load()
}
