package dev

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeFile creates parent dirs as needed and writes contents to path. It
// returns the absolute path written to so callers can re-use it for asserts.
func writeFile(t *testing.T, path, contents string) string {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(contents), 0o644))
	return path
}

func TestIddWatchEditIdleActivity_ReturnsAfterIdleOnIntentEdit(t *testing.T) {
	t.Parallel()
	worktree := t.TempDir()
	intentDir := filepath.Join(worktree, "intent")
	require.NoError(t, os.MkdirAll(intentDir, 0o755))

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	type result struct {
		out IddWatchEditIdleResult
		err error
	}
	resCh := make(chan result, 1)
	go func() {
		out, err := IddWatchEditIdleActivity(ctx, IddWatchEditIdleInput{
			WorktreeDir:  worktree,
			WatchSubdir:  "intent",
			IdleDuration: 150 * time.Millisecond,
			MaxWait:      10 * time.Second,
		})
		resCh <- result{out, err}
	}()

	// Give the watcher a moment to install its inotify hooks before writing.
	time.Sleep(50 * time.Millisecond)
	// Both edits land within one burst window, so they should coalesce into
	// the same returned batch rather than splitting into two activity
	// returns. The watcher dedupes paths, so under heavy load (where hook
	// installation may lag past the initial sleep) the writes can simply be
	// retried until a settled batch is reported.
	writeBurst := func() {
		writeFile(t, filepath.Join(intentDir, "goals.md"), "# initial\n")
		time.Sleep(40 * time.Millisecond)
		writeFile(t, filepath.Join(intentDir, "notes.md"), "more\n")
	}
	writeBurst()

	retryTicker := time.NewTicker(500 * time.Millisecond)
	defer retryTicker.Stop()
	deadline := time.After(12 * time.Second)
	for {
		select {
		case r := <-resCh:
			require.NoError(t, r.err)
			assert.False(t, r.out.TimedOut)
			sort.Strings(r.out.ChangedPaths)
			assert.Equal(t, []string{"intent/goals.md", "intent/notes.md"}, r.out.ChangedPaths)
			return
		case <-retryTicker.C:
			writeBurst()
		case <-deadline:
			t.Fatal("watcher did not return after idle window elapsed")
		}
	}
}

func TestIddWatchEditIdleActivity_IgnoresEditsOutsideWatchSubdir(t *testing.T) {
	t.Parallel()
	worktree := t.TempDir()
	intentDir := filepath.Join(worktree, "intent")
	require.NoError(t, os.MkdirAll(intentDir, 0o755))
	otherDir := filepath.Join(worktree, "src")
	require.NoError(t, os.MkdirAll(otherDir, 0o755))

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	type result struct {
		out IddWatchEditIdleResult
		err error
	}
	resCh := make(chan result, 1)
	go func() {
		out, err := IddWatchEditIdleActivity(ctx, IddWatchEditIdleInput{
			WorktreeDir:  worktree,
			WatchSubdir:  "intent",
			IdleDuration: 150 * time.Millisecond,
			MaxWait:      1500 * time.Millisecond,
		})
		resCh <- result{out, err}
	}()

	time.Sleep(50 * time.Millisecond)
	// Edits outside the watched subdir must not start the idle timer or
	// appear in ChangedPaths; only the MaxWait timeout should trigger return.
	writeFile(t, filepath.Join(otherDir, "main.md"), "code\n")
	writeFile(t, filepath.Join(worktree, "README.md"), "top level\n")

	select {
	case r := <-resCh:
		require.NoError(t, r.err)
		assert.True(t, r.out.TimedOut, "expected MaxWait timeout when no intent edits occurred")
		assert.Empty(t, r.out.ChangedPaths)
	case <-time.After(3 * time.Second):
		t.Fatal("watcher did not return on MaxWait timeout")
	}
}

func TestIddWatchEditIdleActivity_IgnoresUnwatchedExtensions(t *testing.T) {
	t.Parallel()
	worktree := t.TempDir()
	intentDir := filepath.Join(worktree, "intent")
	require.NoError(t, os.MkdirAll(intentDir, 0o755))

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	type result struct {
		out IddWatchEditIdleResult
		err error
	}
	resCh := make(chan result, 1)
	go func() {
		out, err := IddWatchEditIdleActivity(ctx, IddWatchEditIdleInput{
			WorktreeDir:  worktree,
			WatchSubdir:  "intent",
			IdleDuration: 150 * time.Millisecond,
			MaxWait:      1500 * time.Millisecond,
		})
		resCh <- result{out, err}
	}()

	time.Sleep(50 * time.Millisecond)
	// Editor swap and binary-ish files under intent/ should be filtered
	// out so they neither populate ChangedPaths nor reset the idle window.
	writeFile(t, filepath.Join(intentDir, ".goals.md.swp"), "swap\n")
	writeFile(t, filepath.Join(intentDir, "image.png"), "binary\n")

	select {
	case r := <-resCh:
		require.NoError(t, r.err)
		assert.True(t, r.out.TimedOut)
		assert.Empty(t, r.out.ChangedPaths)
	case <-time.After(3 * time.Second):
		t.Fatal("watcher did not return on MaxWait timeout")
	}
}

