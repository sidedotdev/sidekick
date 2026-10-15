package cachehome

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/adrg/xdg"
)

// Get returns the Sidekick cache directory, creating it if necessary.
// SIDE_CACHE_HOME overrides the XDG location.
func Get() (string, error) {
	sidekickCacheDir := os.Getenv("SIDE_CACHE_HOME")
	if sidekickCacheDir != "" {
		err := os.MkdirAll(sidekickCacheDir, 0755)
		if err != nil {
			return "", fmt.Errorf("failed to create Sidekick cache directory from SIDE_CACHE_HOME: %w", err)
		}
		return sidekickCacheDir, nil
	}

	sidekickCacheDir = filepath.Join(xdg.CacheHome, "sidekick")
	err := os.MkdirAll(sidekickCacheDir, 0755)
	if err != nil {
		return "", fmt.Errorf("failed to create Sidekick cache directory: %w", err)
	}
	return sidekickCacheDir, nil
}
