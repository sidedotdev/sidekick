package common

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/denormal/go-gitignore"
)

// IgnoreFileType represents the type of ignore file with inherent precedence
type IgnoreFileType int

// IsIgnoreFile returns true if the given filename is an ignore file
func IsIgnoreFile(name string) bool {
	fmt.Printf("DEBUG: IsIgnoreFile checking %s against %s, %s, %s\n",
		name,
		GitIgnoreType.String(),
		IgnoreType.String(),
		SideIgnoreType.String())
	return name == GitIgnoreType.String() ||
		name == IgnoreType.String() ||
		name == SideIgnoreType.String()
}

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

// collectIgnoreFiles finds all ignore files from startDir up to and including gitRoot
func collectIgnoreFiles(startDir string, gitRoot string) ([]IgnoreFile, error) {
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
		if dir == gitRoot {
			break
		}

		// Move to parent directory
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}

	// Sort files by precedence: directory depth (deeper first), then type (higher type first)
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

	return files, nil
}

// NewIgnoreManager creates a new IgnoreManager for the given directory
func NewIgnoreManager(baseDirectory string) (*IgnoreManager, error) {
	gitRoot, err := findGitRoot(baseDirectory)
	if err != nil {
		// If not in a git repo, only collect ignore files from baseDirectory
		gitRoot = baseDirectory
	}

	files, err := collectIgnoreFiles(baseDirectory, gitRoot)
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
			fmt.Printf("DEBUG: IsIgnored path=%s isDir=%v file.Dir=%s file.Type=%v match.Ignore()=%v\n",
				path, isDir, file.Dir, file.Type, match.Ignore())
			return match.Ignore()
		}
	}
	fmt.Printf("DEBUG: IsIgnored path=%s isDir=%v no match found\n", path, isDir)
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

		// Skip ignore files
		if !entry.IsDir() {
			isIgnore := IsIgnoreFile(entry.Name())
			fmt.Printf("DEBUG: Checking file %s, isIgnore=%v\n", entry.Name(), isIgnore)
			if isIgnore {
				return nil
			}
		}

		// Check if path should be ignored
		if ignoreManager.IsIgnored(path, entry.IsDir()) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		return handleEntry(path, entry)
	})

	return err
}
