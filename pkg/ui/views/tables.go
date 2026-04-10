package views

import (
	"fmt"
	"sort"
	"strings"
	"tcpsh/internal/forward"
	"tcpsh/internal/session"
	"tcpsh/pkg/ui"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// PortRow holds display data for one listener port.
type PortRow struct {
	Port     int
	Host     string
	Sessions int
}

// RenderPorts renders a formatted table of open listener ports.
func RenderPorts(ports []PortRow) string {
	if len(ports) == 0 {
		return ui.StyleMuted.Render("  No ports open.") + "\n"
	}
	sort.Slice(ports, func(i, j int) bool { return ports[i].Port < ports[j].Port })

	var sb strings.Builder
	sb.WriteString(ui.StyleHeader.Render(fmt.Sprintf("  %-8s %-20s %-12s", "PORT", "HOST", "CONNECTIONS")) + "\n")
	for _, r := range ports {
		host := r.Host
		if host == "" {
			host = "0.0.0.0"
		}
		sb.WriteString(fmt.Sprintf("  %-8d %-20s %-12d\n",
			r.Port,
			ui.StyleMuted.Render(host),
			r.Sessions,
		))
	}
	return sb.String()
}

// RenderSessions renders a formatted table of active sessions.
func RenderSessions(sessions []*session.Session) string {
	if len(sessions) == 0 {
		return ui.StyleMuted.Render("  No active connections.") + "\n"
	}
	sort.Slice(sessions, func(i, j int) bool { return sessions[i].ID < sessions[j].ID })

	var sb strings.Builder
	sb.WriteString(ui.StyleHeader.Render(fmt.Sprintf("  %-6s %-8s %-26s %-12s %-12s %-10s %-12s",
		"ID", "PORT", "REMOTE", "STATE", "TX", "RX", "DURATION")) + "\n")

	for _, s := range sessions {
		stateStyle := stateColor(s.State())
		sb.WriteString(fmt.Sprintf("  %-6d %-8d %-26s %-12s %-12s %-10s %-12s\n",
			s.ID,
			s.Port,
			s.RemoteAddr,
			stateStyle.Render(s.State().String()),
			formatBytes(s.BytesTX.Load()),
			formatBytes(s.BytesRX.Load()),
			formatDuration(s.Duration()),
		))
	}
	return sb.String()
}

// RenderForwards renders a table of active TCP forwarders.
func RenderForwards(entries []*forward.Entry) string {
	if len(entries) == 0 {
		return ui.StyleMuted.Render("  No active forwards.") + "\n"
	}

	var sb strings.Builder
	sb.WriteString(ui.StyleHeader.Render(fmt.Sprintf("  %-8s %-30s %-8s %-12s %-12s",
		"LOCAL", "REMOTE", "TYPE", "TX", "RX")) + "\n")

	for _, e := range entries {
		kind := ui.StyleForward.Render("fwd")
		if e.IsProxy() {
			kind = ui.StyleProxy.Render("proxy")
		}
		tx, rx := e.Stats()
		sb.WriteString(fmt.Sprintf("  %-8d %-30s %-8s %-12s %-12s\n",
			e.Rule.LocalPort,
			e.Rule.RemoteAddr(),
			kind,
			formatBytes(tx),
			formatBytes(rx),
		))
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
