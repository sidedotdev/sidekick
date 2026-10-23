package repotools

import (
	"fmt"
	"path/filepath"

	"github.com/bmatcuk/doublestar/v4"
)

// FilterFilesByGlob matches both repository-relative paths and basenames.
func FilterFilesByGlob(files []string, globPattern string) ([]string, error) {
	var filteredFiles []string
	for _, file := range files {
		if file == "" {
			continue
		}
		matched, err := doublestar.PathMatch(globPattern, file)
		if err != nil {
			return nil, fmt.Errorf("invalid glob pattern %s: %v", globPattern, err)
		}
		if !matched {
			base := filepath.Base(file)
			matched, err = doublestar.PathMatch(globPattern, base)
			if err != nil {
				return nil, fmt.Errorf("invalid glob pattern %s (when matching basename %s): %v", globPattern, base, err)
			}
		}
		if matched {
			filteredFiles = append(filteredFiles, file)
		}
	}
	return filteredFiles, nil
}
