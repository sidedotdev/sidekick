package env

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/rs/zerolog/log"
)

const (
	HibernationMetadataFile = ".sidekick-hibernation.json"
	HibernationPatchFile    = ".sidekick-hibernation.patch.tgz"

	defaultHibernationLockTimeout = 15 * time.Minute
	hibernationLockTimeoutEnvVar  = "SIDE_HIBERNATION_LOCK_TIMEOUT"

	// hibernatedRemoteExitCode is the exit status the remote read-lock wrapper
	// uses to signal that the worktree is hibernated. The Go side catches it and
	// retries after waking.
	hibernatedRemoteExitCode = 119
)

// HibernationMetadata stores the state needed to restore a hibernated worktree.
type HibernationMetadata struct {
	BranchName string `json:"branchName"`
	HeadCommit string `json:"headCommit"`
	RepoDir    string `json:"repoDir"`
}

func hibernationLockTimeout() time.Duration {
	if s := os.Getenv(hibernationLockTimeoutEnvVar); s != "" {
		if d, err := time.ParseDuration(s); err == nil {
			return d
		}
		if secs, err := strconv.Atoi(s); err == nil {
			return time.Duration(secs) * time.Second
		}
	}
	return defaultHibernationLockTimeout
}

// hibernationLockFile returns a per-worktree lock file path in /tmp derived from
// the worktree's working directory. It lives outside the worktree so it survives
// the directory being cleared during hibernate/wake, and it is keyed per
// worktree so unrelated worktrees on the same host (or remote container) never
// contend with one another.
func hibernationLockFile(workDir string) string {
	h := sha256.Sum256([]byte(workDir))
	return fmt.Sprintf("/tmp/.sidekick-hib-%x.lock", h[:8])
}

func isLocalEnv(e Env) bool {
	switch e.(type) {
	case *LocalEnv, *LocalGitWorktreeEnv:
		return true
	default:
		return false
	}
}

// EnvWithDir creates a new Env of the same concrete type but pointing at a
// different working directory.
func EnvWithDir(original Env, dir string) Env {
	switch e := original.(type) {
	case *LocalEnv:
		return &LocalEnv{WorkingDirectory: dir, Hibernated: e.Hibernated}
	case *LocalGitWorktreeEnv:
		return &LocalGitWorktreeEnv{WorkingDirectory: dir, Hibernated: e.Hibernated}
	case *DevPodEnv:
		return &DevPodEnv{WorkingDirectory: dir, WorkspaceName: e.WorkspaceName, LocalRepoDir: e.LocalRepoDir, Hibernated: e.Hibernated}
	case *OpenShellEnv:
		return &OpenShellEnv{WorkingDirectory: dir, SandboxName: e.SandboxName, LocalRepoDir: e.LocalRepoDir, Hibernated: e.Hibernated}
	case *ModalEnv:
		return &ModalEnv{WorkingDirectory: dir, SandboxName: e.SandboxName, SSHHost: e.SSHHost, SSHPort: e.SSHPort, LocalRepoDir: e.LocalRepoDir, PortForwards: e.PortForwards, Hibernated: e.Hibernated}
	default:
		return &LocalEnv{WorkingDirectory: dir}
	}
}

func runSkipWake(ctx context.Context, e Env, input EnvRunCommandInput) (EnvRunCommandOutput, error) {
	input.SkipWaking = true
	return e.RunCommand(ctx, input)
}

func setEnvHibernated(e Env, val bool) {
	switch et := e.(type) {
	case *LocalEnv:
		et.Hibernated = val
	case *LocalGitWorktreeEnv:
		et.Hibernated = val
	case *DevPodEnv:
		et.Hibernated = val
	case *OpenShellEnv:
		et.Hibernated = val
	case *ModalEnv:
		et.Hibernated = val
	}
}

// -- Locking --
//
// Concurrency between hibernate/wake (writers) and normal command execution
// (readers) is coordinated with flock, keyed per worktree. Local envs hold the
// lock fd directly in Go; remote envs use flock(1) in the command shell.
//
// TODO: fall back gracefully when flock(1) is unavailable in a remote container
// (currently we assume util-linux flock is present).

