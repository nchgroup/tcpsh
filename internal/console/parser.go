package console

import (
	"fmt"
	"strconv"
	"strings"
)

// CmdKind classifies a parsed command.
type CmdKind int

const (
	CmdTool        CmdKind = iota // internal tcpsh command
	CmdSystem                     // !<shell command>
	CmdSpecial                    // +exit / +back / +bg / +background
	CmdPassthrough                // raw line to send to TCP session
	CmdEmpty
)

// Cmd is the result of parsing one input line.
type Cmd struct {
	Kind CmdKind
	Verb string   // first token lowercased (for CmdTool / CmdSpecial)
	Args []string // remaining tokens
	Raw  string   // original trimmed line
}

// Parse takes a raw input line and returns a Cmd.
// sessionMode controls whether unknown input is treated as passthrough.
func Parse(line string, sessionMode bool) Cmd {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return Cmd{Kind: CmdEmpty, Raw: trimmed}
	}

	// System command: starts with !
	if strings.HasPrefix(trimmed, "!") {
		return Cmd{Kind: CmdSystem, Raw: trimmed, Args: []string{trimmed[1:]}}
	}

	// Special session commands: start with +
	if strings.HasPrefix(trimmed, "+") {
		verb := strings.ToLower(strings.Fields(trimmed)[0])
		return Cmd{Kind: CmdSpecial, Verb: verb, Raw: trimmed}
	}

	// Tokenize
	parts := strings.Fields(trimmed)
	verb := strings.ToLower(parts[0])
	args := parts[1:]

	// Known tool verbs
	toolVerbs := map[string]bool{
		"open": true, "close": true, "kill": true,
		"use": true, "list": true, "info": true, "log": true,
		"fwd": true, "proxy": true, "help": true, "exit": true,
		"clear": true, "send": true, "read": true,
	}
	if toolVerbs[verb] {
		return Cmd{Kind: CmdTool, Verb: verb, Args: args, Raw: trimmed}
	}

	// In session mode, unknown input is passthrough to TCP.
	if sessionMode {
		return Cmd{Kind: CmdPassthrough, Raw: trimmed}
	}

	// In menu mode, treat as unknown tool command (dispatcher will print error).
	return Cmd{Kind: CmdTool, Verb: verb, Args: args, Raw: trimmed}
}

// ParsePortIdx parses strings like "4444" or "4444:2" into (port, idx, err).
// idx is 0 when no index is specified (meaning "first/only connection").
func ParsePortIdx(s string) (port, idx int, err error) {
	parts := strings.SplitN(s, ":", 2)
	port, err = strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, fmt.Errorf("invalid port %q", parts[0])
	}
	if len(parts) == 2 {
		idx, err = strconv.Atoi(parts[1])
		if err != nil {
			return 0, 0, fmt.Errorf("invalid index %q", parts[1])
		}
	}
	return port, idx, nil
}

// ParseRemote parses "host:port" into (host, port, err).
func ParseRemote(s string) (host string, port int, err error) {
	idx := strings.LastIndex(s, ":")
	if idx < 0 {
		return "", 0, fmt.Errorf("expected host:port, got %q", s)
	}
	host = s[:idx]
	if host == "" {
		host = "127.0.0.1"
	}
	port, err = strconv.Atoi(s[idx+1:])
	if err != nil {
		return "", 0, fmt.Errorf("invalid port in %q", s)
	}
	return host, port, nil
}
