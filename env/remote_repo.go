package env

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"sidekick/coding/unix"

	"github.com/rs/zerolog/log"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
)

// seedCloneDepth is how much history a fresh sandbox clone starts with:
// enough for a coding agent to inspect recent changes, while keeping the
// first sync fast even for repos with large histories.
const seedCloneDepth = 25

// syncRepoOverSSH mirrors the local HEAD branch (plus any extraBranches) of
// a local git repository into a remote environment reachable via the given
// ssh args (ending with the destination). A missing remote repo is first
// seeded as a shallow clone served straight from the host through a
// reverse-forwarded tunnel; updates go through `git push` over the same ssh
// transport. When containerRepoDir is empty, the repo lands under the
// remote $HOME; the resolved directory is returned.
func syncRepoOverSSH(ctx context.Context, sshArgs []string, localRepoDir, containerRepoDir string, extraBranches []string) (string, error) {
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

	headRef, err := localHeadRef(ctx, localRepoDir)
	if err != nil {
		return "", err
	}
	refspecs := syncRefspecs(headRef, extraBranches)

	exists, err := remoteRepoExists(ctx, sshArgs, containerRepoDir)
	if err != nil {
		return "", err
	}
	if !exists {
		if err := seedRepoOverSSH(ctx, sshArgs, localRepoDir, containerRepoDir, headRef, extraBranches); err != nil {
			// The push below seeds the repo too (with the full history of
			// the synced branches): slower, but it needs no reverse tunnel.
			log.Warn().Err(err).Str("remoteRepoDir", containerRepoDir).
				Msg("shallow repo seed failed; falling back to seeding via push")
		}
	}

	if err := pushRepoSyncOverSSH(ctx, sshArgs, localRepoDir, containerRepoDir, headRef, refspecs); err != nil {
		return "", err
	}
	return containerRepoDir, nil
}

// syncRefspecs returns forced refspecs for the HEAD branch plus any extra
// branches, deduplicated.
func syncRefspecs(headRef string, extraBranches []string) []string {
	refs := []string{headRef}
	for _, branch := range extraBranches {
		ref := branch
		if !strings.HasPrefix(ref, "refs/") {
			ref = "refs/heads/" + ref
		}
		if !slices.Contains(refs, ref) {
			refs = append(refs, ref)
		}
	}
	specs := make([]string, len(refs))
	for i, ref := range refs {
		specs[i] = fmt.Sprintf("+%s:%s", ref, ref)
	}
	return specs
}

// remoteRepoExists reports whether a git repo already exists at
// containerRepoDir in the remote environment.
func remoteRepoExists(ctx context.Context, sshArgs []string, containerRepoDir string) (bool, error) {
	script := fmt.Sprintf("if [ -d %s/.git ]; then echo EXISTS; else echo MISSING; fi", shellQuote(containerRepoDir))
	checkArgs := make([]string, len(sshArgs))
	copy(checkArgs, sshArgs)
	checkArgs = append(checkArgs, script)
	output, err := unix.RunCommandActivity(ctx, unix.RunCommandActivityInput{
		WorkingDir: os.TempDir(),
		Command:    "ssh",
		Args:       checkArgs,
	})
	if err != nil {
		return false, fmt.Errorf("failed to check for remote repo: %w", err)
	}
	if output.ExitStatus != 0 {
		return false, fmt.Errorf("checking for remote repo failed (exit %d): %s", output.ExitStatus, output.Stderr)
	}
	return strings.Contains(output.Stdout, "EXISTS"), nil
}

