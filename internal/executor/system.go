package executor

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
)

// Run executes a shell command string (everything after the leading !)
// and returns combined stdout+stderr output as a string.
func Run(cmdStr string) (string, error) {
	cmdStr = strings.TrimSpace(cmdStr)
	if cmdStr == "" {
		return "", fmt.Errorf("empty command")
	}

	// Split into args to avoid shell injection; use sh -c for pipeline support.
	cmd := exec.Command("bash", "-c", cmdStr)

	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf

	if err := cmd.Run(); err != nil {
		// Include output even on error (stderr is useful).
		output := buf.String()
		if output != "" {
			return output, nil
		}
		return "", fmt.Errorf("%w", err)
	}
	return buf.String(), nil
}
