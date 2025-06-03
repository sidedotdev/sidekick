package common

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/adrg/xdg"
)

// GetSidekickCacheHome returns a directory path for storing user-specific
// sidekick cache data. If needed, it also creates the necessary directories for
// storing user-specific cache data according to the XDG spec. Can be overridden by
// setting the SIDE_CACHE_HOME environment variable.
func GetSidekickCacheHome() (string, error) {
	sidekickCacheDir := os.Getenv("SIDE_CACHE_HOME")
	if sidekickCacheDir != "" {
		// If the override is set, ensure this specific directory exists.
		err := os.MkdirAll(sidekickCacheDir, 0755)
		if err != nil {
			return "", fmt.Errorf("failed to create Sidekick cache directory from SIDE_CACHE_HOME: %w", err)
		}
		return sidekickCacheDir, nil
	}

	// Default to XDG cache directory + /sidekick
	sidekickCacheDir = filepath.Join(xdg.CacheHome, "sidekick")
	err := os.MkdirAll(sidekickCacheDir, 0755)
	if err != nil {
		return "", fmt.Errorf("failed to create Sidekick cache directory: %w", err)
	}
	return sidekickCacheDir, nil
}
