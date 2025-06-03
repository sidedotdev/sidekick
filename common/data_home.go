package common

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/adrg/xdg"
)

// GetSidekickDataHome returns a directory path for storing user-specific
// sidekick data. If needed, it also creates the necessary directories for
// storing user-specific data according to the XDG spec. Can be overridden by
// setting the SIDE_DATA_HOME environment variable.
func GetSidekickDataHome() (string, error) {
	sidekickDataDir := os.Getenv("SIDE_DATA_HOME")
	if sidekickDataDir != "" {
		return sidekickDataDir, nil
	}

	sidekickDataDir = filepath.Join(xdg.DataHome, "sidekick")
	err := os.MkdirAll(sidekickDataDir, 0755)
	if err != nil {
		return "", fmt.Errorf("failed to create Sidekick data directory: %w", err)
	}
	return sidekickDataDir, nil
}

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

// GetReplayCacheFilePath constructs the full path for a cached workflow history file.
// It ensures the parent directory for the cache file exists.
// The path is <SIDEKICK_CACHE_HOME>/replays/<sidekickVersion>/<workflowId>_events.json.
func GetReplayCacheFilePath(sidekickVersion string, workflowId string) (string, error) {
	baseCacheDir, err := GetSidekickCacheHome()
	if err != nil {
		return "", fmt.Errorf("failed to get Sidekick cache home: %w", err)
	}

	replayFilePath := filepath.Join(baseCacheDir, "replays", sidekickVersion, fmt.Sprintf("%s_events.json", workflowId))

	replayFileDir := filepath.Dir(replayFilePath)
	err = os.MkdirAll(replayFileDir, 0755)
	if err != nil {
		return "", fmt.Errorf("failed to create replay cache directory '%s': %w", replayFileDir, err)
	}

	return replayFilePath, nil
}