// seedRepoOverSSH creates the remote repo as a shallow clone (real shas,
// truncated history) of the local repo, fetched straight from the host
// through a reverse-forwarded tunnel.
func seedRepoOverSSH(ctx context.Context, sshArgs []string, localRepoDir, containerRepoDir, headRef string, extraBranches []string) error {
	seedStart := time.Now()
	headBranch := strings.TrimPrefix(headRef, "refs/heads/")
	quotedRepo := shellQuote(containerRepoDir)
	err := tunneledRepoScriptOverSSH(ctx, sshArgs, localRepoDir, func(url string) string {
		var script strings.Builder
		script.WriteString("mkdir -p " + shellQuote(filepath.Dir(containerRepoDir)))
		script.WriteString(fmt.Sprintf(" && git clone --depth %d --no-tags --single-branch --branch %s %s %s",
			seedCloneDepth, shellQuote(headBranch), url, quotedRepo))
		for _, spec := range syncRefspecs(headRef, extraBranches)[1:] {
			script.WriteString(fmt.Sprintf(" && git -C %s fetch --depth %d %s %s",
				quotedRepo, seedCloneDepth, url, shellQuote(spec)))
		}
		// The origin remote points at the tunnel, which is gone after this
		// command; remove it so nothing in the sandbox tries to use it.
		script.WriteString(fmt.Sprintf(" && git -C %s remote remove origin", quotedRepo))
		script.WriteString(fmt.Sprintf(" && git -C %s config user.name 'Sidekick' && git -C %s config user.email 'sidekick@side.dev'",
			quotedRepo, quotedRepo))
		return script.String()
	})
	if err != nil {
		return fmt.Errorf("seeding remote repo failed: %w", err)
	}
	log.Debug().
		Str("mode", "seed").
		Str("remoteRepoDir", containerRepoDir).
		Dur("seedDur", time.Since(seedStart)).
		Msg("synced repo over ssh")
	return nil
}

// tunneledRepoScriptOverSSH runs a remote shell script produced by
// buildScript, which receives a git URL from which the remote can fetch the
// local repo: an in-process upload-pack server on the host loopback is
// reverse-forwarded into the remote environment for the duration of the one
// ssh command, so the remote only ever gets temporary read-only access to
// this single repository. The remote-side tunnel port is chosen at random
// and retried on collision, which surfaces via ExitOnForwardFailure.
func tunneledRepoScriptOverSSH(ctx context.Context, sshArgs []string, localRepoDir string, buildScript func(url string) string) error {
	localPort, stop, err := startLocalGitUploadPackServer(ctx, localRepoDir)
	if err != nil {
		return err
	}
	defer stop()

	dest, opts := splitSSHDestination(sshArgs)
	if dest == "" {
		return fmt.Errorf("could not determine ssh destination from args %v", sshArgs)
	}

	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		remotePort := 20000 + rand.Intn(40000)
		url := fmt.Sprintf("git://127.0.0.1:%d/repo", remotePort)
		runArgs := append(append([]string{}, opts...),
			"-o", "ExitOnForwardFailure=yes",
			"-R", fmt.Sprintf("127.0.0.1:%d:127.0.0.1:%d", remotePort, localPort),
			dest, buildScript(url))
		output, err := unix.RunCommandActivity(ctx, unix.RunCommandActivityInput{
			WorkingDir: os.TempDir(),
			Command:    "ssh",
			Args:       runArgs,
		})
		if err != nil {
			return fmt.Errorf("failed to run tunneled repo script: %w", err)
		}
		if output.ExitStatus == 0 {
			return nil
		}
		lastErr = fmt.Errorf("tunneled repo script failed (exit %d): %s", output.ExitStatus, output.Stderr)
	}
	return lastErr
}

