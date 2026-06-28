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

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
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
			MaxWait:      4 * time.Second,
		})
		resCh <- result{out, err}
	}()

	// Give the watcher a moment to install its inotify hooks before writing.
	time.Sleep(50 * time.Millisecond)
	writeFile(t, filepath.Join(intentDir, "goals.md"), "# initial\n")
	// A second edit during the burst window should coalesce into the same
	// returned batch rather than splitting into two activity returns.
	time.Sleep(40 * time.Millisecond)
	writeFile(t, filepath.Join(intentDir, "notes.md"), "more\n")

	select {
	case r := <-resCh:
		require.NoError(t, r.err)
		assert.False(t, r.out.TimedOut)
		sort.Strings(r.out.ChangedPaths)
		assert.Equal(t, []string{"intent/goals.md", "intent/notes.md"}, r.out.ChangedPaths)
	case <-time.After(4 * time.Second):
		t.Fatal("watcher did not return after idle window elapsed")
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
