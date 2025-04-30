package git

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec" // Added for ListWorktreesActivity
	"path/filepath"
	"strings"
)

// GitWorktree holds information about a Git worktree.
type GitWorktree struct {
	Path   string // Absolute, symlink-resolved path to the worktree directory.
	Branch string // The branch checked out in the worktree. Empty if detached.
}

// runGitCommand executes a git command in the specified directory.
// It returns stdout, stderr, exit code, and any error encountered during execution.
// If the command runs but exits with a non-zero status, the error will be an *exec.ExitError,
// but stdout, stderr, and the exit code will still be populated.
func runGitCommand(ctx context.Context, repoDir string, args ...string) (stdout, stderr string, exitCode int, err error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	// Ensure the directory exists before trying to set cmd.Dir
	// This prevents exec from erroring out before git can report "not a git repository"
	// We still expect git commands to fail if repoDir is not a valid repository.
	if _, statErr := os.Stat(repoDir); statErr == nil {
		cmd.Dir = repoDir
	} else {
		// If the directory doesn't exist, return an error immediately.
		// Git commands would fail anyway, but this provides a clearer error sooner.
		return "", "", -1, fmt.Errorf("repository directory '%s' not found: %w", repoDir, statErr)
	}

	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	err = cmd.Run()
	stdout = stdoutBuf.String()
	stderr = stderrBuf.String()

	// Default exit code to -1 for errors where the process didn't start or exit normally.
	exitCode = -1
	if cmd.ProcessState != nil {
		exitCode = cmd.ProcessState.ExitCode()
	}

	if err != nil {
		// Check if it's an ExitError (command ran but exited non-zero)
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
			// Return a more informative error including stderr
			// We still return the captured stdout/stderr/exitCode along with the error.
			return stdout, stderr, exitCode, fmt.Errorf("git command %v failed in %s with exit code %d: %w\nstderr: %s", args, repoDir, exitCode, err, stderr)
		}
		// Other errors (e.g., command not found, context cancelled, directory not found)
		return stdout, stderr, exitCode, fmt.Errorf("failed to run git command %v in %s: %w", args, repoDir, err)
	}

	// Success (exit code 0)
	return stdout, stderr, 0, nil
}

// GetCurrentBranch determines the current branch name or detached HEAD state for a repository.
// It returns the branch name, a boolean indicating true if in detached HEAD state, and an error if encountered.
// An empty repository (initialized but without commits) will return the initial branch name (e.g., "main" or "master")
// and isDetached=false.
func GetCurrentBranch(ctx context.Context, repoDir string) (branchName string, isDetached bool, err error) {
	// `git symbolic-ref --short HEAD` returns the current branch name if on a branch.
	// It exits with 0 even in an empty repo after `git init -b <name>`.
	// It exits with 1 and prints "fatal: ref HEAD is not a symbolic ref" to stderr if in detached HEAD state.
	stdout, stderr, exitCode, err := runGitCommand(ctx, repoDir, "symbolic-ref", "--short", "HEAD")

	if err == nil && exitCode == 0 {
		// Success, we are on a branch (or unborn branch in an empty repo)
		return strings.TrimSpace(stdout), false, nil
	}

	// Check specifically for the detached HEAD error signature.
	// `git symbolic-ref --short HEAD` exits 128 with "fatal: ref HEAD is not a symbolic ref" when detached.
	if exitCode == 128 && strings.Contains(stderr, "fatal: ref HEAD is not a symbolic ref") {
		// We can be reasonably sure this means detached HEAD.
		// No need for the extra rev-parse check, as symbolic-ref failing this way
		// is the primary indicator.
		return "", true, nil
	}

	// Handle cases where the initial runGitCommand failed for reasons other than the specific detached HEAD case
	// (e.g., directory not found, not a git repo). The error from runGitCommand is used.
	if err != nil {
		return "", false, fmt.Errorf("failed to determine current branch in %s: %w", repoDir, err)
	}

	// If err was nil, but exitCode was non-zero and not the handled detached case.
	return "", false, fmt.Errorf("failed to determine current branch in %s: git symbolic-ref exit code %d, stderr: %s", repoDir, exitCode, stderr)
}

// GetDefaultBranch finds the default branch name (main or master) for a repository.
// It checks for 'main' first, then 'master'. Requires the branch to exist and have commits.
// Returns the default branch name or an error if neither is found or verifiable.
func GetDefaultBranch(ctx context.Context, repoDir string) (branchName string, err error) {
	// `git rev-parse --verify <branch>` checks if a branch exists and points to a valid commit.
	// This correctly handles empty repositories where the initial branch exists but has no commits yet.

	// Try 'main' first
	_, stderrMain, exitCodeMain, errMain := runGitCommand(ctx, repoDir, "rev-parse", "--verify", "main")
	if errMain == nil && exitCodeMain == 0 {
		return "main", nil
	}

	// Try 'master' next
	_, stderrMaster, exitCodeMaster, errMaster := runGitCommand(ctx, repoDir, "rev-parse", "--verify", "master")
	if errMaster == nil && exitCodeMaster == 0 {
		return "master", nil
	}

	// Could not determine default branch
	// Provide more context in the error message
	errMsg := fmt.Sprintf("could not determine default branch (checked main, master) in %s", repoDir)
	if errMain != nil {
		errMsg += fmt.Sprintf("\n main check failed (exit %d): %s", exitCodeMain, strings.TrimSpace(stderrMain))
	}
	if errMaster != nil {
		errMsg += fmt.Sprintf("\n master check failed (exit %d): %s", exitCodeMaster, strings.TrimSpace(stderrMaster))
	}
	// Use the error from the underlying git command if available, otherwise create a generic one.
	finalErr := errMaster // Use the error from the last check
	if finalErr == nil {
		finalErr = errMain // Use the error from the first check if the second succeeded unexpectedly
	}
	if finalErr == nil {
		finalErr = fmt.Errorf(errMsg) // Create a generic error if no underlying error occurred
	} else {
		finalErr = fmt.Errorf("%s: %w", errMsg, finalErr) // Wrap the underlying error
	}

	return "", finalErr
}

