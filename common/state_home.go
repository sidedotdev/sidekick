package common

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/adrg/xdg"
)

// GetSidekickStateHome returns a directory path for storing user-specific
// sidekick state data (logs, traces, etc). If needed, it also creates the
// necessary directories for storing state data according to the XDG spec.
// Can be overridden by setting the SIDE_STATE_HOME environment variable.
func GetSidekickStateHome() (string, error) {
	sidekickStateDir := os.Getenv("SIDE_STATE_HOME")
	if sidekickStateDir != "" {
		err := os.MkdirAll(sidekickStateDir, 0755)
		if err != nil {
			return "", fmt.Errorf("failed to create Sidekick state directory from SIDE_STATE_HOME: %w", err)
		}
		return sidekickStateDir, nil
	}

	sidekickStateDir = filepath.Join(xdg.StateHome, "sidekick")
	err := os.MkdirAll(sidekickStateDir, 0755)
	if err != nil {
		return "", fmt.Errorf("failed to create Sidekick state directory: %w", err)
	}
	return sidekickStateDir, nil
}
