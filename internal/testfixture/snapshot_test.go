package testfixture

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSnapshotBuildsOnceAndIsolatesCopies(t *testing.T) {
	t.Parallel()

	var builds atomic.Int32
	snapshot := New(func(dir string) error {
		builds.Add(1)
		if err := os.MkdirAll(filepath.Join(dir, "empty"), 0750); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, "executable"), []byte("original"), 0755); err != nil {
			return err
		}
		return os.Symlink("executable", filepath.Join(dir, "link"))
	})

	const copies = 16
	dirs := make([]string, copies)
	for i := range dirs {
		dirs[i] = t.TempDir()
	}
	var wg sync.WaitGroup
	errs := make(chan error, copies)
	for _, dir := range dirs {
		wg.Go(func() {
			errs <- snapshot.CopyTo(dir)
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	require.EqualValues(t, 1, builds.Load())

	for _, dir := range dirs {
		content, err := os.ReadFile(filepath.Join(dir, "link"))
		require.NoError(t, err)
		require.Equal(t, "original", string(content))
		info, err := os.Stat(filepath.Join(dir, "executable"))
		require.NoError(t, err)
		require.Equal(t, os.FileMode(0755), info.Mode().Perm())
		info, err = os.Stat(filepath.Join(dir, "empty"))
		require.NoError(t, err)
		require.True(t, info.IsDir())
		require.Equal(t, os.FileMode(0750), info.Mode().Perm())
	}

	require.NoError(t, os.WriteFile(filepath.Join(dirs[0], "executable"), []byte("modified"), 0755))
	require.NoError(t, os.Remove(filepath.Join(dirs[0], "link")))
	require.NoError(t, os.RemoveAll(dirs[1]))
	for _, dir := range dirs[2:] {
		content, err := os.ReadFile(filepath.Join(dir, "link"))
		require.NoError(t, err)
		require.Equal(t, "original", string(content))
	}
	later := t.TempDir()
	require.NoError(t, snapshot.CopyTo(later))
	content, err := os.ReadFile(filepath.Join(later, "link"))
	require.NoError(t, err)
	require.Equal(t, "original", string(content))
	require.EqualValues(t, 1, builds.Load())
}

func TestSnapshotBuildFailureIsNotPublished(t *testing.T) {
	t.Parallel()

	buildErr := errors.New("fixture construction failed")
	var builds atomic.Int32
	snapshot := New(func(dir string) error {
		builds.Add(1)
		if err := os.WriteFile(filepath.Join(dir, "partial"), nil, 0644); err != nil {
			return err
		}
		return buildErr
	})
	for range 2 {
		require.ErrorIs(t, snapshot.CopyTo(t.TempDir()), buildErr)
	}
	require.EqualValues(t, 1, builds.Load())
}

func TestSnapshotDefinitionsAreIndependent(t *testing.T) {
	t.Parallel()

	for i := range 2 {
		snapshot := New(func(dir string) error {
			return os.WriteFile(filepath.Join(dir, "value"), []byte(fmt.Sprint(i)), 0644)
		})
		dir := t.TempDir()
		require.NoError(t, snapshot.CopyTo(dir))
		content, err := os.ReadFile(filepath.Join(dir, "value"))
		require.NoError(t, err)
		require.Equal(t, fmt.Sprint(i), string(content))
	}
}

func TestSnapshotRejectsNonemptyDestination(t *testing.T) {
	t.Parallel()

	var builds atomic.Int32
	snapshot := New(func(string) error {
		builds.Add(1)
		return nil
	})
	dir := t.TempDir()
	path := filepath.Join(dir, "existing")
	require.NoError(t, os.WriteFile(path, []byte("keep"), 0644))
	require.ErrorContains(t, snapshot.CopyTo(dir), "not empty")
	content, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "keep", string(content))
	require.Zero(t, builds.Load())
}