// pushRepoSyncOverSSH updates the given refspecs in the remote repo via `git
// push` over the same ssh transport: git execs git-receive-pack through the
// existing sshd channel, so no daemon or extra remote service is required,
// only git itself. Native negotiation provides incrementality and backward
// moves in one connection.
func pushRepoSyncOverSSH(ctx context.Context, sshArgs []string, localRepoDir, containerRepoDir, headRef string, refspecs []string) error {
	dest, opts := splitSSHDestination(sshArgs)
	if dest == "" {
		return fmt.Errorf("could not determine ssh destination from args %v", sshArgs)
	}
	gitSSH := "ssh"
	for _, a := range opts {
		gitSSH += " " + shellQuote(a)
	}

	// Prepare the remote repo: init when missing, and allow pushes to (and
	// deletions of) checked-out branches; the working tree is realigned
	// right after the push.
	quotedRepo := shellQuote(containerRepoDir)
	prepScript := fmt.Sprintf(
		"if [ ! -d %s/.git ]; then mkdir -p %s && git init -q %s && "+
			"git -C %s config user.name 'Sidekick' && git -C %s config user.email 'sidekick@side.dev'; fi && "+
			"git -C %s config receive.denyCurrentBranch ignore && "+
			"git -C %s config receive.denyDeleteCurrent ignore",
		quotedRepo, quotedRepo, quotedRepo, quotedRepo, quotedRepo, quotedRepo, quotedRepo,
	)
	prepArgs := make([]string, len(sshArgs))
	copy(prepArgs, sshArgs)
	prepArgs = append(prepArgs, prepScript)
	prepStart := time.Now()
	prepOutput, err := unix.RunCommandActivity(ctx, unix.RunCommandActivityInput{
		WorkingDir: os.TempDir(),
		Command:    "ssh",
		Args:       prepArgs,
	})
	if err != nil {
		return fmt.Errorf("failed to prepare remote repo for push: %w", err)
	}
	if prepOutput.ExitStatus != 0 {
		return fmt.Errorf("preparing remote repo for push failed (exit %d): %s", prepOutput.ExitStatus, prepOutput.Stderr)
	}
	prepDur := time.Since(prepStart)

	pushStart := time.Now()
	pushArgs := append([]string{"-C", localRepoDir, "push", "--quiet", dest + ":" + containerRepoDir}, refspecs...)
	cmd := exec.CommandContext(ctx, "git", pushArgs...)
	cmd.Env = append(os.Environ(), "GIT_SSH_COMMAND="+gitSSH)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git push to %s: %w: %s", containerRepoDir, err, string(out))
	}
	pushDur := time.Since(pushStart)

	applyScript := "cd " + quotedRepo +
		" && git symbolic-ref HEAD " + shellQuote(headRef) +
		" && git reset --hard"
	applyArgs := make([]string, len(sshArgs))
	copy(applyArgs, sshArgs)
	applyArgs = append(applyArgs, applyScript)
	applyStart := time.Now()
	applyOutput, err := unix.RunCommandActivity(ctx, unix.RunCommandActivityInput{
		WorkingDir: os.TempDir(),
		Command:    "ssh",
		Args:       applyArgs,
	})
	if err != nil {
		return fmt.Errorf("failed to realign remote repo after push: %w", err)
	}
	if applyOutput.ExitStatus != 0 {
		return fmt.Errorf("realigning remote repo after push failed (exit %d): %s", applyOutput.ExitStatus, applyOutput.Stderr)
	}

	log.Debug().
		Str("mode", "push").
		Str("remoteRepoDir", containerRepoDir).
		Dur("prepDur", prepDur).
		Dur("pushDur", pushDur).
		Dur("remoteApplyDur", time.Since(applyStart)).
		Msg("synced repo over ssh")

	return nil
}

// localHeadRef returns the local HEAD's branch ref, or an error when HEAD is
// detached: the incremental path relies on a symbolic HEAD to re-point the
// remote, while the full sync handles detached HEAD via the bundle's HEAD
// entry.
func localHeadRef(ctx context.Context, localRepoDir string) (string, error) {
	output, err := unix.RunCommandActivity(ctx, unix.RunCommandActivityInput{
		WorkingDir: localRepoDir,
		Command:    "git",
		Args:       []string{"symbolic-ref", "--quiet", "HEAD"},
	})
	if err != nil {
		return "", fmt.Errorf("failed to resolve local HEAD: %w", err)
	}
	if output.ExitStatus != 0 {
		return "", fmt.Errorf("local HEAD is detached")
	}
	return strings.TrimSpace(output.Stdout), nil
}

// syncMergeResultToLocalOverSSH transfers the given branch from a remote
// environment (reachable via sshArgs) back to the local host repository. Such
// environments hold an independent clone of the repo rather than a bind
// mount, so a merge performed inside the environment does not otherwise
// reach the host checkout that is the source of truth. The branch is fetched
// into a temporary ref (negotiated transfer, so only missing objects move)
// and the local branch is then fast-forwarded, updating its worktree in
// place when it is checked out.
func syncMergeResultToLocalOverSSH(ctx context.Context, sshArgs []string, workingDirectory, localRepoDir, branch string) error {
	tempRef := "refs/sidekick-sync/" + branch
	refspec := fmt.Sprintf("+refs/heads/%s:%s", branch, tempRef)
	if err := fetchRemoteRefToLocal(ctx, sshArgs, workingDirectory, localRepoDir, refspec); err != nil {
		return err
	}

	// Advance the local branch from the temporary ref (fetching into one is
	// always permitted, even for the currently checked-out branch): when a
	// worktree has the branch checked out, merge --ff-only there so both the
	// ref and the working tree move; otherwise update the ref directly. The
	// branch is passed as a positional argument ($1) rather than
	// interpolated into the script so that arbitrary branch names cannot
	// alter the shell command.
	applyScript := `set -e
branch="$1"
tempref="refs/sidekick-sync/$branch"
wt=$(git worktree list --porcelain | awk -v b="refs/heads/$branch" '$1=="worktree"{p=$2} $1=="branch"&&$2==b{print p; exit}')
if [ -n "$wt" ]; then git -C "$wt" merge --ff-only "$tempref"; else git update-ref "refs/heads/$branch" "$tempref"; fi
git update-ref -d "$tempref"`
	applyOutput, err := unix.RunCommandActivity(ctx, unix.RunCommandActivityInput{
		WorkingDir: localRepoDir,
		Command:    "sh",
		Args:       []string{"-c", applyScript, "sh", branch},
	})
	if err != nil {
		return fmt.Errorf("failed to apply synced branch to local repo: %w", err)
	}
	if applyOutput.ExitStatus != 0 {
		return fmt.Errorf("applying synced branch to local repo failed (exit %d): %s", applyOutput.ExitStatus, applyOutput.Stderr)
	}
	return nil
}