// acquireFileLock opens (creating if needed) lockPath and blocks until it can
// take the requested flock mode, honoring ctx and the hibernation lock timeout.
func acquireFileLock(ctx context.Context, lockPath string, how int) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(lockPath), 0755); err != nil {
		return nil, fmt.Errorf("create hibernation lock dir: %w", err)
	}
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return nil, fmt.Errorf("open hibernation lock: %w", err)
	}
	deadline := time.Now().Add(hibernationLockTimeout())
	for {
		lockErr := syscall.Flock(int(f.Fd()), how|syscall.LOCK_NB)
		if lockErr == nil {
			return f, nil
		}
		if !errors.Is(lockErr, syscall.EWOULDBLOCK) {
			f.Close()
			return nil, fmt.Errorf("flock hibernation lock: %w", lockErr)
		}
		if time.Now().After(deadline) {
			f.Close()
			return nil, fmt.Errorf("timed out acquiring hibernation lock after %v", hibernationLockTimeout())
		}
		select {
		case <-ctx.Done():
			f.Close()
			return nil, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func releaseFileLock(f *os.File) func() {
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}
}

// acquireLocalReadLock takes a shared lock on the worktree for the duration of a
// local read/command, returning a release function.
func acquireLocalReadLock(ctx context.Context, workDir string) (func(), error) {
	f, err := acquireFileLock(ctx, hibernationLockFile(workDir), syscall.LOCK_SH)
	if err != nil {
		return nil, err
	}
	return releaseFileLock(f), nil
}

// acquireLocalReadLockWithWake takes a shared lock and, if the worktree turns
// out to be hibernated, releases it, wakes the worktree (under an exclusive
// lock), and retries. This closes the race between checking hibernation and
// acquiring the lock. For use by local Env types only.
func acquireLocalReadLockWithWake(ctx context.Context, e Env) (func(), error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		release, err := acquireLocalReadLock(ctx, e.GetWorkingDirectory())
		if err != nil {
			return nil, err
		}
		if !IsHibernated(e.GetWorkingDirectory()) {
			return release, nil
		}
		release()
		if _, err := WakeHibernatedEnv(ctx, e); err != nil {
			return nil, err
		}
	}
}

// acquireHibernationWriteLock takes an exclusive lock for a hibernate/wake
// operation. For local envs the lock fd is held for the whole operation. For
// remote envs it is a no-op because a lock fd cannot be held across separate
// SSH invocations; the individual destructive/constructive steps self-serialize
// via `flock -x` (see runExclusiveScript) and the operation is idempotent.
func acquireHibernationWriteLock(ctx context.Context, e Env) (func(), error) {
	if !isLocalEnv(e) {
		return func() {}, nil
	}
	f, err := acquireFileLock(ctx, hibernationLockFile(e.GetWorkingDirectory()), syscall.LOCK_EX)
	if err != nil {
		return nil, err
	}
	return releaseFileLock(f), nil
}

// runExclusiveScript runs a shell script that mutates the worktree tree. For
// local envs the caller already holds the exclusive lock, so it runs plainly.
// For remote envs it wraps the script in `flock -x` so concurrent read commands
// (which take `flock -s`) never observe a half-torn-down/half-rebuilt tree.
func runExclusiveScript(ctx context.Context, e Env, script string) (EnvRunCommandOutput, error) {
	if isLocalEnv(e) {
		return runSkipWake(ctx, e, EnvRunCommandInput{Command: "sh", Args: []string{"-c", script}})
	}
	lock := hibernationLockFile(e.GetWorkingDirectory())
	return runSkipWake(ctx, e, EnvRunCommandInput{Command: "flock", Args: []string{"-x", lock, "sh", "-c", script}})
}

// wrapRemoteReadLock wraps a remote command so it runs under a per-worktree
// shared flock and bails with hibernatedRemoteExitCode when the worktree is
// hibernated, letting the Go side wake and retry. workDir must be the worktree
// root (where the hibernation sentinel lives), not a relative subdirectory.
func wrapRemoteReadLock(workDir, inner string) string {
	lock := hibernationLockFile(workDir)
	meta := workDir + "/" + HibernationMetadataFile
	body := fmt.Sprintf("test -f %s && exit %d; %s", shellQuote(meta), hibernatedRemoteExitCode, inner)
	return fmt.Sprintf("flock -s %s sh -c %s", shellQuote(lock), shellQuote(body))
}

