package testfixture

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
)

// Snapshot supplies independent copies of a relocatable directory fixture.
// Construct a separate Snapshot for each fixture definition. The snapshot lives
// only within the process, so fixture code changes cannot leave stale disk caches.
type Snapshot struct {
	build   func(string) error
	once    sync.Once
	entries []entry
	err     error
}

func New(build func(string) error) *Snapshot {
	return &Snapshot{build: build}
}

// CopyTo populates an existing, empty directory. Fixture builders must not
// embed their destination's absolute path in files or symlink targets.
func (s *Snapshot) CopyTo(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	if len(entries) != 0 {
		return fmt.Errorf("fixture destination is not empty: %s", dir)
	}
	built := false
	s.once.Do(func() {
		built = true
		s.err = s.build(dir)
		if s.err == nil {
			s.entries, s.err = capture(dir)
		}
	})
	if s.err != nil {
		return s.err
	}
	if built {
		return nil
	}

	for _, e := range s.entries {
		path := filepath.Join(dir, e.path)
		switch {
		case e.mode.IsDir():
			err = os.Mkdir(path, e.mode.Perm()|0700)
		case e.mode&os.ModeSymlink != 0:
			err = os.Symlink(string(e.data), path)
		default:
			err = os.WriteFile(path, e.data, e.mode.Perm())
			if err == nil {
				err = os.Chmod(path, e.mode.Perm())
			}
		}
		if err != nil {
			return fmt.Errorf("copy fixture %s: %w", e.path, err)
		}
	}
	// Apply directory permissions after their children have been written.
	for i := len(s.entries) - 1; i >= 0; i-- {
		e := s.entries[i]
		if e.mode.IsDir() {
			if err := os.Chmod(filepath.Join(dir, e.path), e.mode.Perm()); err != nil {
				return err
			}
		}
	}
	return nil
}

type entry struct {
	path string
	mode fs.FileMode
	data []byte
}

func capture(root string) ([]entry, error) {
	var entries []entry
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == root {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		e := entry{path: rel, mode: info.Mode()}
		switch {
		case e.mode.IsDir():
		case e.mode.IsRegular():
			e.data, err = os.ReadFile(path)
		case e.mode&os.ModeSymlink != 0:
			var target string
			target, err = os.Readlink(path)
			e.data = []byte(target)
		default:
			return fmt.Errorf("unsupported fixture file %s: %s", rel, e.mode)
		}
		if err != nil {
			return err
		}
		entries = append(entries, e)
		return nil
	})
	return entries, err
}