// syncGitRefToLocalOverSSH transfers a single fully-qualified ref (e.g. an
// archive tag like "refs/tags/archive/foo") from a remote environment's clone
// into the local repo. Unlike branch syncing there is no local working tree
// to update, so a plain fetch of the ref suffices.
func syncGitRefToLocalOverSSH(ctx context.Context, sshArgs []string, workingDirectory, localRepoDir, ref string) error {
	return fetchRemoteRefToLocal(ctx, sshArgs, workingDirectory, localRepoDir, fmt.Sprintf("%s:%s", ref, ref))
}

// syncFlowBranchToLocalOverSSH backs up a flow branch's commits from a remote
// environment's clone into the local repo, force-updating the same-name local
// branch. Forcing is safe because the local branch is reserved for the flow
// and never checked out, so no working tree can be affected; the fetch is
// negotiated, so only commits missing locally are transferred.
func syncFlowBranchToLocalOverSSH(ctx context.Context, sshArgs []string, workingDirectory, localRepoDir, branch string) error {
	refspec := fmt.Sprintf("+refs/heads/%s:refs/heads/%s", branch, branch)
	return fetchRemoteRefToLocal(ctx, sshArgs, workingDirectory, localRepoDir, refspec)
}

// fetchRemoteRefToLocal fetches the given refspec from a remote
// environment's repo into the local one via git's negotiated transfer over
// the same ssh transport (git execs git-upload-pack through sshd; no daemon
// is needed). workingDirectory may be a worktree: it shares the object
// store, so any repo-level ref is reachable from there.
func fetchRemoteRefToLocal(ctx context.Context, sshArgs []string, workingDirectory, localRepoDir, refspec string) error {
	dest, opts := splitSSHDestination(sshArgs)
	if dest == "" {
		return fmt.Errorf("could not determine ssh destination from args %v", sshArgs)
	}
	gitSSH := "ssh"
	for _, a := range opts {
		gitSSH += " " + shellQuote(a)
	}
	cmd := exec.CommandContext(ctx, "git", "-C", localRepoDir,
		"fetch", "--no-tags", "--no-write-fetch-head", dest+":"+workingDirectory, refspec)
	cmd.Env = append(os.Environ(), "GIT_SSH_COMMAND="+gitSSH)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git fetch %s %s: %w: %s", workingDirectory, refspec, err, string(out))
	}
	return nil
}

