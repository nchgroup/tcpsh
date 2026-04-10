package session

// State represents the lifecycle state of a Session.
type State int

const (
	StateActive     State = iota // Connected, not in foreground
	StateForeground              // Currently attached to the REPL
	StateBackground              // Explicitly sent to background
	StateDead                    // Connection closed / error
)

func (s State) String() string {
	switch s {
	case StateActive:
		return "active"
	case StateForeground:
		return "foreground"
	case StateBackground:
		return "background"
	case StateDead:
		return "dead"
	default:
		return "unknown"
	}
}
