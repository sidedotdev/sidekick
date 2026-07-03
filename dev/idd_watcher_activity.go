package dev

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
	"go.temporal.io/sdk/activity"
)

// IddWatchEditIdleInput configures the worktree-edit watcher activity.
type IddWatchEditIdleInput struct {
	// WorktreeDir is the absolute path to the IDD worktree to watch.
	WorktreeDir string
	// WatchSubdir is the worktree-relative directory to watch for intent
	// edits (e.g. "intent"). When set, only edits under this subtree count
	// toward the idle/burst window; files elsewhere in the worktree are
	// ignored. An empty value falls back to watching the whole worktree.
	WatchSubdir string
	// IdleDuration is how long the filesystem must stay quiet after at least
	// one write/create/rename event before the activity returns.
	IdleDuration time.Duration
	// MaxWait caps the total time the activity will wait for an idle window;
	// on expiry it returns whatever changes (if any) have been seen so far.
	// Zero disables the cap.
	MaxWait time.Duration
}

// IddWatchEditIdleResult reports a settled batch of intent edits.
type IddWatchEditIdleResult struct {
	// ChangedPaths are worktree-relative paths observed during the burst,
	// deduplicated in observation order.
	ChangedPaths []string
	// TimedOut is true when the activity returned because MaxWait elapsed
	// with no edit activity at all, rather than because edits settled.
	TimedOut bool
}

// iddWatchedExtensions limits the watcher to files the user can author as
// intent. Anything else under the worktree (build artifacts, lockfiles,
// editor swap files) is ignored to avoid spurious orchestrator turns.
var iddWatchedExtensions = map[string]struct{}{
	".md":   {},
	".mdx":  {},
	".txt":  {},
	".yaml": {},
	".yml":  {},
}

// IddWatchEditIdleActivity blocks until the IDD worktree has been quiet for
// IdleDuration after observing at least one intent-file edit, or until
// MaxWait elapses. It heartbeats periodically so Temporal can detect a
// stuck or cancelled worker.
//
// The activity is long-running and is meant to be the single fsnotify
// consumer for one IDD workflow at a time; the workflow restarts it after
// each settled burst.
func IddWatchEditIdleActivity(ctx context.Context, input IddWatchEditIdleInput) (IddWatchEditIdleResult, error) {
	if input.WorktreeDir == "" {
		return IddWatchEditIdleResult{}, errors.New("WorktreeDir is required")
	}
	if input.IdleDuration <= 0 {
		input.IdleDuration = 8 * time.Second
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return IddWatchEditIdleResult{}, fmt.Errorf("fsnotify.NewWatcher: %w", err)
	}
	defer watcher.Close()

	// Default to watching only the intent/ subtree so unrelated worktree
	// edits (e.g. editor scratch files in the repo root, or code drops)
	// don't trigger orchestrator turns. Callers may override via WatchSubdir.
	watchRoot := input.WorktreeDir
	if input.WatchSubdir != "" {
		watchRoot = filepath.Join(input.WorktreeDir, input.WatchSubdir)
	}
	// If the subtree doesn't exist yet (fresh IDD worktree before any
	// intent has been authored), watch the worktree itself so the very
	// first creation of the intent directory is observed; events outside
	// the subtree are filtered out below.
	if _, err := os.Stat(watchRoot); err != nil {
		if !os.IsNotExist(err) {
			return IddWatchEditIdleResult{}, fmt.Errorf("stat watch root: %w", err)
		}
		watchRoot = input.WorktreeDir
	}
	if err := addWatchersRecursive(watcher, watchRoot); err != nil {
		return IddWatchEditIdleResult{}, fmt.Errorf("addWatchersRecursive: %w", err)
	}

	heartbeatTicker := time.NewTicker(15 * time.Second)
	defer heartbeatTicker.Stop()

	var (
		idleTimer    *time.Timer
		idleC        <-chan time.Time
		changedOrder []string
		seen         = map[string]struct{}{}
	)

	var maxWaitC <-chan time.Time
	if input.MaxWait > 0 {
		maxWaitTimer := time.NewTimer(input.MaxWait)
		defer maxWaitTimer.Stop()
		maxWaitC = maxWaitTimer.C
	}

	resetIdle := func() {
		if idleTimer != nil {
			idleTimer.Stop()
		}
		idleTimer = time.NewTimer(input.IdleDuration)
		idleC = idleTimer.C
	}

	for {
		select {
		case <-ctx.Done():
			return IddWatchEditIdleResult{ChangedPaths: changedOrder}, ctx.Err()

		case <-heartbeatTicker.C:
			if activity.IsActivity(ctx) {
				activity.RecordHeartbeat(ctx, len(changedOrder))
			}

		case <-maxWaitC:
			return IddWatchEditIdleResult{
				ChangedPaths: changedOrder,
				TimedOut:     len(changedOrder) == 0,
			}, nil

		case <-idleC:
			return IddWatchEditIdleResult{ChangedPaths: changedOrder}, nil

		case ev, ok := <-watcher.Events:
			if !ok {
				return IddWatchEditIdleResult{ChangedPaths: changedOrder}, errors.New("watcher events channel closed")
			}
			if !isRelevantEvent(ev) {
				continue
			}
			rel, ok := relativeWorktreePath(input.WorktreeDir, ev.Name)
			if !ok {
				continue
			}
			if shouldIgnorePath(rel) {
				continue
			}
			if input.WatchSubdir != "" && !isUnder(rel, input.WatchSubdir) {
				continue
			}
			if ev.Op&fsnotify.Create != 0 {
				// Newly created directories inside the worktree must be added
				// to the watcher so their contents are observed too.
				if info, err := osStatNoFollow(ev.Name); err == nil && info.IsDir() {
					_ = addWatchersRecursive(watcher, ev.Name)
					continue
				}
			}
			if !hasWatchedExtension(rel) {
				continue
			}
			if _, dup := seen[rel]; !dup {
				seen[rel] = struct{}{}
				changedOrder = append(changedOrder, rel)
			}
			resetIdle()

		case err, ok := <-watcher.Errors:
			if !ok {
				return IddWatchEditIdleResult{ChangedPaths: changedOrder}, errors.New("watcher errors channel closed")
			}
			return IddWatchEditIdleResult{ChangedPaths: changedOrder}, fmt.Errorf("fsnotify error: %w", err)
		}
	}
}

