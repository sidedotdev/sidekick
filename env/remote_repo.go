package env

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"sidekick/coding/unix"

	"go.temporal.io/sdk/temporal"
)

// syncRepoOverSSH uploads a local git repository to a remote environment
// reachable via the given ssh args (ending with the destination), using a git
// bundle transferred over ssh. name distinguishes remote temp files between
// environments. When containerRepoDir is empty, the repo lands under the
// remote $HOME; the resolved directory is returned.
func syncRepoOverSSH(ctx context.Context, sshArgs []string, name, localRepoDir, containerRepoDir string) (string, error) {
	if containerRepoDir == "" {
		homeArgs := make([]string, len(sshArgs))
		copy(homeArgs, sshArgs)
		homeArgs = append(homeArgs, "echo $HOME")
		homeOutput, err := unix.RunCommandActivity(ctx, unix.RunCommandActivityInput{
			WorkingDir: ".",
			Command:    "ssh",
			Args:       homeArgs,
		})
		if err != nil {
			return "", fmt.Errorf("failed to resolve $HOME in sandbox: %w", err)
		}
		home := strings.TrimSpace(homeOutput.Stdout)
		if home == "" {
			home = "/tmp"
		}
		containerRepoDir = filepath.Join(home, filepath.Base(localRepoDir))
	}

	// Create a git bundle containing all refs. The bundle path must be unique
	// per invocation: `git bundle create` creates a sibling .lock file and
	// will refuse to run if either it or the bundle already exists, so a
	// fixed path keyed only on the environment name collides with concurrent
	// or previously-crashed invocations against the same environment.
	bundleFile, err := os.CreateTemp("", fmt.Sprintf("repo-sync-%s-*.bundle", name))
	if err != nil {
		return "", fmt.Errorf("failed to allocate bundle temp file: %w", err)
	}
	bundlePath := bundleFile.Name()
	// Close immediately so git can write to it; also remove the empty
	// placeholder so `git bundle create` does not see a pre-existing file
	// (git refuses to overwrite an existing bundle target).
	bundleFile.Close()
	if err := os.Remove(bundlePath); err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("failed to clear bundle temp file: %w", err)
	}
	defer os.Remove(bundlePath)

	// FIXME skip creating the bundle when it's not needed (when updating)
	bundleOutput, err := unix.RunCommandActivity(ctx, unix.RunCommandActivityInput{
		WorkingDir: localRepoDir,
		Command:    "git",
		Args:       []string{"bundle", "create", bundlePath, "--all"},
	})
	if err != nil {
		return "", fmt.Errorf("failed to create git bundle: %w", err)
	}
	if bundleOutput.ExitStatus != 0 {
		return "", fmt.Errorf("git bundle create failed (exit %d): %s", bundleOutput.ExitStatus, bundleOutput.Stderr)
	}

	// Upload the bundle using ssh with cat + stdin redirect
	// (avoids needing scp binary or separate SSH config for scp)
	remoteBundlePath := "/tmp/repo-" + name + ".bundle"
	uploadArgs := make([]string, len(sshArgs))
	copy(uploadArgs, sshArgs)
	uploadArgs = append(uploadArgs, fmt.Sprintf("cat > %s", shellQuote(remoteBundlePath)))

	uploadOutput, err := unix.RunCommandActivity(ctx, unix.RunCommandActivityInput{
		WorkingDir: os.TempDir(),
		Command:    "sh",
		Args:       []string{"-c", fmt.Sprintf("cat %s | ssh %s", shellQuote(bundlePath), strings.Join(quoteArgs(uploadArgs), " "))},
	})
	if err != nil {
		return "", fmt.Errorf("failed to upload bundle to sandbox: %w", err)
	}
	if uploadOutput.ExitStatus != 0 {
		return "", fmt.Errorf("bundle upload failed (exit %d): %s", uploadOutput.ExitStatus, uploadOutput.Stderr)
	}

	// Clone or update the repo inside the sandbox.
	// If the repo already exists (sandbox reuse), fetch from the bundle and
	// reset to match; otherwise clone fresh. On reuse, the bundle's prior
	// HEAD branch may have been pruned by --prune, leaving the sandbox HEAD
	// dangling; we re-point HEAD at the bundle's HEAD ref and reset via
	// FETCH_HEAD to recover.
	quotedRepo := shellQuote(containerRepoDir)
	quotedBundle := shellQuote(remoteBundlePath)
	cloneScript := fmt.Sprintf(
		"if [ -d %s/.git ]; then "+
			"cd %s && "+
			"head_ref=$(git ls-remote --symref %s HEAD 2>/dev/null | awk '/^ref:/ {print $2; exit}') && "+
			"git fetch --update-head-ok %s '+refs/*:refs/*' --prune && "+
			"if [ -n \"$head_ref\" ]; then git symbolic-ref HEAD \"$head_ref\"; fi && "+
			"git fetch %s && "+
			"if ! git rev-parse --verify HEAD >/dev/null 2>&1; then git update-ref --no-deref HEAD \"$(git rev-parse FETCH_HEAD)\"; fi && "+
			"git reset --hard FETCH_HEAD; "+
			"else "+
			"mkdir -p %s && git clone %s %s && cd %s && "+
			"git config user.name 'Sidekick' && git config user.email 'sidekick@side.dev'; "+
			"fi && rm -f %s",
		quotedRepo,
		quotedRepo,
		quotedBundle,
		quotedBundle,
		quotedBundle,
		shellQuote(filepath.Dir(containerRepoDir)), quotedBundle, quotedRepo, quotedRepo,
		quotedBundle,
	)

	cloneArgs := make([]string, len(sshArgs))
	copy(cloneArgs, sshArgs)
	cloneArgs = append(cloneArgs, cloneScript)

	cloneOutput, err := unix.RunCommandActivity(ctx, unix.RunCommandActivityInput{
		WorkingDir: os.TempDir(),
		Command:    "ssh",
		Args:       cloneArgs,
	})
	if err != nil {
		return "", fmt.Errorf("failed to clone bundle in sandbox: %w", err)
	}
	if cloneOutput.ExitStatus != 0 {
		return "", fmt.Errorf("git clone in sandbox failed (exit %d): %s", cloneOutput.ExitStatus, cloneOutput.Stderr)
	}

	return containerRepoDir, nil
}

