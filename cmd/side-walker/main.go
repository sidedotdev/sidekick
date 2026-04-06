// side-walker is a standalone binary that walks a code directory respecting
// ignore files (e.g. .gitignore, .ignore, .sideignore) and prints entries to
// stdout using a line protocol: "f:<relative-path>" or "d:<relative-path>".
//
// Usage: side-walker <directory> [ignore-file-names...]
//
// If no ignore file names are provided, it defaults to
// .gitignore, .ignore, .sideignore.
//
// It is designed to be cross-compiled and uploaded to remote environments
// (devpod, openshell) where the full sidekick binary is not available.
package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/denormal/go-gitignore"
)

var defaultIgnoreFileNames = []string{".gitignore", ".ignore", ".sideignore"}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "usage: side-walker <directory> [ignore-file-names...]\n")
		os.Exit(1)
	}
	baseDir := os.Args[1]
	ignoreFileNames := os.Args[2:]
	if len(ignoreFileNames) == 0 {
		ignoreFileNames = defaultIgnoreFileNames
	}

	err := walkDirectory(baseDir, ignoreFileNames, func(path string, isDir bool) error {
		relPath, err := filepath.Rel(baseDir, path)
		if err != nil {
			return err
		}
		prefix := "f"
		if isDir {
			prefix = "d"
		}
		_, err = fmt.Printf("%s:%s\n", prefix, relPath)
		return err
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

// The walking logic below mirrors common.WalkDirectory but is kept
// self-contained so the binary has minimal dependencies and can be built
// with CGO_ENABLED=0.

type ignoreFile struct {
	precedence int
	fileName   string
	dir        string
	gitIgnore  gitignore.GitIgnore
}

type ignoreManager struct {
	files           []ignoreFile
	ignoreFileNames []string
}

func sortIgnoreFiles(files []ignoreFile) {
	sort.Slice(files, func(i, j int) bool {
		iDepth := len(strings.Split(files[i].dir, string(filepath.Separator)))
		jDepth := len(strings.Split(files[j].dir, string(filepath.Separator)))
		if iDepth != jDepth {
			return iDepth > jDepth
		}
		return files[i].precedence > files[j].precedence
	})
}

func collectAncestorIgnoreFiles(startDir string, ignoreFileNames []string) ([]ignoreFile, error) {
	var files []ignoreFile
	dir := startDir

	for {
		for i := len(ignoreFileNames) - 1; i >= 0; i-- {
			name := ignoreFileNames[i]
			p := filepath.Join(dir, name)
			if _, err := os.Stat(p); err == nil {
				gi, err := gitignore.NewRepositoryWithFile(dir, name)
				if err != nil {
					return nil, err
				}
				files = append(files, ignoreFile{precedence: i, fileName: name, dir: dir, gitIgnore: gi})
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

func (im *ignoreManager) addIgnoreFile(fileName string, precedence int, dir string) error {
	p := filepath.Join(dir, fileName)
	if _, err := os.Stat(p); err == nil {
		gi, err := gitignore.NewRepositoryWithFile(dir, fileName)
		if err != nil {
			return err
		}
		im.files = append(im.files, ignoreFile{precedence: precedence, fileName: fileName, dir: dir, gitIgnore: gi})
		sortIgnoreFiles(im.files)
	}
	return nil
}

func (im *ignoreManager) isIgnored(path string, isDir bool) bool {
	for _, f := range im.files {
		match := f.gitIgnore.Absolute(path, isDir)
		if match != nil {
			return match.Ignore()
		}
	}
	return false
}

func walkDirectory(baseDirectory string, ignoreFileNames []string, handleEntry func(string, bool) error) error {
	files, err := collectAncestorIgnoreFiles(baseDirectory, ignoreFileNames)
	if err != nil {
		return err
	}
	im := &ignoreManager{files: files, ignoreFileNames: ignoreFileNames}

	info, err := os.Stat(baseDirectory)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return errors.New("baseDirectory must be a directory")
	}

	return filepath.WalkDir(baseDirectory, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == baseDirectory {
			return nil
		}
		if entry.IsDir() && entry.Name() == ".git" {
			return filepath.SkipDir
		}
		if im.isIgnored(path, entry.IsDir()) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			for i, name := range im.ignoreFileNames {
				if err := im.addIgnoreFile(name, i, path); err != nil {
					return err
				}
			}
		}
		return handleEntry(path, entry.IsDir())
	})
}
