package common

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/denormal/go-gitignore"
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
type IgnoreFile struct {
	Precedence int
	FileName   string
	Dir        string
	GitIgnore  gitignore.GitIgnore
}

// IgnoreManager handles collection and evaluation of ignore files
type IgnoreManager struct {
	files           []IgnoreFile
	ignoreFileNames []string
}

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

// sortIgnoreFiles sorts ignore files by precedence: directory depth (deeper
// first), then precedence value (higher first).
func sortIgnoreFiles(files []IgnoreFile) {
	sort.Slice(files, func(i, j int) bool {
		iDepth := len(strings.Split(files[i].Dir, string(filepath.Separator)))
		jDepth := len(strings.Split(files[j].Dir, string(filepath.Separator)))
		if iDepth != jDepth {
			return iDepth > jDepth
		}
		return files[i].Precedence > files[j].Precedence
	})
}

// collectAncestorIgnoreFiles finds all ignore files from startDir up to and
// including gitRoot (or filesystem root if not in git repo).
// ignoreFileNames is ordered by precedence (last element = highest).
func collectAncestorIgnoreFiles(startDir string, ignoreFileNames []string) ([]IgnoreFile, error) {
	var files []IgnoreFile
	dir := startDir

	for {
		// Check each ignore file in the current directory (reverse order so
		// highest-precedence files are visited first, matching the old behaviour).
		for i := len(ignoreFileNames) - 1; i >= 0; i-- {
			name := ignoreFileNames[i]
			ignoreFilePath := filepath.Join(dir, name)
			if _, err := os.Stat(ignoreFilePath); err == nil {
				gi, err := gitignore.NewRepositoryWithFile(dir, name)
				if err != nil {
					return nil, err
				}
				files = append(files, IgnoreFile{
					Precedence: i,
					FileName:   name,
					Dir:        dir,
					GitIgnore:  gi,
				})
			}
		}

		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			break
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}

	sortIgnoreFiles(files)
	return files, nil
}

// AddIgnoreFile adds a new ignore file and maintains the precedence order.
func (im *IgnoreManager) AddIgnoreFile(fileName string, precedence int, dir string) error {
	ignoreFilePath := filepath.Join(dir, fileName)
	if _, err := os.Stat(ignoreFilePath); err == nil {
		gi, err := gitignore.NewRepositoryWithFile(dir, fileName)
		if err != nil {
			return err
		}
		im.files = append(im.files, IgnoreFile{
			Precedence: precedence,
			FileName:   fileName,
			Dir:        dir,
			GitIgnore:  gi,
		})
		sortIgnoreFiles(im.files)
	}
	return nil
}

// NewIgnoreManager creates a new IgnoreManager for the given directory,
// using the provided ignore file names ordered by precedence.
func NewIgnoreManager(baseDirectory string, ignoreFileNames []string) (*IgnoreManager, error) {
	files, err := collectAncestorIgnoreFiles(baseDirectory, ignoreFileNames)
	if err != nil {
		return nil, err
	}

	return &IgnoreManager{files: files, ignoreFileNames: ignoreFileNames}, nil
}

// IsIgnored checks if a path should be ignored according to all ignore files
func (im *IgnoreManager) IsIgnored(path string, isDir bool) bool {
	for _, file := range im.files {
		match := file.GitIgnore.Absolute(path, isDir)
		if match != nil {
			return match.Ignore()
		}
	}
	return false
}
