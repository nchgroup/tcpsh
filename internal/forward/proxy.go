package forward

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"time"
)

// Proxy is like a Forwarder but additionally logs intercepted traffic to a file or stdout.
type Proxy struct {
	Rule        Rule
	ln          net.Listener
	cancel      context.CancelFunc
	dialTimeout time.Duration

	logMu  sync.Mutex
	logOut io.Writer
}

func startProxy(ctx context.Context, rule Rule, dialTimeout time.Duration) (*Proxy, error) {
	addr := fmt.Sprintf(":%d", rule.LocalPort)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("proxy listen :%d: %w", rule.LocalPort, err)
	}

	var out io.Writer = os.Stdout
	if rule.LogFile != "" {
		f, err := os.OpenFile(rule.LogFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
		if err != nil {
			_ = ln.Close()
			return nil, fmt.Errorf("proxy log file %s: %w", rule.LogFile, err)
		}
		out = f
	}

	ctx, cancel := context.WithCancel(ctx)
	p := &Proxy{
		Rule:        rule,
		ln:          ln,
		cancel:      cancel,
		dialTimeout: dialTimeout,
		logOut:      out,
	}
	go p.acceptLoop(ctx)
	return p, nil
}

func (p *Proxy) acceptLoop(ctx context.Context) {
	for {
		conn, err := p.ln.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return
			default:
				return
			}
		}
		go p.handle(ctx, conn)
	}
}

func (p *Proxy) handle(ctx context.Context, local net.Conn) {
	defer local.Close()

	dialer := &net.Dialer{Timeout: p.dialTimeout}
	remote, err := dialer.DialContext(ctx, "tcp", p.Rule.RemoteAddr())
	if err != nil {
		fmt.Printf("[proxy :%d] dial failed: %v\n", p.Rule.LocalPort, err)
		return
	}
	defer remote.Close()

	tag := fmt.Sprintf("[proxy :%d %s]", p.Rule.LocalPort, local.RemoteAddr())

	var once sync.Once
	done := make(chan struct{})
	closeAll := func() {
		once.Do(func() {
			_ = local.Close()
			_ = remote.Close()
			close(done)
		})
	}

	// local → remote (log as TX)
	go func() {
		tee := io.TeeReader(local, &logWriter{out: p.logOut, mu: &p.logMu, tag: tag + " TX"})
		_, _ = io.Copy(remote, tee)
		closeAll()
	}()

	// remote → local (log as RX)
	go func() {
		tee := io.TeeReader(remote, &logWriter{out: p.logOut, mu: &p.logMu, tag: tag + " RX"})
		_, _ = io.Copy(local, tee)
		closeAll()
	}()

	select {
	case <-done:
	case <-ctx.Done():
		closeAll()
	}
}

// SetLogFile redirects proxy logging to a file. Pass "" to reset to stdout.
func (p *Proxy) SetLogFile(path string) error {
	p.logMu.Lock()
	defer p.logMu.Unlock()
	if path == "" {
		p.logOut = os.Stdout
		return nil
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	p.logOut = f
	return nil
}

// Close stops the proxy.
func (p *Proxy) Close() {
	p.cancel()
	_ = p.ln.Close()
}

// logWriter is a thread-safe io.Writer that prefixes each write with a tag.
type logWriter struct {
	out io.Writer
	mu  *sync.Mutex
	tag string
}

func (lw *logWriter) Write(p []byte) (int, error) {
	lw.mu.Lock()
	defer lw.mu.Unlock()
	_, _ = fmt.Fprintf(lw.out, "%s %q\n", lw.tag, p)
	return len(p), nil
}