// syncMergeResultToLocalOverSSH transfers the given branch from a remote
// environment (reachable via sshArgs) back to the local host repository. Such
// environments hold an independent clone of the repo (synced in via a git
// bundle) rather than a bind mount, so a merge performed inside the
// environment does not otherwise reach the host checkout that is the source
// of truth. This bundles the branch out of the environment and fast-forwards
// the local branch, updating its worktree in place when it is checked out.
func syncMergeResultToLocalOverSSH(ctx context.Context, sshArgs []string, name, workingDirectory, localRepoDir, branch string) error {
	// Bundle the branch inside the sandbox. The working directory is a worktree
	// that shares the object store, so the branch ref is reachable from here.
	remoteBundlePath := "/tmp/repo-back-" + name + ".bundle"
	bundleScript := fmt.Sprintf(
		"cd %s && rm -f %s && git bundle create %s %s",
		shellQuote(workingDirectory),
		shellQuote(remoteBundlePath),
		shellQuote(remoteBundlePath),
		shellQuote(branch),
	)
	bundleArgs := append(append([]string{}, sshArgs...), bundleScript)
	bundleOutput, err := unix.RunCommandActivity(ctx, unix.RunCommandActivityInput{
		WorkingDir: os.TempDir(),
		Command:    "ssh",
		Args:       bundleArgs,
	})
	if err != nil {
		return fmt.Errorf("failed to create git bundle in sandbox: %w", err)
	}
	if bundleOutput.ExitStatus != 0 {
		return fmt.Errorf("git bundle create in sandbox failed (exit %d): %s", bundleOutput.ExitStatus, bundleOutput.Stderr)
	}

	localBundle, err := os.CreateTemp("", fmt.Sprintf("repo-back-%s-*.bundle", name))
	if err != nil {
		return fmt.Errorf("failed to allocate bundle temp file: %w", err)
	}
	localBundlePath := localBundle.Name()
	localBundle.Close()
	defer os.Remove(localBundlePath)

	downloadArgs := append(append([]string{}, sshArgs...), fmt.Sprintf("cat %s", shellQuote(remoteBundlePath)))
	downloadOutput, err := unix.RunCommandActivity(ctx, unix.RunCommandActivityInput{
		WorkingDir: os.TempDir(),
		Command:    "sh",
		Args:       []string{"-c", fmt.Sprintf("ssh %s > %s", strings.Join(quoteArgs(downloadArgs), " "), shellQuote(localBundlePath))},
	})
	if err != nil {
		return fmt.Errorf("failed to download bundle from sandbox: %w", err)
	}
	if downloadOutput.ExitStatus != 0 {
		return fmt.Errorf("bundle download failed (exit %d): %s", downloadOutput.ExitStatus, downloadOutput.Stderr)
	}

	// Best-effort cleanup of the sandbox-side bundle.
	cleanupArgs := append(append([]string{}, sshArgs...), fmt.Sprintf("rm -f %s", shellQuote(remoteBundlePath)))
	_, _ = unix.RunCommandActivity(ctx, unix.RunCommandActivityInput{
		WorkingDir: os.TempDir(),
		Command:    "ssh",
		Args:       cleanupArgs,
	})

	// Apply the branch to the local repo. The bundle path and branch are passed
	// as positional arguments ($1, $2) rather than interpolated into the script
	// so that arbitrary branch names cannot alter the shell command. Fetch into
	// a temporary ref first, which is always permitted (even for the currently
	// checked-out branch), then advance the local branch: when a worktree has it
	// checked out, merge --ff-only there so both the ref and the working tree
	// move; otherwise update the ref directly.
	applyScript := `set -e
bundle="$1"
branch="$2"
tempref="refs/sidekick-sync/$branch"
git fetch "$bundle" "+refs/heads/$branch:$tempref"
wt=$(git worktree list --porcelain | awk -v b="refs/heads/$branch" '$1=="worktree"{p=$2} $1=="branch"&&$2==b{print p; exit}')
if [ -n "$wt" ]; then git -C "$wt" merge --ff-only "$tempref"; else git update-ref "refs/heads/$branch" "$tempref"; fi
git update-ref -d "$tempref"`
	applyOutput, err := unix.RunCommandActivity(ctx, unix.RunCommandActivityInput{
		WorkingDir: localRepoDir,
		Command:    "sh",
		Args:       []string{"-c", applyScript, "sh", localBundlePath, branch},
	})
	if err != nil {
		return fmt.Errorf("failed to apply synced branch to local repo: %w", err)
	}
	if applyOutput.ExitStatus != 0 {
		return fmt.Errorf("applying synced branch to local repo failed (exit %d): %s", applyOutput.ExitStatus, applyOutput.Stderr)
	}
	return nil
}

