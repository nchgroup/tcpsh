package ui

import (
	"fmt"

	"github.com/charmbracelet/lipgloss"
)

// Colors
var (
	colorActive     = lipgloss.Color("10")  // bright green
	colorBackground = lipgloss.Color("11")  // bright yellow
	colorDead       = lipgloss.Color("9")   // bright red
	colorForeground = lipgloss.Color("14")  // bright cyan
	colorForward    = lipgloss.Color("12")  // bright blue
	colorProxy      = lipgloss.Color("13")  // bright magenta
	colorMuted      = lipgloss.Color("8")   // dark gray
	colorBold       = lipgloss.Color("15")  // white
)

// Styles for labels / badges.
var (
	StyleActive     = lipgloss.NewStyle().Foreground(colorActive).Bold(true)
	StyleBackground = lipgloss.NewStyle().Foreground(colorBackground).Bold(true)
	StyleDead       = lipgloss.NewStyle().Foreground(colorDead)
	StyleForeground = lipgloss.NewStyle().Foreground(colorForeground).Bold(true)
	StyleForward    = lipgloss.NewStyle().Foreground(colorForward).Bold(true)
	StyleProxy      = lipgloss.NewStyle().Foreground(colorProxy).Bold(true)
	StyleMuted      = lipgloss.NewStyle().Foreground(colorMuted)
	StyleBold       = lipgloss.NewStyle().Foreground(colorBold).Bold(true)

	StyleHeader = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorBold).
			BorderStyle(lipgloss.NormalBorder()).
			BorderBottom(true)

	StylePrompt = lipgloss.NewStyle().Foreground(colorActive).Bold(true)
	StyleError  = lipgloss.NewStyle().Foreground(colorDead).Bold(true)
	StyleInfo   = lipgloss.NewStyle().Foreground(colorForeground)
	StyleWarn   = lipgloss.NewStyle().Foreground(colorBackground)
)

// Banner returns the ASCII art startup banner.
func Banner(version string) string {
	art := `
  _                 _
 | |_ ___ _ __  ___| |__
 | __/ __| '_ \/ __| '_ \
 | || (__| |_) \__ \ | | |
  \__\___| .__/|___/_| |_|
         |_|`

	tag := StyleMuted.Render("TCP connection manager  ") + StyleActive.Render("v"+version)
	return StyleBold.Render(art) + "\n" + tag + "\n"
}

// Infof formats an info-level inline notification.
func Infof(format string, args ...interface{}) string {
	return StyleInfo.Render("[+] ") + lipgloss.NewStyle().Render(fmt.Sprintf(format, args...))
}

// Errorf formats an error-level inline notification.
func Errorf(format string, args ...interface{}) string {
	return StyleError.Render("[!] ") + lipgloss.NewStyle().Render(fmt.Sprintf(format, args...))
}

// Warnf formats a warning-level inline notification.
func Warnf(format string, args ...interface{}) string {
	return StyleWarn.Render("[~] ") + lipgloss.NewStyle().Render(fmt.Sprintf(format, args...))
}
