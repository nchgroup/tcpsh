package views

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/nchgroup/tcpsh/internal/forward"
	"github.com/nchgroup/tcpsh/internal/session"
	"github.com/nchgroup/tcpsh/pkg/ui"
)

// col pads/truncates s to exactly w visual columns, ANSI-aware.
func col(w int, s string) string {
	return lipgloss.NewStyle().Width(w).MaxWidth(w).Render(s)
}

// row builds a display row with a 2-space leading indent.
func row(cells ...string) string {
	return "  " + strings.Join(cells, "")
}

// PortRow holds display data for one listener port.
type PortRow struct {
	Port     int
	Host     string
	Sessions int
}

// Listeners column widths.
// PORT:  65535 = 5 chars  → 7
// HOST:  255.255.255.255  = 15 chars → 18
// CONNS: small int        → 12 (fits header "CONNECTIONS")
const (
	wLPort  = 7
	wLHost  = 18
	wLConns = 12
)

// RenderPorts renders a formatted table of open listener ports.
func RenderPorts(ports []PortRow) string {
	if len(ports) == 0 {
		return ui.StyleMuted.Render("  No ports open.") + "\n"
	}
	sort.Slice(ports, func(i, j int) bool { return ports[i].Port < ports[j].Port })

	var sb strings.Builder
	hdr := row(col(wLPort, "PORT"), col(wLHost, "HOST"), col(wLConns, "CONNECTIONS"))
	sb.WriteString(ui.StyleHeader.Render(hdr) + "\n")

	for _, r := range ports {
		host := r.Host
		if host == "" {
			host = "0.0.0.0"
		}
		sb.WriteString(row(
			col(wLPort, fmt.Sprintf("%d", r.Port)),
			col(wLHost, ui.StyleMuted.Render(host)),
			col(wLConns, fmt.Sprintf("%d", r.Sessions)),
		) + "\n")
	}
	return sb.String()
}

// Sessions column widths.
// ID:       small int           → 5
// PORT:     65535 = 5 chars     → 7
// REMOTE:   255.255.255.255:65535 = 21 chars → 23
// STATE:    "foreground" = 10   → 13
// TX/RX:    "1023.9M" = 7       → 9
// DURATION: "99h59m59s" = 9     → 11
// IDLE:     same format         → 11
const (
	wSID     = 5
	wSPort   = 7
	wSRemote = 23
	wSState  = 13
	wSTX     = 9
	wSRX     = 9
	wSDur    = 11
	wSIdle   = 11
)

// RenderSessions renders a formatted table of active sessions.
func RenderSessions(sessions []*session.Session) string {
	if len(sessions) == 0 {
		return ui.StyleMuted.Render("  No active connections.") + "\n"
	}
	sort.Slice(sessions, func(i, j int) bool { return sessions[i].ID < sessions[j].ID })

	var sb strings.Builder
	hdr := row(
		col(wSID, "ID"),
		col(wSPort, "PORT"),
		col(wSRemote, "REMOTE"),
		col(wSState, "STATE"),
		col(wSTX, "TX"),
		col(wSRX, "RX"),
		col(wSIdle, "IDLE"),
		col(wSDur, "DURATION"),
	)
	sb.WriteString(ui.StyleHeader.Render(hdr) + "\n")

	for _, s := range sessions {
		idle := s.IdleDuration()
		idleStyle := ui.StyleMuted
		if idle > 5*time.Minute {
			idleStyle = ui.StyleWarn
		}
		sb.WriteString(row(
			col(wSID, fmt.Sprintf("%d", s.ID)),
			col(wSPort, fmt.Sprintf("%d", s.Port)),
			col(wSRemote, s.RemoteAddr),
			col(wSState, stateColor(s.State()).Render(s.State().String())),
			col(wSTX, formatBytes(s.BytesTX.Load())),
			col(wSRX, formatBytes(s.BytesRX.Load())),
			col(wSIdle, idleStyle.Render(formatDuration(idle))),
			col(wSDur, formatDuration(s.Duration())),
		) + "\n")
	}
	return sb.String()
}

// Forwards column widths.
// LOCAL:  port 5 chars  → 7
// REMOTE: host:port     → 28
// TYPE:   "proxy" = 5   → 7
// TX/RX:  same as above → 9
const (
	wFLocal  = 7
	wFRemote = 28
	wFType   = 7
	wFTX     = 9
	wFRX     = 9
)

// RenderForwards renders a table of active TCP forwarders.
func RenderForwards(entries []*forward.Entry) string {
	if len(entries) == 0 {
		return ui.StyleMuted.Render("  No active forwards.") + "\n"
	}

	var sb strings.Builder
	hdr := row(
		col(wFLocal, "LOCAL"),
		col(wFRemote, "REMOTE"),
		col(wFType, "TYPE"),
		col(wFTX, "TX"),
		col(wFRX, "RX"),
	)
	sb.WriteString(ui.StyleHeader.Render(hdr) + "\n")

	for _, e := range entries {
		kind := ui.StyleForward.Render("fwd")
		if e.IsProxy() {
			kind = ui.StyleProxy.Render("proxy")
		}
		tx, rx := e.Stats()
		sb.WriteString(row(
			col(wFLocal, fmt.Sprintf("%d", e.Rule.LocalPort)),
			col(wFRemote, e.Rule.RemoteAddr()),
			col(wFType, kind),
			col(wFTX, formatBytes(tx)),
			col(wFRX, formatBytes(rx)),
		) + "\n")
	}
	return sb.String()
}

func stateColor(s session.State) lipgloss.Style {
	switch s {
	case session.StateActive:
		return ui.StyleActive
	case session.StateForeground:
		return ui.StyleForeground
	case session.StateBackground:
		return ui.StyleBackground
	default:
		return ui.StyleDead
	}
}

func formatBytes(n int64) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%dB", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.1fK", float64(n)/1024)
	default:
		return fmt.Sprintf("%.1fM", float64(n)/(1024*1024))
	}
}

func formatDuration(d time.Duration) string {
	d = d.Round(time.Second)
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	if h > 0 {
		return fmt.Sprintf("%dh%02dm%02ds", h, m, s)
	}
	if m > 0 {
		return fmt.Sprintf("%dm%02ds", m, s)
	}
	return fmt.Sprintf("%ds", s)
}
