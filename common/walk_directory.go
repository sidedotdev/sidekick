package common

import (
	"errors"
	"os"
	"path/filepath"

	"sidekick/env/localio"
)

// DefaultIgnoreFileNames is the base set of ignore file names used by Walk
// when no custom set is provided.
var DefaultIgnoreFileNames = []string{".gitignore", ".ignore"}

// SidekickIgnoreFileNames extends the defaults with .sideignore at the
// highest precedence position (last element).
var SidekickIgnoreFileNames = []string{".gitignore", ".ignore", ".sideignore"}

// IgnoreFile represents a single ignore file with its parsed rules.
// Precedence is determined by the index of the file name in the caller's
// ordered ignore-file-name list (higher = wins over lower).
type IgnoreFile = localio.IgnoreFile

// IgnoreManager handles collection and evaluation of ignore files
type IgnoreManager = localio.IgnoreManager

// findGitRoot finds the git repository root by looking for .git directory
func findGitRoot(startDir string) (string, error) {
	dir := startDir
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("not in a git repository")
		}
		dir = parent
	}
}

// NewIgnoreManager creates a new IgnoreManager for the given directory,
// using the provided ignore file names ordered by precedence.
func NewIgnoreManager(baseDirectory string, ignoreFileNames []string) (*IgnoreManager, error) {
	return localio.NewIgnoreManager(baseDirectory, ignoreFileNames)
}