// isHibernatedExitError reports whether err is a command that exited with
// hibernatedRemoteExitCode.
func isHibernatedExitError(err error) bool {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode() == hibernatedRemoteExitCode
	}
	return false
}

// IsHibernated reports whether the given local directory contains the
// hibernation sentinel.
func IsHibernated(workDir string) bool {
	_, err := os.Stat(filepath.Join(workDir, HibernationMetadataFile))
	return err == nil
}

// WakeHibernatedEnv restores a hibernated worktree to the filesystem state it
// had before hibernation, to the best of our ability. It uses only
// Env.RunCommand (with SkipWaking=true) and has no dependency on Temporal.
//
// The operation is idempotent and interruption-safe: the sentinel files remain
// in the worktree directory throughout and are only removed as the final step,
// so retrying after any interruption works without special recovery.
//
// Returns the hibernation metadata (empty if not hibernated).
//
// Caveats:
//   - If the branch advanced during hibernation, the worktree stays at detached HEAD.
//   - Untracked files that are git-ignored are not captured (they are treated as
//     regenerable), so they are not restored.
//   - Untracked binary files are not captured either (also treated as regenerable,
//     to keep the archive small); untracked text files and all tracked/staged
//     changes, including staged binary changes, are restored.
//   - Empty untracked directories are not recreated (git does not track them).
func WakeHibernatedEnv(ctx context.Context, e Env) (HibernationMetadata, error) {
	workDir := e.GetWorkingDirectory()

	release, err := acquireHibernationWriteLock(ctx, e)
	if err != nil {
		return HibernationMetadata{}, err
	}
	defer release()

	// Re-check under the lock; another actor may have already woken it.
	catResult, err := runSkipWake(ctx, e, EnvRunCommandInput{
		Command: "cat",
		Args:    []string{HibernationMetadataFile},
	})
	if err != nil || catResult.ExitStatus != 0 {
		return HibernationMetadata{}, nil
	}

	var metadata HibernationMetadata
	if err := json.Unmarshal([]byte(catResult.Stdout), &metadata); err != nil {
		return HibernationMetadata{}, fmt.Errorf("failed to parse hibernation metadata: %w", err)
	}

	// Normal path: hibernation keeps the .git link, so the worktree is still
	// registered and we restore in place. This also covers the interrupted cases
	// (hibernate or a prior wake interrupted mid-flight), since the restore below
	// is fully idempotent.
	gitCheck, _ := runSkipWake(ctx, e, EnvRunCommandInput{
		Command: "test",
		Args:    []string{"-e", ".git"},
	})
	if gitCheck.ExitStatus == 0 {
		repoEnv := EnvWithDir(e, metadata.RepoDir)
		_, _ = runSkipWake(ctx, repoEnv, EnvRunCommandInput{
			Command: "git",
			Args:    []string{"worktree", "repair", workDir},
		})

		// Restore in place as a single flock-guarded script (see doc above). We
		// detach at the exact hibernated commit instead of resetting to the HEAD
		// symref: the retained .git keeps the worktree on its branch, so if that
		// branch was force-moved during hibernation the symref would otherwise
		// follow it (and resetting the symref could clobber the branch). A
		// whole-tree checkout also clears the retained index so the captured staged
		// changes re-apply cleanly; reattachBranchFragment re-points HEAD at the
		// branch only when it still matches the hibernated commit.
		wakeScript := fmt.Sprintf(`set -e
git checkout -q -f --detach %s
git clean -fdx -e %s -e %s
%s
%s
rm -f %s %s
`,
			shellQuote(metadata.HeadCommit),
			shellQuote(HibernationMetadataFile), shellQuote(HibernationPatchFile),
			reattachBranchFragment(metadata.RepoDir, metadata.BranchName, metadata.HeadCommit),
			restoreFilesFragment(),
			shellQuote(HibernationMetadataFile), shellQuote(HibernationPatchFile))
		if err := runWakeScript(ctx, e, wakeScript); err != nil {
			return HibernationMetadata{}, err
		}
		setEnvHibernated(e, false)
		log.Info().Str("workDir", workDir).Str("branch", metadata.BranchName).Msg("Woke hibernated worktree")
		return metadata, nil
	}

	// Legacy path: worktrees hibernated by an older build that unlinked .git have
	// no live registration, so recreate one in a temp sibling directory (the
	// sentinel files in workDir stay untouched until the final commit point).
	repoEnv := EnvWithDir(e, metadata.RepoDir)
	tmpWorktreeScript := fmt.Sprintf(`set -e
parent=$(dirname %s)
tmpwt=$(mktemp -d "$parent/.sidekick-wake-XXXXXX")
printf '%%s' "$tmpwt"
`, shellQuote(workDir))
	tmpResult, err := runSkipWake(ctx, repoEnv, EnvRunCommandInput{
		Command: "sh",
		Args:    []string{"-c", tmpWorktreeScript},
	})
	if err != nil {
		return HibernationMetadata{}, fmt.Errorf("failed to create temp dir for wake: %w", err)
	}
	if tmpResult.ExitStatus != 0 {
		return HibernationMetadata{}, fmt.Errorf("failed to create temp dir for wake: %s", tmpResult.Stderr)
	}
	tmpWt := strings.TrimSpace(tmpResult.Stdout)

	cleanupTmp := func() {
		_, _ = runSkipWake(ctx, repoEnv, EnvRunCommandInput{
			Command: "git",
			Args:    []string{"worktree", "remove", "--force", tmpWt},
		})
		_, _ = runSkipWake(ctx, repoEnv, EnvRunCommandInput{
			Command: "rm",
			Args:    []string{"-rf", tmpWt},
		})
	}

	addResult, err := runSkipWake(ctx, repoEnv, EnvRunCommandInput{
		Command: "git",
		Args:    []string{"worktree", "add", "--detach", tmpWt, metadata.HeadCommit},
	})
	if err != nil {
		cleanupTmp()
		return HibernationMetadata{}, fmt.Errorf("failed to recreate worktree: %w", err)
	}
	if addResult.ExitStatus != 0 {
		cleanupTmp()
		return HibernationMetadata{}, fmt.Errorf("failed to recreate worktree: %s", addResult.Stderr)
	}

	// Reconstruct the worktree as one flock-guarded script (see doc above):
	// clear workDir (keeping sentinels), move .git in from the temp worktree,
	// repair the worktree links, populate HEAD, reattach the branch, restore the
	// captured changes, and finally drop the sentinels. Running it as a single
	// script keeps wake atomic against concurrent hibernate/wake/read commands,
	// including on remote envs. The temp-worktree creation above is a main-repo
	// operation that never touches workDir, so it is safe outside this lock.
	wakeScript := fmt.Sprintf(`set -e
cd %s
for f in .* *; do
  case "$f" in
    .|..|%s|%s) continue ;;
    *) rm -rf "$f" ;;
  esac
done
mv %s/.git %s/.git
rm -rf %s
git -C %s worktree repair %s || true
git checkout -f HEAD -- .
%s
%s
rm -f %s %s
`,
		shellQuote(workDir),
		HibernationMetadataFile, HibernationPatchFile,
		shellQuote(tmpWt), shellQuote(workDir),
		shellQuote(tmpWt),
		shellQuote(metadata.RepoDir), shellQuote(workDir),
		reattachBranchFragment(metadata.RepoDir, metadata.BranchName, metadata.HeadCommit),
		restoreFilesFragment(),
		shellQuote(HibernationMetadataFile), shellQuote(HibernationPatchFile))
	if err := runWakeScript(ctx, e, wakeScript); err != nil {
		cleanupTmp()
		return HibernationMetadata{}, err
	}

	// DO NOT run `git worktree prune` (or any repo-wide worktree GC) here or
	// anywhere in hibernate/wake. Prune deletes the admin entry of EVERY worktree
	// whose directory is unreachable from the current process. In our mixed
	// host/devcontainer setups a worktree living inside the devcontainer looks
	// "gone" from the host (and vice versa), so a single prune silently destroys
	// valid worktree metadata for all of them. The wake script already repaired
	// this worktree's admin entry onto workDir, so there is nothing to clean up.

	setEnvHibernated(e, false)
	log.Info().Str("workDir", workDir).Str("branch", metadata.BranchName).Msg("Woke hibernated worktree")
	return metadata, nil
}