// syncBranchToRemoteOverSSH force-updates a single branch in a remote
// environment's clone from the local repository, which is the source of truth
// for it. When the branch is checked out in a remote worktree, that worktree
// is realigned with a hard reset, so callers must ensure any uncommitted
// changes there have been preserved first. A branch missing locally is
// skipped, leaving the remote branch untouched.
func syncBranchToRemoteOverSSH(ctx context.Context, sshArgs []string, workingDirectory, localRepoDir, branch string) error {
	verifyOutput, err := unix.RunCommandActivity(ctx, unix.RunCommandActivityInput{
		WorkingDir: localRepoDir,
		Command:    "git",
		Args:       []string{"rev-parse", "--verify", "--quiet", "refs/heads/" + branch},
	})
	if err != nil {
		return fmt.Errorf("failed to check for local branch %s: %w", branch, err)
	}
	if verifyOutput.ExitStatus != 0 {
		// The branch only exists in the remote environment, so there is
		// nothing local to refresh it from.
		return nil
	}

	dest, opts := splitSSHDestination(sshArgs)
	if dest == "" {
		return fmt.Errorf("could not determine ssh destination from args %v", sshArgs)
	}
	gitSSH := "ssh"
	for _, a := range opts {
		gitSSH += " " + shellQuote(a)
	}

	// Allow updating the branch even while it is checked out remotely; the
	// affected worktree is realigned right after the push.
	prepScript := fmt.Sprintf("git -C %s config receive.denyCurrentBranch ignore", shellQuote(workingDirectory))
	prepArgs := append(slices.Clone(sshArgs), prepScript)
	prepOutput, err := unix.RunCommandActivity(ctx, unix.RunCommandActivityInput{
		WorkingDir: os.TempDir(),
		Command:    "ssh",
		Args:       prepArgs,
	})
	if err != nil {
		return fmt.Errorf("failed to prepare remote repo for branch sync: %w", err)
	}
	if prepOutput.ExitStatus != 0 {
		return fmt.Errorf("preparing remote repo for branch sync failed (exit %d): %s", prepOutput.ExitStatus, prepOutput.Stderr)
	}

	refspec := fmt.Sprintf("+refs/heads/%s:refs/heads/%s", branch, branch)
	pushStart := time.Now()
	cmd := exec.CommandContext(ctx, "git", "-C", localRepoDir, "push", "--quiet", dest+":"+workingDirectory, refspec)
	cmd.Env = append(os.Environ(), "GIT_SSH_COMMAND="+gitSSH)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git push branch %s to %s: %w: %s", branch, workingDirectory, err, string(out))
	}

	// The push only moved the ref, so realign any remote worktree that has the
	// branch checked out.
	applyScript := fmt.Sprintf(
		`cd %s && wt=$(git worktree list --porcelain | awk -v b=refs/heads/%s '$1=="worktree"{p=$2} $1=="branch"&&$2==b{print p; exit}') && `+
			`if [ -n "$wt" ]; then git -C "$wt" reset --hard refs/heads/%s; fi`,
		shellQuote(workingDirectory), shellQuote(branch), shellQuote(branch),
	)
	applyArgs := append(slices.Clone(sshArgs), applyScript)
	applyOutput, err := unix.RunCommandActivity(ctx, unix.RunCommandActivityInput{
		WorkingDir: os.TempDir(),
		Command:    "ssh",
		Args:       applyArgs,
	})
	if err != nil {
		return fmt.Errorf("failed to realign remote worktree after branch sync: %w", err)
	}
	if applyOutput.ExitStatus != 0 {
		return fmt.Errorf("realigning remote worktree after branch sync failed (exit %d): %s", applyOutput.ExitStatus, applyOutput.Stderr)
	}

	log.Debug().
		Str("mode", "branch-push").
		Str("branch", branch).
		Dur("pushDur", time.Since(pushStart)).
		Msg("synced branch to remote over ssh")

	return nil
}

// reserveLocalBranch creates the flow branch in the worker-local repository,
// which is the sole authority on flow branch name availability. The branch is
// never checked out locally, so no working tree is affected.
func reserveLocalBranch(ctx context.Context, localRepoDir, branchName, startBranch string) error {
	startRef := "HEAD"
	if startBranch != "" {
		startRef = startBranch
	}
	out, err := exec.CommandContext(ctx, "git", "-C", localRepoDir, "branch", branchName, startRef).CombinedOutput()
	if err == nil {
		return nil
	}
	reserveErr := fmt.Errorf("failed to reserve branch %s in local repo %s: %w: %s", branchName, localRepoDir, err, string(out))
	if strings.Contains(string(out), "already exists") {
		return temporal.NewNonRetryableApplicationError(
			reserveErr.Error(),
			ErrTypeBranchAlreadyExists,
			reserveErr,
		)
	}
	return reserveErr
}

