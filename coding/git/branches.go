package git

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"sidekick/env"
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

// BranchState represents the current branch state of a Git repository.
type BranchState struct {
	// Name is the current branch name. Empty if in detached HEAD state.
	Name string
	// IsDetached indicates whether HEAD is in a detached state.
	IsDetached bool
}

// GetCurrentBranch determines the current branch name or detached HEAD state for a repository.
// It returns the branch state and any error encountered.
// An empty repository (initialized but without commits) will return the initial branch name (e.g., "main" or "master")
// and IsDetached=false.
// FIXME: adjust to take in an env.EnvContainer instead of repoDir, and use
// EnvRunCommand instead of runGitCommand. note: error behavior is that a
// non-zero exit code is an err for runGitCommand, but not for EnvRunCommand.
func GetCurrentBranch(ctx context.Context, repoDir string) (BranchState, error) {
	// `git symbolic-ref --short HEAD` returns the current branch name if on a branch.
	// It exits with 0 even in an empty repo after `git init -b <name>`.
	// It exits with 1 and prints "fatal: ref HEAD is not a symbolic ref" to stderr if in detached HEAD state.
	stdout, stderr, exitCode, err := runGitCommand(ctx, repoDir, "symbolic-ref", "--short", "HEAD")

	if err == nil && exitCode == 0 {
		// Success, we are on a branch (or unborn branch in an empty repo)
		return BranchState{Name: strings.TrimSpace(stdout)}, nil
	}

	// Check specifically for the detached HEAD error signature.
	// `git symbolic-ref --short HEAD` exits 128 with "fatal: ref HEAD is not a symbolic ref" when detached.
	if exitCode == 128 && strings.Contains(stderr, "fatal: ref HEAD is not a symbolic ref") {
		// We can be reasonably sure this means detached HEAD.
		// No need for the extra rev-parse check, as symbolic-ref failing this way
		// is the primary indicator.
		return BranchState{IsDetached: true}, nil
	}

	// Handle cases where the initial runGitCommand failed for reasons other than the specific detached HEAD case
	// (e.g., directory not found, not a git repo). The error from runGitCommand is used.
	if err != nil {
		return BranchState{}, fmt.Errorf("failed to determine current branch in %s: %w", repoDir, err)
	}

	// If err was nil, but exitCode was non-zero and not the handled detached case.
	return BranchState{}, fmt.Errorf("failed to determine current branch in %s: git symbolic-ref exit code %d, stderr: %s", repoDir, exitCode, stderr)
}

// GetDefaultBranch finds the default branch name for a repository.
// It first tries to determine the default from origin/HEAD (most reliable when remote exists),
// then falls back to checking for 'main' or 'master' branches.
// Returns the default branch name or an error if it cannot be determined.
func GetDefaultBranch(ctx context.Context, repoDir string) (branchName string, err error) {
	devEnv, err := env.NewLocalEnv(ctx, env.LocalEnvParams{RepoDir: repoDir})
	if err != nil {
		return "", err
	}

	// First, try to get the default branch from origin/HEAD (most reliable)
	result, err := devEnv.RunCommand(ctx, env.EnvRunCommandInput{
		Command: "git",
		Args:    []string{"symbolic-ref", "refs/remotes/origin/HEAD"},
	})
	if err != nil {
		return "", err
	}
	if result.ExitStatus == 0 {
		// Output is like "refs/remotes/origin/main", extract the branch name
		ref := strings.TrimSpace(result.Stdout)
		if strings.HasPrefix(ref, "refs/remotes/origin/") {
			return strings.TrimPrefix(ref, "refs/remotes/origin/"), nil
		}
	}

	// Fall back to checking for 'main' first
	result, err = devEnv.RunCommand(ctx, env.EnvRunCommandInput{
		Command: "git",
		Args:    []string{"rev-parse", "--verify", "main"},
	})
	if err != nil {
		return "", err
	}
	if result.ExitStatus == 0 {
		return "main", nil
	}

	// Try 'master' next
	result, err = devEnv.RunCommand(ctx, env.EnvRunCommandInput{
		Command: "git",
		Args:    []string{"rev-parse", "--verify", "master"},
	})
	if err != nil {
		return "", err
	}
	if result.ExitStatus == 0 {
		return "master", nil
	}

	return "", fmt.Errorf("could not determine default branch for %s: neither origin/HEAD, main, nor master found", repoDir)
}

// ListLocalBranches lists all local branches in a repository, sorted by commit date (newest first).
// Returns a slice of branch names or an error. Returns an empty slice for an empty repository.
// FIXME: adjust to take in an env.EnvContainer instead of repoDir, and use
// EnvRunCommand instead of runGitCommand. note: error behavior is that a
// non-zero exit code is an err for runGitCommand, but not for EnvRunCommand.
func ListLocalBranches(ctx context.Context, repoDir string) (branchNames []string, err error) {
	// `git branch --list` shows local branches.
	// `--sort=-committerdate` sorts by the date of the last commit on the branch.
	// `--format=%(refname:short)` prints only the short branch name per line.
	stdout, stderr, exitCode, err := runGitCommand(ctx, repoDir, "branch", "--list", "--sort=-committerdate", "--format=%(refname:short)")

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

	return branches, nil
}