// runWakeScript runs a worktree-reconstruction script under an exclusive lock so
// the entire wake is atomic against concurrent hibernate/wake/read commands,
// including on remote envs where a lock fd cannot be held across SSH round-trips.
func runWakeScript(ctx context.Context, e Env, script string) error {
	result, err := runExclusiveScript(ctx, e, script)
	if err != nil {
		return fmt.Errorf("failed to wake worktree: %w", err)
	}
	if result.ExitStatus != 0 {
		return fmt.Errorf("failed to wake worktree: %s", result.Stderr)
	}
	return nil
}

// reattachBranchFragment returns shell that checks out the hibernated branch in
// the current directory only if it still points at the hibernated commit;
// otherwise the worktree is left at detached HEAD.
func reattachBranchFragment(repoDir, branch, head string) string {
	return fmt.Sprintf(`if [ "$(git -C %s rev-parse %s 2>/dev/null)" = %s ]; then
  git checkout %s 2>/dev/null || true
fi`, shellQuote(repoDir), shellQuote(branch), shellQuote(head), shellQuote(branch))
}

// restoreFilesFragment returns shell that overlays the captured working-tree
// changes onto a clean HEAD checkout: it untars changed/untracked file contents
// (preserving binary content, symlinks, and permissions), removes files that
// were deleted, and re-applies the staged (index) state, including staged binary
// changes carried by the --binary patch.
//
// deleted.list is newline-delimited as written by HibernateEnv; the tr step also
// normalizes NUL separators so lists written by an earlier build (which used
// git's -z output) still apply. Paths containing literal newlines are not
// supported, a documented best-effort caveat.
func restoreFilesFragment() string {
	return fmt.Sprintf(`if [ -f %s ]; then
  rtmp=$(mktemp -d)
  tar xzf %s -C "$rtmp"
  if [ -s "$rtmp/changed.tgz" ]; then tar xzf "$rtmp/changed.tgz"; fi
  if [ -s "$rtmp/deleted.list" ]; then tr '\000' '\012' < "$rtmp/deleted.list" | while IFS= read -r d; do if [ -n "$d" ]; then rm -f -- "$d"; fi; done; fi
  if [ -s "$rtmp/staged.patch" ]; then git apply --cached --binary --allow-empty --whitespace=nowarn "$rtmp/staged.patch"; fi
  rm -rf "$rtmp"
fi`, shellQuote(HibernationPatchFile), shellQuote(HibernationPatchFile))
}