// clearStaleRemoteWorktree removes same-name branch worktrees and leftover
// worktree directories in the sandbox, which can survive in restored or reused
// snapshots. It is only used when the local repo holds branch-name authority,
// so stale sandbox state must never block or rename a flow branch.
func clearStaleRemoteWorktree(ctx context.Context, envContainer EnvContainer, repoDir, branchName, worktreePath string) error {
	quotedRepo := shellQuote(repoDir)
	quotedPath := shellQuote(worktreePath)
	script := fmt.Sprintf(
		`cd %s && git worktree prune && `+
			`git worktree list --porcelain | awk -v b=%s '$1=="worktree"{p=$2} $1=="branch"&&$2==b{print p}' | `+
			`while IFS= read -r wt; do git worktree unlock "$wt" >/dev/null 2>&1; git worktree remove --force "$wt" >/dev/null 2>&1; done; `+
			`git worktree unlock %s >/dev/null 2>&1; git worktree remove --force %s >/dev/null 2>&1; `+
			`rm -rf %s; git worktree prune`,
		quotedRepo, shellQuote("refs/heads/"+branchName), quotedPath, quotedPath, quotedPath,
	)
	output, err := envContainer.Env.RunCommand(ctx, EnvRunCommandInput{
		Command: "sh",
		Args:    []string{"-c", script},
	})
	if err != nil {
		return fmt.Errorf("failed to clear stale worktree state in sandbox: %w", err)
	}
	if output.ExitStatus != 0 {
		return fmt.Errorf("clearing stale worktree state failed in sandbox (exit %d): %s", output.ExitStatus, output.Stderr)
	}
	return nil
}