// ListLocalBranches lists all local branches in a repository, sorted by commit date (newest first).
// Returns a slice of branch names or an error. Returns an empty slice for an empty repository.
func ListLocalBranches(ctx context.Context, repoDir string) (branchNames []string, err error) {
	// `git branch --list` shows local branches.
	// `--sort=-committerdate` sorts by the date of the last commit on the branch.
	// `--format=%(refname:short)` prints only the short branch name per line.
	stdout, stderr, exitCode, err := runGitCommand(ctx, repoDir, "branch", "--list", "--sort=-committerdate", "--format=%(refname:short)")

	// `git branch --list` exits 0 even in an empty repo (just prints nothing).
	// An error here likely means it's not a git repo or another issue occurred.
	if err != nil {
		// Check if the error is due to non-zero exit code specifically.
		if exitCode != 0 {
			return nil, fmt.Errorf("failed to list branches in %s: %w\nstderr: %s", repoDir, err, stderr)
		}
		// If exit code is 0 but err is not nil, it might be a context issue or similar.
		return nil, fmt.Errorf("failed to list branches in %s: %w", repoDir, err)
	}
	// Double check exit code, although err should be non-nil if exitCode != 0 based on runGitCommand
	if exitCode != 0 {
		return nil, fmt.Errorf("failed to list branches in %s: git branch exit code %d\nstderr: %s", repoDir, exitCode, stderr)
	}

	lines := strings.Split(stdout, "\n")
	branches := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			branches = append(branches, trimmed)
		}
	}

	// If the repo is empty (only `git init` run), `git branch --list` returns nothing (exit code 0).
	// This results in an empty slice, which is the correct behavior.

	return branches, nil
}

// ListWorktreesActivity lists all Git worktrees for a given repository directory.
// It returns a slice of GitWorktree structs, each containing the absolute,
// symlink-resolved path and the corresponding branch name.
// Worktrees with a detached HEAD are excluded.
func ListWorktreesActivity(ctx context.Context, repoDir string) ([]GitWorktree, error) {
	stdout, stderr, exitCode, err := runGitCommand(ctx, repoDir, "worktree", "list", "--porcelain")
	if err != nil {
		// runGitCommand already wraps the error with context
		// Check if the error is specifically "not a git repository" which might be masked by exit code 128
		if exitCode == 128 && strings.Contains(stderr, "not a git repository") {
			return nil, fmt.Errorf("directory '%s' is not a git repository: %w", repoDir, err)
		}
		return nil, err // Return the wrapped error from runGitCommand
	}

	var worktrees []GitWorktree
	entries := strings.Split(strings.TrimSpace(stdout), "\n\n")

	for _, entry := range entries {
		lines := strings.Split(entry, "\n")
		var path, branch string
		isDetached := false

		for _, line := range lines {
			if strings.HasPrefix(line, "worktree ") {
				path = strings.TrimPrefix(line, "worktree ")
			} else if strings.HasPrefix(line, "branch refs/heads/") {
				branch = strings.TrimPrefix(line, "branch refs/heads/")
			} else if line == "detached" {
				isDetached = true
				// If detached, we don't care about the branch line even if present (unlikely)
				branch = "" // Ensure branch is empty if detached
				break       // No need to parse further lines for branch info if detached
			}
		}

		// Only add if we found a path and a non-detached branch
		if path != "" && branch != "" && !isDetached {
			worktrees = append(worktrees, GitWorktree{Path: path, Branch: branch})
		} else if path != "" && !isDetached && branch == "" {
			// This case might occur for the main worktree if it's somehow detached
			// but the 'detached' line wasn't present, or if parsing failed.
			// Or, more likely, the main worktree before the first commit.
			// Let's check if it's the main worktree path.
			absRepoDir, _ := filepath.Abs(repoDir)
			if path == absRepoDir {
				// It's the main worktree, possibly without a branch yet (pre-commit).
				// Or it could be detached without the 'detached' flag (less common).
				// We need a branch name for the map value. Let's skip it if no branch is found.
				// Alternatively, we could try GetCurrentBranch, but let's keep this focused.
				// For now, skip if no branch ref is explicitly listed.
			}
		}
	}

	return worktrees, nil
}
