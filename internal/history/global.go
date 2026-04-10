package history

import (
	"os"
	"path/filepath"

	"github.com/chzyer/readline"
)

// Global manages the persistent global command history file.
type Global struct {
	path string
}

// NewGlobal creates a Global history backed by the given file path.
// The file and its parent directories are created if they don't exist.
func NewGlobal(path string) (*Global, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	// Touch the file if it doesn't exist.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return nil, err
	}
	_ = f.Close()
	return &Global{path: path}, nil
}

// Path returns the history file path (used by readline.Config).
func (g *Global) Path() string {
	return g.path
}

// ReadlineHistory returns a readline-compatible FileAutoCompleteCallback instance.
// Pass g.Path() directly to readline.Config.HistoryFile.
func (g *Global) ReadlineFileHistory() readline.AutoCompleter {
	// readline reads the history file itself via Config.HistoryFile.
	// This is a no-op helper kept for API symmetry.
	return nil
}