// HibernateEnv captures the current worktree state as a compressed archive and
// deletes all working files, leaving only the hibernation artifacts on disk. The
// worktree stays registered with git: the tiny .git link is retained so the
// worktree remains valid and its branch stays protected, which lets wake be a
// pure in-place restore with no worktree recreation and no admin-entry churn. It
// is idempotent: re-hibernating an already-hibernated worktree returns the
// existing metadata. No mutations happen before the sentinels are written, so an
// interruption at any point is safe to retry.
func HibernateEnv(ctx context.Context, e Env, branchName string) (HibernationMetadata, error) {
	if branchName == "" {
		return HibernationMetadata{}, fmt.Errorf("branch name is required for hibernation")
	}

	release, err := acquireHibernationWriteLock(ctx, e)
	if err != nil {
		return HibernationMetadata{}, err
	}
	defer release()

	catResult, catErr := runSkipWake(ctx, e, EnvRunCommandInput{
		Command: "cat",
		Args:    []string{HibernationMetadataFile},
	})
	if catErr == nil && catResult.ExitStatus == 0 {
		var existing HibernationMetadata
		if err := json.Unmarshal([]byte(catResult.Stdout), &existing); err == nil {
			return existing, nil
		}
	}

	workDir := e.GetWorkingDirectory()

	headResult, err := runSkipWake(ctx, e, EnvRunCommandInput{
		Command: "git",
		Args:    []string{"rev-parse", "HEAD"},
	})
	if err != nil {
		return HibernationMetadata{}, fmt.Errorf("failed to get HEAD commit: %w", err)
	}
	if headResult.ExitStatus != 0 {
		return HibernationMetadata{}, fmt.Errorf("failed to get HEAD commit: %s", headResult.Stderr)
	}
	headCommit := strings.TrimSpace(headResult.Stdout)

	wtListResult, err := runSkipWake(ctx, e, EnvRunCommandInput{
		Command: "sh",
		Args:    []string{"-c", `git worktree list --porcelain | head -1 | sed 's/^worktree //'`},
	})
	if err != nil {
		return HibernationMetadata{}, fmt.Errorf("failed to get main worktree path: %w", err)
	}
	if wtListResult.ExitStatus != 0 || strings.TrimSpace(wtListResult.Stdout) == "" {
		return HibernationMetadata{}, fmt.Errorf("failed to get main worktree path: %s", wtListResult.Stderr)
	}
	repoDir := strings.TrimSpace(wtListResult.Stdout)
	repoEnv := EnvWithDir(e, repoDir)
	resolveResult, err := runSkipWake(ctx, repoEnv, EnvRunCommandInput{
		Command: "sh",
		Args:    []string{"-c", "pwd -P"},
	})
	if err == nil && resolveResult.ExitStatus == 0 {
		repoDir = strings.TrimSpace(resolveResult.Stdout)
	}

	tmpPatch := HibernationPatchFile + ".tmp"

	metadata := HibernationMetadata{
		BranchName: branchName,
		HeadCommit: headCommit,
		RepoDir:    repoDir,
	}
	metadataBytes, err := json.Marshal(metadata)
	if err != nil {
		return HibernationMetadata{}, fmt.Errorf("failed to marshal metadata: %w", err)
	}

	// Everything that mutates the worktree runs as a single flock-guarded
	// script: capture the working-tree state, write the sentinels (the atomic
	// commit point), and delete every working file except the retained .git link
	// that keeps the worktree registered. Running it as one script keeps hibernation atomic against
	// concurrent wake/read commands even on remote envs, where a lock fd cannot
	// be held across separate SSH round-trips. Untracked binary files are
	// treated as regenerable and skipped to keep the archive small; the staged
	// index (including staged binary changes) is preserved via a --binary patch.
	hibernateScript := fmt.Sprintf(`set -e
tmpdir=$(mktemp -d)
# Newline-delimited file lists (busybox tar has no --null); core.quotePath=false
# keeps unicode paths literal so tar -T can open them.
git -c core.quotePath=false diff --name-only --diff-filter=D HEAD > "$tmpdir/deleted.list"
git -c core.quotePath=false diff --name-only --diff-filter=d HEAD > "$tmpdir/changed.list"
git -c core.quotePath=false ls-files --others --exclude-standard | while IFS= read -r f; do
  ns=$(git diff --no-index --numstat -- /dev/null "$f" 2>/dev/null | head -n1)
  case "$ns" in
    -*) ;;
    *) printf '%%s\n' "$f" ;;
  esac
done >> "$tmpdir/changed.list"
if [ -s "$tmpdir/changed.list" ]; then
  tar czf "$tmpdir/changed.tgz" -T "$tmpdir/changed.list"
else
  : > "$tmpdir/changed.tgz"
fi
git diff --cached --binary > "$tmpdir/staged.patch"
tar czf %s -C "$tmpdir" changed.tgz deleted.list staged.patch
rm -rf "$tmpdir"
mv %s %s
cat > %s <<'SIDEKICK_HIB_EOF'
%s
SIDEKICK_HIB_EOF
find . -mindepth 1 ! -name .git ! -name %s ! -name %s -depth -exec rm -rf {} + 2>/dev/null || true
remaining=$(find . -mindepth 1 ! -name .git ! -name %s ! -name %s | head -1)
if [ -n "$remaining" ]; then echo "LEFTOVER"; else echo "CLEAN"; fi
`,
		shellQuote(tmpPatch),
		shellQuote(tmpPatch), shellQuote(HibernationPatchFile),
		shellQuote(HibernationMetadataFile),
		string(metadataBytes),
		shellQuote(HibernationMetadataFile), shellQuote(HibernationPatchFile),
		shellQuote(HibernationMetadataFile), shellQuote(HibernationPatchFile))
	hibernateResult, err := runExclusiveScript(ctx, e, hibernateScript)
	if err != nil {
		return HibernationMetadata{}, fmt.Errorf("failed to hibernate worktree: %w", err)
	}
	if hibernateResult.ExitStatus != 0 {
		return HibernationMetadata{}, fmt.Errorf("failed to hibernate worktree: %s", hibernateResult.Stderr)
	}
	if strings.TrimSpace(hibernateResult.Stdout) == "LEFTOVER" {
		return HibernationMetadata{}, fmt.Errorf("failed to reduce worktree to hibernation files only in %s", workDir)
	}

	// The worktree stays registered with git (its .git link is retained above), so
	// there is no dangling admin entry to clean up here. Keeping it registered is
	// deliberate: it protects the branch and lets wake restore in place without
	// recreating the worktree. NEVER run `git worktree prune` in hibernate/wake —
	// it deletes the admin entry of every worktree unreachable from the current
	// process, which is catastrophic in mixed host/devcontainer setups.

	setEnvHibernated(e, true)
	log.Info().Str("workDir", workDir).Str("branch", branchName).Msg("Hibernated worktree")
	return metadata, nil
}