// createRemoteWorktree creates a git worktree under $HOME/sidekick-worktrees
// inside a remote environment via its RunCommand, so that the worktree's .git
// references resolve within the remote filesystem. When localRepoDir is set,
// the same-name branch is first reserved in the worker-local repository, which
// then decides name availability and makes the sandbox side tolerant of stale
// same-name state.
func createRemoteWorktree(ctx context.Context, envContainer EnvContainer, repoDir, branchName, startBranch, workspaceId, localRepoDir string) (string, error) {
	if localRepoDir != "" {
		if err := reserveLocalBranch(ctx, localRepoDir, branchName, startBranch); err != nil {
			return "", err
		}
	}

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

	if localRepoDir != "" {
		if err := clearStaleRemoteWorktree(ctx, envContainer, repoDir, branchName, worktreePath); err != nil {
			return "", err
		}
	}

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
	createFlag := "-b"
	if localRepoDir != "" {
		createFlag = "-B"
	}
	addOutput, err := envContainer.Env.RunCommand(ctx, EnvRunCommandInput{
		Command: "git",
		Args:    []string{"worktree", "add", createFlag, branchName, worktreePath, baseRef},
	})
	if err != nil {
		return "", fmt.Errorf("failed to run git worktree add in sandbox: %w", err)
	}
	if addOutput.ExitStatus != 0 {
		err := fmt.Errorf("git worktree add failed in sandbox (exit %d): %s", addOutput.ExitStatus, addOutput.Stderr)
		if localRepoDir == "" && strings.Contains(addOutput.Stderr, "already exists") {
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

type SyncRepoToRemoteInput struct {
	EnvContainer EnvContainer `json:"envContainer"`
	// LocalRepoDir is the local repository to upload.
	LocalRepoDir string `json:"localRepoDir"`
	// RemoteRepoDir optionally overrides where the repo lands in the remote
	// environment. Defaults to $HOME/<repo basename>.
	RemoteRepoDir string `json:"remoteRepoDir,omitempty"`
	// Branches lists branches to sync besides the local HEAD branch (e.g. a
	// task's start branch). Only these and the HEAD branch are guaranteed
	// to exist in the remote repo.
	Branches []string `json:"branches,omitempty"`
}

type SyncRepoToRemoteOutput struct {
	RemoteRepoDir string `json:"remoteRepoDir"`
}

// SyncRepoToRemoteActivity mirrors a local git repository's HEAD branch
// (plus Branches) into any SSH-capable environment, seeding missing repos as
// shallow clones and pushing updates otherwise. The environment's working
// directory is not required to exist yet: the repo lands under the remote
// $HOME unless RemoteRepoDir is set.
func SyncRepoToRemoteActivity(ctx context.Context, input SyncRepoToRemoteInput) (SyncRepoToRemoteOutput, error) {
	sshEnv, ok := input.EnvContainer.Env.(SSHCapableEnv)
	if !ok {
		return SyncRepoToRemoteOutput{}, fmt.Errorf("env type %s does not support SSH-based repo sync", input.EnvContainer.Env.GetType())
	}
	HoldReverseForwards(ctx, sshEnv)

	var remoteRepoDir string
	err := RunWithSSHTransportRecovery(ctx, input.EnvContainer.Env, func() error {
		sshArgs, err := sshEnv.SSHArgs(ctx)
		if err != nil {
			return fmt.Errorf("failed to get SSH args: %w", err)
		}
		remoteRepoDir, err = syncRepoOverSSH(ctx, sshArgs, input.LocalRepoDir, input.RemoteRepoDir, input.Branches)
		return err
	})
	if err != nil {
		return SyncRepoToRemoteOutput{}, err
	}
	return SyncRepoToRemoteOutput{RemoteRepoDir: remoteRepoDir}, nil
}

type CreateRemoteWorktreeInput struct {
	EnvContainer EnvContainer `json:"envContainer"`
	RepoDir      string       `json:"repoDir"`
	BranchName   string       `json:"branchName"`
	StartBranch  string       `json:"startBranch,omitempty"`
	WorkspaceId  string       `json:"workspaceId"`
	// LocalRepoDir is the worker-local repository in which the flow branch is
	// reserved. When set, it is the sole authority on branch name availability.
	LocalRepoDir string `json:"localRepoDir,omitempty"`
}

type CreateRemoteWorktreeOutput struct {
	WorktreePath string `json:"worktreePath"`
}

// CreateRemoteWorktreeActivity creates a git worktree inside a remote
// environment via its RunCommand, so that the worktree's .git references
// resolve within the remote filesystem.
func CreateRemoteWorktreeActivity(ctx context.Context, input CreateRemoteWorktreeInput) (CreateRemoteWorktreeOutput, error) {
	worktreePath, err := createRemoteWorktree(ctx, input.EnvContainer, input.RepoDir, input.BranchName, input.StartBranch, input.WorkspaceId, input.LocalRepoDir)
	if err != nil {
		return CreateRemoteWorktreeOutput{}, err
	}
	return CreateRemoteWorktreeOutput{WorktreePath: worktreePath}, nil
}

type DeepenRepoInput struct {
	EnvContainer EnvContainer `json:"envContainer"`
	// LocalRepoDir is the host repository serving the missing history.
	LocalRepoDir string `json:"localRepoDir"`
	// RemoteRepoDir is the environment's repo to deepen.
	RemoteRepoDir string `json:"remoteRepoDir"`
	// Branches lists branches whose full history to backfill besides the
	// local HEAD branch's.
	Branches []string `json:"branches,omitempty"`
}

type DeepenRepoOutput struct {
	// Deepened is false when the remote repo already had complete history.
	Deepened bool `json:"deepened"`
}

func runDeepenWithSSHTransportRecovery(
	ctx context.Context,
	e Env,
	operation func() (wasShallow bool, err error),
) (bool, error) {
	observedShallow := false
	err := RunWithSSHTransportRecovery(ctx, e, func() error {
		wasShallow, err := operation()
		observedShallow = observedShallow || wasShallow
		return err
	})
	return observedShallow, err
}

// DeepenRepoActivity backfills complete history into a shallow-seeded
// remote repo, meant to run in the background once a task is already
// underway. The fetch adds objects and lifts the shallow boundary but
// updates no refs, so it is safe to run concurrently with work happening in
// the environment. Modal sandboxes snapshot immediately after deepening so
// future sandboxes can use the completed history without waiting for idle
// hibernation. No-op for repos with complete history.
func DeepenRepoActivity(ctx context.Context, input DeepenRepoInput) (DeepenRepoOutput, error) {
	sshEnv, ok := input.EnvContainer.Env.(SSHCapableEnv)
	if !ok {
		return DeepenRepoOutput{}, fmt.Errorf("env type %s does not support SSH-based repo deepening", input.EnvContainer.Env.GetType())
	}
	HoldReverseForwards(ctx, sshEnv)

	deepened, err := runDeepenWithSSHTransportRecovery(ctx, input.EnvContainer.Env, func() (bool, error) {
		sshArgs, err := sshEnv.SSHArgs(ctx)
		if err != nil {
			return false, fmt.Errorf("failed to get SSH args: %w", err)
		}

		quotedRepo := shellQuote(input.RemoteRepoDir)
		checkArgs := make([]string, len(sshArgs))
		copy(checkArgs, sshArgs)
		checkArgs = append(checkArgs, fmt.Sprintf("if [ -f %s/.git/shallow ]; then echo SHALLOW; else echo COMPLETE; fi", quotedRepo))
		checkOutput, err := unix.RunCommandActivity(ctx, unix.RunCommandActivityInput{
			WorkingDir: os.TempDir(),
			Command:    "ssh",
			Args:       checkArgs,
		})
		if err != nil {
			return false, fmt.Errorf("failed to check for shallow remote repo: %w", err)
		}
		if checkOutput.ExitStatus != 0 {
			return false, fmt.Errorf("checking for shallow remote repo failed (exit %d): %s", checkOutput.ExitStatus, checkOutput.Stderr)
		}
		if !strings.Contains(checkOutput.Stdout, "SHALLOW") {
			return false, nil
		}

		headRef, err := localHeadRef(ctx, input.LocalRepoDir)
		if err != nil {
			return true, err
		}
		refs := []string{headRef}
		for _, branch := range input.Branches {
			ref := branch
			if !strings.HasPrefix(ref, "refs/") {
				ref = "refs/heads/" + ref
			}
			if !slices.Contains(refs, ref) {
				refs = append(refs, ref)
			}
		}

		deepenStart := time.Now()
		err = tunneledRepoScriptOverSSH(ctx, sshArgs, input.LocalRepoDir, func(url string) string {
			fetch := fmt.Sprintf("git -C %s fetch --unshallow --no-tags %s", quotedRepo, url)
			for _, ref := range refs {
				fetch += " " + shellQuote(ref)
			}
			return fetch
		})
		if err != nil {
			return true, fmt.Errorf("deepening remote repo failed: %w", err)
		}
		log.Debug().
			Str("remoteRepoDir", input.RemoteRepoDir).
			Dur("deepenDur", time.Since(deepenStart)).
			Msg("deepened remote repo history")
		return true, nil
	})
	if err != nil {
		return DeepenRepoOutput{}, err
	}
	return DeepenRepoOutput{Deepened: deepened}, nil
}

type SnapshotEnvironmentInput struct {
	EnvContainer  EnvContainer `json:"envContainer"`
	RemoteRepoDir string       `json:"remoteRepoDir"`
}

type SnapshotEnvironmentOutput struct {
	Snapshotted bool `json:"snapshotted"`
	Attempts    int  `json:"attempts"`
}

// SnapshotEnvironmentActivity checkpoints an environment after repository
// deepening. It remains separate from deepening so a snapshot outage never
// causes completed repository work to run again.
func SnapshotEnvironmentActivity(ctx context.Context, input SnapshotEnvironmentInput) (SnapshotEnvironmentOutput, error) {
	snapshotter, ok := input.EnvContainer.Env.(SnapshottingEnv)
	if !ok {
		return SnapshotEnvironmentOutput{}, nil
	}
	return snapshotEnvironmentAfterDeepening(ctx, snapshotter, input.RemoteRepoDir, 20, 15*time.Second), nil
}

func snapshotEnvironmentAfterDeepening(ctx context.Context, snapshotter SnapshottingEnv, remoteRepoDir string, attempts int, retryDelay time.Duration) SnapshotEnvironmentOutput {
	snapshotStart := time.Now()
	for attempt := 1; attempt <= attempts; attempt++ {
		if activity.IsActivity(ctx) {
			activity.RecordHeartbeat(ctx, attempt)
		}
		snapshotOutput, snapshotErr := snapshotter.Snapshot(ctx)
		event := log.Info().
			Str("envType", string(snapshotter.GetType())).
			Str("remoteRepoDir", remoteRepoDir).
			Dur("snapshotDur", time.Since(snapshotStart)).
			Int("attempt", attempt).
			Int("maxAttempts", attempts).
			Int("exitStatus", snapshotOutput.ExitStatus).
			Str("stdout", strings.TrimSpace(snapshotOutput.Stdout)).
			Str("stderr", strings.TrimSpace(snapshotOutput.Stderr))
		if snapshotErr == nil && snapshotOutput.ExitStatus == 0 {
			event.Msg("captured post-deepening environment snapshot")
			return SnapshotEnvironmentOutput{Snapshotted: true, Attempts: attempt}
		}
		event.Err(snapshotErr).Msg("post-deepening environment snapshot attempt failed")
		if attempt < attempts {
			select {
			case <-ctx.Done():
				log.Warn().
					Err(ctx.Err()).
					Str("envType", string(snapshotter.GetType())).
					Str("remoteRepoDir", remoteRepoDir).
					Msg("post-deepening environment snapshot retries interrupted")
				return SnapshotEnvironmentOutput{Attempts: attempt}
			case <-time.After(retryDelay):
			}
		}
	}

	log.Warn().
		Str("envType", string(snapshotter.GetType())).
		Str("remoteRepoDir", remoteRepoDir).
		Int("attempts", attempts).
		Msg("post-deepening environment snapshot retries exhausted")
	return SnapshotEnvironmentOutput{Attempts: attempts}
}
