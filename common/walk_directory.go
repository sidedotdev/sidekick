package common

import (
	"io/fs"
	"os"
	"path/filepath"

	"github.com/denormal/go-gitignore"
)

func WalkCodeDirectory(baseDirectory string, handleEntry func(string, fs.DirEntry) error) error {
	// TODO allow for multipled/nested ignore files: find all
	// .gitignore/.sideignore files in the directory tree and use the right ones
	// depending on the file path
	gitIgnore, err := gitignore.NewRepository(baseDirectory)
	if err != nil {
		return err
	}

	var sideIgnore *gitignore.GitIgnore
	sideIgnoreFile := filepath.Join(baseDirectory, ".sideignore")
	if _, err := os.Stat(sideIgnoreFile); err == nil {
		tempIgnore, err := gitignore.NewRepositoryWithFile(baseDirectory, ".sideignore")
		if err != nil {
			return err
		}
		sideIgnore = &tempIgnore
	}

	// TODO validate that the basePath is a directory
	err = filepath.WalkDir(baseDirectory, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		// don't show the root directory explicitly
		// also note that the gitingore package fails if given the root directory :facepalm:
		if path == baseDirectory {
			return nil
		}

		// skip the .git directory
		if entry.IsDir() && entry.Name() == ".git" {
			return filepath.SkipDir
		}

		match := gitIgnore.Absolute(path, entry.IsDir())
		var match2 gitignore.Match
		if sideIgnore != nil {
			match2 = (*sideIgnore).Absolute(path, entry.IsDir())
		}

		if (match != nil && match.Ignore()) || (match2 != nil && match2.Ignore()) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		return handleEntry(path, entry)
	})

	return err
}