func TestIsUnder(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		rel  string
		dir  string
		want bool
	}{
		{"exact match", "intent", "intent", true},
		{"nested file", "intent/sub/goals.md", "intent", true},
		{"empty dir matches all", "anything/here", "", true},
		{"sibling dir not under", "intentions/goals.md", "intent", false},
		{"different root", "src/main.go", "intent", false},
		{"trailing slash on dir is normalized", "intent/x.md", "intent/", true},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, isUnder(tc.rel, tc.dir))
		})
	}
}

func TestShouldIgnorePath(t *testing.T) {
	t.Parallel()
	cases := []struct {
		rel  string
		want bool
	}{
		{"intent/goals.md", false},
		{"", true},
		{".git", true},
		{".git/HEAD", true},
		{"intent/.DS_Store", true},
		{"intent/goals.md~", true},
		{"intent/sub/notes.md", false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.rel, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, shouldIgnorePath(tc.rel))
		})
	}
}

// saveViaRename mimics how editors and IDEs persist a file: write a temp file
// outside the target directory, then rename it into place.
func saveViaRename(t *testing.T, dest, contents string) {
	t.Helper()
	tmp := writeFile(t, filepath.Join(t.TempDir(), "editor-save-tmp"), contents)
	require.NoError(t, os.Rename(tmp, dest))
}

// TestIddWatchEditIdleActivity_DetectsNestedDirectIdeEdits covers direct edits
// made outside the canvas: an IDE saving intent via rename into a nested
// intent subdirectory of the server-local IDD worktree.
func TestIddWatchEditIdleActivity_DetectsNestedDirectIdeEdits(t *testing.T) {
	t.Parallel()
	worktree := t.TempDir()
	nestedDir := filepath.Join(worktree, "intent", "idd")
	require.NoError(t, os.MkdirAll(nestedDir, 0o755))

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	type result struct {
		out IddWatchEditIdleResult
		err error
	}
	resCh := make(chan result, 1)
	go func() {
		out, err := IddWatchEditIdleActivity(ctx, IddWatchEditIdleInput{
			WorktreeDir:  worktree,
			WatchSubdir:  "intent",
			IdleDuration: 150 * time.Millisecond,
			MaxWait:      10 * time.Second,
		})
		resCh <- result{out, err}
	}()

	// Give the watcher a moment to install its inotify hooks before writing.
	time.Sleep(50 * time.Millisecond)
	save := func() {
		saveViaRename(t, filepath.Join(nestedDir, "idd.md"), "# idd intent\n")
	}
	save()

	// Hook installation can lag past the initial sleep on a loaded machine, so
	// the save is repeated until a settled batch is reported.
	retryTicker := time.NewTicker(500 * time.Millisecond)
	defer retryTicker.Stop()
	deadline := time.After(12 * time.Second)
	for {
		select {
		case r := <-resCh:
			require.NoError(t, r.err)
			assert.False(t, r.out.TimedOut)
			assert.Equal(t, []string{"intent/idd/idd.md"}, r.out.ChangedPaths)
			return
		case <-retryTicker.C:
			save()
		case <-deadline:
			t.Fatal("watcher did not report the nested intent edit")
		}
	}
}

// TestIddWatchEditIdleActivity_DetectsEditsInDirectoryCreatedAfterStart covers
// a new intent subdirectory appearing while the watcher runs: its contents must
// be watched too, otherwise whole new intent areas would go unnoticed.
func TestIddWatchEditIdleActivity_DetectsEditsInDirectoryCreatedAfterStart(t *testing.T) {
	t.Parallel()
	worktree := t.TempDir()
	intentDir := filepath.Join(worktree, "intent")
	require.NoError(t, os.MkdirAll(intentDir, 0o755))

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	type result struct {
		out IddWatchEditIdleResult
		err error
	}
	resCh := make(chan result, 1)
	go func() {
		out, err := IddWatchEditIdleActivity(ctx, IddWatchEditIdleInput{
			WorktreeDir:  worktree,
			WatchSubdir:  "intent",
			IdleDuration: 150 * time.Millisecond,
			MaxWait:      10 * time.Second,
		})
		resCh <- result{out, err}
	}()

	time.Sleep(50 * time.Millisecond)
	newSubtree := filepath.Join(intentDir, "subsystems")
	// Both the directory registration and the write may need retrying: the
	// watcher only observes writes once it has hooked the new directory.
	createAndSave := func() {
		require.NoError(t, os.MkdirAll(newSubtree, 0o755))
		time.Sleep(100 * time.Millisecond)
		writeFile(t, filepath.Join(newSubtree, "api.md"), "# api intent\n")
	}
	createAndSave()

	retryTicker := time.NewTicker(500 * time.Millisecond)
	defer retryTicker.Stop()
	deadline := time.After(12 * time.Second)
	for {
		select {
		case r := <-resCh:
			require.NoError(t, r.err)
			assert.False(t, r.out.TimedOut)
			assert.Equal(t, []string{"intent/subsystems/api.md"}, r.out.ChangedPaths)
			return
		case <-retryTicker.C:
			createAndSave()
		case <-deadline:
			t.Fatal("watcher did not report the edit in the newly created intent subdirectory")
		}
	}
}