// createRemoteWorktree creates a git worktree under $HOME/sidekick-worktrees
// inside a remote environment via its RunCommand, so that the worktree's .git
// references resolve within the remote filesystem.
func createRemoteWorktree(ctx context.Context, envContainer EnvContainer, repoDir, branchName, startBranch, workspaceId string) (string, error) {
	repoName := filepath.Base(repoDir)
	branchSuffix := strings.TrimPrefix(branchName, "side/")
	dirName := repoName + "-" + branchSuffix

	homeOutput, err := envContainer.Env.RunCommand(ctx, EnvRunCommandInput{
		Command: "sh",
		Args:    []string{"-c", "echo $HOME"},
	})
	if err != nil {
		return "", fmt.Errorf("failed to resolve $HOME in environment: %w", err)
	}
	baseDir := strings.TrimSpace(homeOutput.Stdout)
	if baseDir == "" {
		baseDir = "/tmp"
	}
	worktreePath := filepath.Join(baseDir, "sidekick-worktrees", workspaceId, dirName)

	mkdirOutput, err := envContainer.Env.RunCommand(ctx, EnvRunCommandInput{
		Command: "mkdir",
		Args:    []string{"-p", worktreePath},
	})
	if err != nil {
		return "", fmt.Errorf("failed to create worktree directory in sandbox: %w", err)
	}
	if mkdirOutput.ExitStatus != 0 {
		return "", fmt.Errorf("mkdir failed in sandbox (exit %d): %s", mkdirOutput.ExitStatus, mkdirOutput.Stderr)
	}

	baseRef := "HEAD"
	if startBranch != "" {
		baseRef = startBranch
	}
	addOutput, err := envContainer.Env.RunCommand(ctx, EnvRunCommandInput{
		Command: "git",
		Args:    []string{"worktree", "add", "-b", branchName, worktreePath, baseRef},
	})
	if err != nil {
		return "", fmt.Errorf("failed to run git worktree add in sandbox: %w", err)
	}
	if addOutput.ExitStatus != 0 {
		err := fmt.Errorf("git worktree add failed in sandbox (exit %d): %s", addOutput.ExitStatus, addOutput.Stderr)
		if strings.Contains(addOutput.Stderr, "already exists") {
			return "", temporal.NewNonRetryableApplicationError(
				err.Error(),
				ErrTypeBranchAlreadyExists,
				err,
			)
		}
		return "", err
	}

	return worktreePath, nil
}