// wakeIfHibernatedLocal wakes the worktree if it is hibernated, using a fast
// os.Stat check. For local Env types only.
func wakeIfHibernatedLocal(ctx context.Context, e Env) error {
	if !IsHibernated(e.GetWorkingDirectory()) {
		return nil
	}
	_, err := WakeHibernatedEnv(ctx, e)
	return err
}

// wakeIfHibernatedRemote wakes the worktree if it is hibernated, using a remote
// shell check. For remote Env types (DevPod, OpenShell, Modal).
func wakeIfHibernatedRemote(ctx context.Context, e Env) error {
	result, err := runSkipWake(ctx, e, EnvRunCommandInput{
		Command: "test",
		Args:    []string{"-f", HibernationMetadataFile},
	})
	if err != nil || result.ExitStatus != 0 {
		return nil
	}
	_, err = WakeHibernatedEnv(ctx, e)
	return err
}

// -- Hibernate/Wake methods on concrete Env types --

func (e *LocalEnv) Hibernate(ctx context.Context, branchName string) (HibernationMetadata, error) {
	return HibernateEnv(ctx, e, branchName)
}

func (e *LocalEnv) WakeIfHibernated(ctx context.Context) error {
	return wakeIfHibernatedLocal(ctx, e)
}

func (e *LocalGitWorktreeEnv) Hibernate(ctx context.Context, branchName string) (HibernationMetadata, error) {
	return HibernateEnv(ctx, e, branchName)
}

func (e *LocalGitWorktreeEnv) WakeIfHibernated(ctx context.Context) error {
	return wakeIfHibernatedLocal(ctx, e)
}

func (e *DevPodEnv) Hibernate(ctx context.Context, branchName string) (HibernationMetadata, error) {
	return HibernateEnv(ctx, e, branchName)
}

func (e *DevPodEnv) WakeIfHibernated(ctx context.Context) error {
	return wakeIfHibernatedRemote(ctx, e)
}

func (e *OpenShellEnv) Hibernate(ctx context.Context, branchName string) (HibernationMetadata, error) {
	return HibernateEnv(ctx, e, branchName)
}

func (e *OpenShellEnv) WakeIfHibernated(ctx context.Context) error {
	return wakeIfHibernatedRemote(ctx, e)
}

func (e *ModalEnv) Hibernate(ctx context.Context, branchName string) (HibernationMetadata, error) {
	return HibernateEnv(ctx, e, branchName)
}

func (e *ModalEnv) WakeIfHibernated(ctx context.Context) error {
	return wakeIfHibernatedRemote(ctx, e)
}
