package git

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"sidekick/coding/unix"
	"sidekick/common"
	"sidekick/env"

	"github.com/rs/zerolog/log"
)

// LockExistingSidekickWorktrees scans the Sidekick data home's
// worktrees/<workspaceId>/<dir> layout and locks each existing git worktree.
// Locking prevents "git worktree prune" (including via auto-gc) from deleting a
// worktree whose working tree isn't visible from the environment running prune
// (e.g. host vs devcontainer sharing the same .git). It is best-effort: entries
// that are already locked are left as-is, and broken or non-worktree
// directories are skipped with a log line rather than aborting the pass.
func LockExistingSidekickWorktrees(ctx context.Context) {
	dataHome, err := common.GetSidekickDataHome()
	if err != nil {
		log.Warn().Err(err).Msg("skipping worktree locking: failed to get Sidekick data home")
		return
	}
	lockWorktreesUnder(ctx, filepath.Join(dataHome, "worktrees"))
}

// lockWorktreesUnder locks every worktree found in the <workspaceId>/<dir>
// layout beneath worktreesRoot. It is split out from
// LockExistingSidekickWorktrees so the scanning logic can be exercised against
// an arbitrary root without mutating the ambient data home.
func lockWorktreesUnder(ctx context.Context, worktreesRoot string) {
	workspaceEntries, err := os.ReadDir(worktreesRoot)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Warn().Err(err).Str("path", worktreesRoot).Msg("skipping worktree locking: failed to read worktrees directory")
		}
		return
	}

	for _, workspaceEntry := range workspaceEntries {
		if !workspaceEntry.IsDir() {
			continue
		}
		workspaceDir := filepath.Join(worktreesRoot, workspaceEntry.Name())
		worktreeEntries, err := os.ReadDir(workspaceDir)
		if err != nil {
			log.Warn().Err(err).Str("path", workspaceDir).Msg("failed to read workspace worktrees directory during worktree locking")
			continue
		}
		var worktreePaths []string
		for _, worktreeEntry := range worktreeEntries {
			if !worktreeEntry.IsDir() {
				continue
			}
			worktreePaths = append(worktreePaths, filepath.Join(workspaceDir, worktreeEntry.Name()))
		}
		lockWorkspaceWorktrees(ctx, worktreePaths)
	}
}

// lockWorkspaceWorktrees locks every worktree in worktreePaths, which are
// expected to share a single repo (a Sidekick workspace maps to one repo). It
// runs "git worktree list --porcelain" once to learn which worktrees are
// already locked, so a repeat startup pass spawns no per-worktree git process
// for the common case. Worktrees whose locked state can't be batch-determined
// (e.g. a broken entry, or one belonging to a different repo) fall back to the
// idempotent per-worktree lock, which tolerates "already locked".
func lockWorkspaceWorktrees(ctx context.Context, worktreePaths []string) {
	locked := batchLockedWorktrees(ctx, worktreePaths)
	for _, worktreePath := range worktreePaths {
		if locked[resolveWorktreePath(worktreePath)] {
			continue
		}
		lockSidekickWorktree(ctx, worktreePath)
	}
}

// batchLockedWorktrees returns the set of symlink-resolved worktree paths that
// git reports as locked, discovered with a single "git worktree list
// --porcelain". It tries each candidate as the command's working directory
// until one succeeds, since all worktrees sharing a repo yield the same listing
// and some candidates may be broken. An empty map is returned if none succeed.
func batchLockedWorktrees(ctx context.Context, worktreePaths []string) map[string]bool {
	for _, worktreePath := range worktreePaths {
		result, err := unix.RunCommandActivity(ctx, unix.RunCommandActivityInput{
			WorkingDir: worktreePath,
			Command:    "git",
			Args:       []string{"worktree", "list", "--porcelain"},
		})
		if err != nil || result.ExitStatus != 0 {
			continue
		}
		return parseLockedWorktrees(result.Stdout)
	}
	return map[string]bool{}
}

// parseLockedWorktrees extracts the symlink-resolved working-tree paths marked
// "locked" in "git worktree list --porcelain" output. Entries are separated by
// blank lines, each starting with a "worktree <path>" line.
func parseLockedWorktrees(porcelain string) map[string]bool {
	locked := map[string]bool{}
	var currentPath string
	for _, line := range strings.Split(porcelain, "\n") {
		switch {
		case strings.HasPrefix(line, "worktree "):
			currentPath = strings.TrimPrefix(line, "worktree ")
		case line == "locked" || strings.HasPrefix(line, "locked "):
			if currentPath != "" {
				locked[resolveWorktreePath(currentPath)] = true
			}
		case line == "":
			currentPath = ""
		}
	}
	return locked
}

// resolveWorktreePath normalizes a worktree path for comparison, resolving
// symlinks when possible so paths reported by "git worktree list" (which git
// may emit symlink-resolved) match the data-home paths we scan.
func resolveWorktreePath(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	if abs, err := filepath.Abs(path); err == nil {
		return abs
	}
	return path
}

func lockSidekickWorktree(ctx context.Context, worktreePath string) {
	// Skip the git subprocess for worktrees already locked, keeping repeat
	// startup passes cheap (a couple of filesystem stats) even with many
	// worktrees. Any uncertainty falls back to the authoritative git command.
	if worktreeIsLocked(worktreePath) {
		return
	}

	result, err := unix.RunCommandActivity(ctx, unix.RunCommandActivityInput{
		WorkingDir: worktreePath,
		Command:    "git",
		Args:       []string{"worktree", "lock", "--reason", env.WorktreeLockReason, "."},
	})
	if err != nil {
		log.Warn().Err(err).Str("path", worktreePath).Msg("failed to run git worktree lock")
		return
	}
	if result.ExitStatus != 0 && !strings.Contains(result.Stderr, "already locked") {
		log.Info().Str("path", worktreePath).Str("stderr", result.Stderr).Msg("skipping broken or non-worktree directory during worktree locking")
	}
}

// worktreeIsLocked reports whether the linked worktree at worktreePath is
// already locked, using a filesystem check that avoids spawning git. Linked
// worktrees store a ".git" file pointing at their admin directory under the
// common repo's .git/worktrees/<name>, where git records a lock as a "locked"
// file. Returning false on any error (including an unreadable pointer) makes
// the caller fall back to "git worktree lock", which is authoritative.
func worktreeIsLocked(worktreePath string) bool {
	adminDir, err := worktreeAdminDir(worktreePath)
	if err != nil {
		return false
	}
	_, err = os.Stat(filepath.Join(adminDir, "locked"))
	return err == nil
}

// worktreeAdminDir resolves the admin directory of the linked worktree at
// worktreePath by parsing its ".git" pointer file ("gitdir: <path>").
func worktreeAdminDir(worktreePath string) (string, error) {
	data, err := os.ReadFile(filepath.Join(worktreePath, ".git"))
	if err != nil {
		return "", err
	}
	gitdir, ok := strings.CutPrefix(strings.TrimSpace(string(data)), "gitdir:")
	if !ok {
		return "", fmt.Errorf("unexpected .git file contents in %q", worktreePath)
	}
	return strings.TrimSpace(gitdir), nil
}