func isRelevantEvent(ev fsnotify.Event) bool {
	return ev.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename|fsnotify.Remove) != 0
}

func relativeWorktreePath(root, abs string) (string, bool) {
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return "", false
	}
	rel = filepath.ToSlash(rel)
	if rel == "." || strings.HasPrefix(rel, "../") {
		return "", false
	}
	return rel, true
}

func shouldIgnorePath(rel string) bool {
	if rel == "" {
		return true
	}
	// Skip git internals and common editor/build noise.
	if strings.HasPrefix(rel, ".git/") || rel == ".git" {
		return true
	}
	base := filepath.Base(rel)
	if strings.HasPrefix(base, ".") && base != "." && base != ".." {
		// Hidden files (e.g. .swp, .DS_Store) are noise.
		return true
	}
	if strings.HasSuffix(base, "~") {
		return true
	}
	return false
}

func hasWatchedExtension(rel string) bool {
	ext := strings.ToLower(filepath.Ext(rel))
	_, ok := iddWatchedExtensions[ext]
	return ok
}

// isUnder reports whether the worktree-relative slash path rel sits inside
// the worktree-relative directory dir (or equals dir itself). Both
// arguments must use forward slashes.
func isUnder(rel, dir string) bool {
	dir = strings.Trim(filepath.ToSlash(dir), "/")
	if dir == "" {
		return true
	}
	return rel == dir || strings.HasPrefix(rel, dir+"/")
}

// addWatchersRecursive walks root and registers every directory with the
// watcher. fsnotify on most platforms watches a directory non-recursively,
// so subdirectories must be added individually.
func addWatchersRecursive(watcher *fsnotify.Watcher, root string) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		if filepath.Base(path) == ".git" {
			return filepath.SkipDir
		}
		return watcher.Add(path)
	})
}

// osStatNoFollow stats the path without following symlinks; the result is
// only used to distinguish "newly created directory" from "newly created
// file" when expanding the watch set on the fly.
func osStatNoFollow(path string) (os.FileInfo, error) {
	return os.Lstat(path)
}
