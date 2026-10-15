package common

import "sidekick/internal/cachehome"

// GetSidekickCacheHome returns a directory path for storing user-specific
// sidekick cache data. If needed, it also creates the necessary directories for
// storing user-specific cache data according to the XDG spec. Can be overridden by
// setting the SIDE_CACHE_HOME environment variable.
func GetSidekickCacheHome() (string, error) {
	return cachehome.Get()
}
