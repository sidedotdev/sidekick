package common

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/denormal/go-gitignore"
)

// IgnoreFileType represents the type of ignore file with inherent precedence
type IgnoreFileType int

const (
	GitIgnoreType IgnoreFileType = iota
	IgnoreType
	SideIgnoreType
)

// String returns the filename for the ignore file type
func (t IgnoreFileType) String() string {
	switch t {
	case GitIgnoreType:
		return ".gitignore"
	case IgnoreType:
		return ".ignore"
	case SideIgnoreType:
		return ".sideignore"
	default:
		return ""
	}
}

// IgnoreFile represents a single ignore file with its type and parsed rules
type IgnoreFile struct {
	Type      IgnoreFileType
	Dir       string
	GitIgnore gitignore.GitIgnore
}

// IgnoreManager handles collection and evaluation of ignore files
type IgnoreManager struct {
	files []IgnoreFile
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

// sortIgnoreFiles sorts ignore files by precedence: directory depth (deeper first), then type (higher type first)
func sortIgnoreFiles(files []IgnoreFile) {
	sort.Slice(files, func(i, j int) bool {
		// Compare directory depths
		iDepth := len(strings.Split(files[i].Dir, string(filepath.Separator)))
		jDepth := len(strings.Split(files[j].Dir, string(filepath.Separator)))
		if iDepth != jDepth {
			return iDepth > jDepth
		}
		// If same depth, compare types
		return files[i].Type > files[j].Type
	})
}

// collectAncestorIgnoreFiles finds all ignore files from startDir up to and
// including gitRoot (or filesystem root if not in git repo)
func collectAncestorIgnoreFiles(startDir string) ([]IgnoreFile, error) {
	var files []IgnoreFile
	dir := startDir

	for {
		// Check each type of ignore file in the current directory
		for _, ignoreType := range []IgnoreFileType{SideIgnoreType, IgnoreType, GitIgnoreType} {
			ignoreFile := filepath.Join(dir, ignoreType.String())
			if _, err := os.Stat(ignoreFile); err == nil {
				gitIgnore, err := gitignore.NewRepositoryWithFile(dir, ignoreType.String())
				if err != nil {
					return nil, err
				}
				files = append(files, IgnoreFile{
					Type:      ignoreType,
					Dir:       dir,
					GitIgnore: gitIgnore,
				})
			}
		}

		// Stop if we've reached the git root
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			break
		}

		// Move to parent directory
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}

	sortIgnoreFiles(files)
	return files, nil
}

// AddIgnoreFile adds a new ignore file and maintains the precedence order
func (im *IgnoreManager) AddIgnoreFile(ignoreType IgnoreFileType, dir string) error {
	ignoreFile := filepath.Join(dir, ignoreType.String())
	if _, err := os.Stat(ignoreFile); err == nil {
		gitIgnore, err := gitignore.NewRepositoryWithFile(dir, ignoreType.String())
		if err != nil {
			return err
		}
		im.files = append(im.files, IgnoreFile{
			Type:      ignoreType,
			Dir:       dir,
			GitIgnore: gitIgnore,
		})
		sortIgnoreFiles(im.files)
	}
	return nil
}

// NewIgnoreManager creates a new IgnoreManager for the given directory
func NewIgnoreManager(baseDirectory string) (*IgnoreManager, error) {
	files, err := collectAncestorIgnoreFiles(baseDirectory)
	if err != nil {
		return nil, err
	}

	return &IgnoreManager{files: files}, nil
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

func WalkCodeDirectory(baseDirectory string, handleEntry func(string, fs.DirEntry) error) error {
	// Create ignore manager to handle all ignore files
	ignoreManager, err := NewIgnoreManager(baseDirectory)
	if err != nil {
		return err
	}

	// Validate that baseDirectory is a directory
	info, err := os.Stat(baseDirectory)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return errors.New("baseDirectory must be a directory")
	}

	err = filepath.WalkDir(baseDirectory, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// Don't show the root directory explicitly
		if path == baseDirectory {
			return nil
		}

		// Skip the .git directory
		if entry.IsDir() && entry.Name() == ".git" {
			return filepath.SkipDir
		}

		// Check if path should be ignored
		if ignoreManager.IsIgnored(path, entry.IsDir()) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		// Check for ignore files in current directory
		if entry.IsDir() {
			for _, ignoreType := range []IgnoreFileType{SideIgnoreType, IgnoreType, GitIgnoreType} {
				if err := ignoreManager.AddIgnoreFile(ignoreType, path); err != nil {
					return err
				}
			}
		}

		return handleEntry(path, entry)
	})

	return err
}
