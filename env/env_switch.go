package env

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"strings"

	"github.com/rs/zerolog/log"
	"go.temporal.io/sdk/workflow"
)

// SwitchBundle carries a flow's working state out of one env so an env of
// another type can be provisioned from it. Everything it references is on the
// local host: the branch's commits live in LocalRepoDir and the WIP archive is
// a local file, so the source env is not needed once export completes.
type SwitchBundle struct {
	Branch       string `json:"branch"`
	HeadCommit   string `json:"headCommit"`
	LocalRepoDir string `json:"localRepoDir"`
	// WIPArchivePath is a local archive of uncommitted and untracked text
	// changes in the hibernation patch layout; empty when the tree was clean.
	WIPArchivePath string `json:"wipArchivePath,omitempty"`
}

// SwitchableEnv is implemented by envs whose flow state can be moved to an env
// of another type.
type SwitchableEnv interface {
	Env
	// ExportForSwitch makes the branch's commits available in the local repo
	// and captures uncommitted work without committing or otherwise mutating
	// the env, so it stays usable if the switch fails.
	ExportForSwitch(ctx context.Context, branch string) (SwitchBundle, error)
	// RestoreFromSwitch moves the checked-out branch to the bundle's head
	// commit and overlays the bundle's uncommitted work.
	RestoreFromSwitch(ctx context.Context, bundle SwitchBundle) error
}

var (
	_ SwitchableEnv = (*LocalEnv)(nil)
	_ SwitchableEnv = (*LocalGitWorktreeEnv)(nil)
	_ SwitchableEnv = (*DevPodEnv)(nil)
	_ SwitchableEnv = (*OpenShellEnv)(nil)
	_ SwitchableEnv = (*ModalEnv)(nil)
)

// EnvIdentity recognizes an env across value copies and config-only changes,
// e.g. a container captured before a sandbox was recreated or switched away.
type EnvIdentity struct {
	EnvType          EnvType `json:"envType"`
	WorkingDirectory string  `json:"workingDirectory"`
}

func IdentityOf(c EnvContainer) EnvIdentity {
	if c.Env == nil {
		return EnvIdentity{}
	}
	return EnvIdentity{EnvType: c.Env.GetType(), WorkingDirectory: c.Env.GetWorkingDirectory()}
}

// EnvInstance distinguishes the physical envs behind one EnvIdentity: a
// sandbox replaced by a successor restored from its snapshot keeps the
// identity (so stale references still resolve to the flow env) but is a
// different sandbox that must be cleaned up on its own.
type EnvInstance struct {
	EnvIdentity
	SandboxName string `json:"sandboxName,omitempty"`
}

func InstanceOf(c EnvContainer) EnvInstance {
	instance := EnvInstance{EnvIdentity: IdentityOf(c)}
	if sandbox, ok := c.Env.(SandboxEnv); ok {
		instance.SandboxName = sandbox.GetSandboxName()
	}
	return instance
}

// EnvTracker exposes a flow's env switching state to the activity gate
// installed by NewEnvSwitchGateInterceptor.
type EnvTracker interface {
	// SwitchInProgress reports whether env-carrying activities must wait.
	SwitchInProgress() bool
	// Resolve returns the env an activity scheduled with c must run on.
	Resolve(c EnvContainer) EnvContainer
}

type envTrackerKey struct{}
type pinnedEnvKey struct{}
type switchBypassKey struct{}

// WithEnvTracker subjects env-carrying activities scheduled from ctx to the
// tracker's switch gate.
func WithEnvTracker(ctx workflow.Context, tracker EnvTracker) workflow.Context {
	return workflow.WithValue(ctx, envTrackerKey{}, tracker)
}

func EnvTrackerFromContext(ctx workflow.Context) EnvTracker {
	tracker, _ := ctx.Value(envTrackerKey{}).(EnvTracker)
	return tracker
}

// WithPinnedEnv marks activities scheduled from ctx as intentionally targeting
// the env they are given, typically the developer's local repo rather than the
// flow env, so they are neither held during a switch nor rewritten after one.
// This matters even when the values match: an in-place local flow env is
// indistinguishable from the local repo env until the flow switches away.
func WithPinnedEnv(ctx workflow.Context) workflow.Context {
	return workflow.WithValue(ctx, pinnedEnvKey{}, true)
}

func IsPinnedEnv(ctx workflow.Context) bool {
	pinned, _ := ctx.Value(pinnedEnvKey{}).(bool)
	return pinned
}

// WithSwitchBypass marks activities scheduled from ctx as part of performing a
// switch, which must reach the source and target envs while the gate is
// holding everything else.
func WithSwitchBypass(ctx workflow.Context) workflow.Context {
	return workflow.WithValue(ctx, switchBypassKey{}, true)
}

func IsSwitchBypass(ctx workflow.Context) bool {
	bypass, _ := ctx.Value(switchBypassKey{}).(bool)
	return bypass
}

func (e *LocalEnv) ExportForSwitch(ctx context.Context, branch string) (SwitchBundle, error) {
	repoDir, err := localRepoRoot(ctx, e)
	if err != nil {
		return SwitchBundle{}, err
	}
	return exportForSwitch(ctx, e, branch, repoDir)
}

func (e *LocalGitWorktreeEnv) ExportForSwitch(ctx context.Context, branch string) (SwitchBundle, error) {
	repoDir, err := localRepoRoot(ctx, e)
	if err != nil {
		return SwitchBundle{}, err
	}
	return exportForSwitch(ctx, e, branch, repoDir)
}

// DevPod workspaces share the host checkout's git directory, so the flow
// branch's commits already live in LocalRepoDir and need no syncing.
func (e *DevPodEnv) ExportForSwitch(ctx context.Context, branch string) (SwitchBundle, error) {
	return exportForSwitch(ctx, e, branch, e.LocalRepoDir)
}

func (e *OpenShellEnv) ExportForSwitch(ctx context.Context, branch string) (SwitchBundle, error) {
	return exportForSwitch(ctx, e, branch, e.LocalRepoDir)
}

func (e *ModalEnv) ExportForSwitch(ctx context.Context, branch string) (SwitchBundle, error) {
	return exportForSwitch(ctx, e, branch, e.LocalRepoDir)
}

func (e *LocalEnv) RestoreFromSwitch(ctx context.Context, bundle SwitchBundle) error {
	return restoreFromSwitch(ctx, e, bundle)
}

func (e *LocalGitWorktreeEnv) RestoreFromSwitch(ctx context.Context, bundle SwitchBundle) error {
	return restoreFromSwitch(ctx, e, bundle)
}

func (e *DevPodEnv) RestoreFromSwitch(ctx context.Context, bundle SwitchBundle) error {
	return restoreFromSwitch(ctx, e, bundle)
}

func (e *OpenShellEnv) RestoreFromSwitch(ctx context.Context, bundle SwitchBundle) error {
	return restoreFromSwitch(ctx, e, bundle)
}

func (e *ModalEnv) RestoreFromSwitch(ctx context.Context, bundle SwitchBundle) error {
	return restoreFromSwitch(ctx, e, bundle)
}

// localRepoRoot resolves the main checkout of the repo that e's working
// directory belongs to, which differs from it for linked worktrees.
func localRepoRoot(ctx context.Context, e Env) (string, error) {
	out, err := e.RunCommand(ctx, EnvRunCommandInput{
		Command: "sh",
		Args:    []string{"-c", `set -e; common=$(git rev-parse --git-common-dir); cd "$common/.."; pwd -P`},
	})
	if err != nil {
		return "", fmt.Errorf("failed to resolve local repo dir: %w", err)
	}
	if out.ExitStatus != 0 {
		return "", fmt.Errorf("failed to resolve local repo dir (exit %d): %s", out.ExitStatus, strings.TrimSpace(out.Stderr))
	}
	return strings.TrimSpace(out.Stdout), nil
}

func exportForSwitch(ctx context.Context, e Env, branch, localRepoDir string) (SwitchBundle, error) {
	if branch == "" {
		return SwitchBundle{}, fmt.Errorf("branch is required to export for switch")
	}
	if localRepoDir == "" {
		return SwitchBundle{}, fmt.Errorf("cannot export %s env for switch: it has no local repo dir", e.GetType())
	}

	if snapshotter, ok := e.(SnapshottingEnv); ok {
		out, err := snapshotter.Snapshot(ctx)
		if errors.Is(err, errModalSandboxNotRunning) {
			if ctx.Err() != nil {
				return SwitchBundle{}, ctx.Err()
			}
			// There is no live state to checkpoint. Reading the flow state
			// below restores the last snapshot through command recovery,
			// which fails if no snapshot is available.
			log.Info().Str("envType", string(e.GetType())).
				Msg("skipping pre-switch snapshot of stopped Modal sandbox")
		} else if err != nil {
			return SwitchBundle{}, fmt.Errorf("failed to snapshot env before switch: %w", err)
		} else if out.ExitStatus != 0 {
			return SwitchBundle{}, fmt.Errorf("failed to snapshot env before switch (exit %d): %s", out.ExitStatus, strings.TrimSpace(out.Stderr))
		}
	}

	checkedOut, err := gitOutputForSwitch(ctx, e, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return SwitchBundle{}, err
	}
	if checkedOut != branch {
		return SwitchBundle{}, fmt.Errorf("cannot export for switch: env has %q checked out, expected branch %q", checkedOut, branch)
	}
	head, err := gitOutputForSwitch(ctx, e, "rev-parse", "HEAD")
	if err != nil {
		return SwitchBundle{}, err
	}

	if syncer, ok := e.(FlowBranchBackupSyncer); ok {
		if err := syncer.SyncFlowBranchToLocal(ctx, branch); err != nil {
			return SwitchBundle{}, fmt.Errorf("failed to sync flow branch %s to local repo: %w", branch, err)
		}
	}

	wipArchivePath, err := captureWIPToLocalFile(ctx, e)
	if err != nil {
		return SwitchBundle{}, err
	}
	return SwitchBundle{
		Branch:         branch,
		HeadCommit:     head,
		LocalRepoDir:   localRepoDir,
		WIPArchivePath: wipArchivePath,
	}, nil
}

const switchWIPArchiveName = "wip.tgz"

// captureWIPToLocalFile archives e's uncommitted work inside e and copies the
// archive to a local temp file, returning "" when there is nothing to carry.
// The archive is built outside the worktree so the worktree is only read.
func captureWIPToLocalFile(ctx context.Context, e Env) (string, error) {
	script := "set -e\nwipdir=$(mktemp -d)\n" +
		captureWIPFragment(`"$wipdir/`+switchWIPArchiveName+`"`) +
		`if [ "$wip_dirty" = 1 ]; then printf '%s\n' "$wipdir/` + switchWIPArchiveName + `"; else rm -rf "$wipdir"; fi` + "\n"
	out, err := e.RunCommand(ctx, EnvRunCommandInput{
		Command: "sh",
		Args:    []string{"-c", script},
		// BSD tar's AppleDouble entries become untracked files on Linux.
		EnvVars: []string{"COPYFILE_DISABLE=1"},
	})
	if err != nil {
		return "", fmt.Errorf("failed to capture uncommitted changes: %w", err)
	}
	if out.ExitStatus != 0 {
		return "", fmt.Errorf("failed to capture uncommitted changes (exit %d): %s", out.ExitStatus, strings.TrimSpace(out.Stderr))
	}

	envArchivePath := strings.TrimSpace(out.Stdout)
	if envArchivePath == "" {
		return "", nil
	}
	if path.Base(envArchivePath) != switchWIPArchiveName {
		return "", fmt.Errorf("unexpected output while capturing uncommitted changes: %q", envArchivePath)
	}
	defer func() {
		for _, p := range []string{envArchivePath, path.Dir(envArchivePath)} {
			if err := e.Remove(ctx, p); err != nil {
				log.Warn().Err(err).Str("path", p).Msg("failed to remove switch WIP archive from env")
			}
		}
	}()

	data, err := e.ReadFile(ctx, envArchivePath)
	if err != nil {
		return "", fmt.Errorf("failed to read WIP archive from env: %w", err)
	}
	f, err := os.CreateTemp("", "sidekick-switch-wip-*.tgz")
	if err != nil {
		return "", fmt.Errorf("failed to create local WIP archive: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(f.Name())
		return "", fmt.Errorf("failed to write local WIP archive: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(f.Name())
		return "", fmt.Errorf("failed to write local WIP archive: %w", err)
	}
	return f.Name(), nil
}

func restoreFromSwitch(ctx context.Context, e Env, bundle SwitchBundle) error {
	if bundle.HeadCommit == "" {
		return fmt.Errorf("switch bundle has no head commit")
	}
	switch e.GetType() {
	case EnvTypeLocal, EnvTypeLocalGitWorktree, EnvTypeDevPod:
		if err := backupLocalSwitchWIP(ctx, e, bundle); err != nil {
			return fmt.Errorf("failed to back up local work before environment switch: %w", err)
		}
	}
	if _, err := gitOutputForSwitch(ctx, e, "reset", "--hard", bundle.HeadCommit); err != nil {
		return err
	}
	if bundle.WIPArchivePath == "" {
		return nil
	}

	data, err := os.ReadFile(bundle.WIPArchivePath)
	if err != nil {
		return fmt.Errorf("failed to read WIP archive: %w", err)
	}
	if err := e.WriteFile(ctx, HibernationPatchFile, data, 0o600); err != nil {
		return fmt.Errorf("failed to copy WIP archive into env: %w", err)
	}
	defer func() {
		if err := e.Remove(ctx, HibernationPatchFile); err != nil {
			log.Warn().Err(err).Str("workDir", e.GetWorkingDirectory()).Msg("failed to remove switch WIP archive after restore")
		}
	}()

	out, err := e.RunCommand(ctx, EnvRunCommandInput{
		Command: "sh",
		Args:    []string{"-c", "set -e\n" + restoreFilesFragment()},
	})
	if err != nil {
		return fmt.Errorf("failed to restore uncommitted changes: %w", err)
	}
	if out.ExitStatus != 0 {
		return fmt.Errorf("failed to restore uncommitted changes (exit %d): %s", out.ExitStatus, strings.TrimSpace(out.Stderr))
	}
	return nil
}

func gitOutputForSwitch(ctx context.Context, e Env, args ...string) (string, error) {
	out, err := e.RunCommand(ctx, EnvRunCommandInput{Command: "git", Args: args})
	// A lost response leaves execution uncertain. Only read-only queries can
	// safely be replayed after the transport invalidates the failed channel.
	if len(args) > 0 && args[0] == "rev-parse" {
		for attempt := 1; attempt < 3 && err != nil && ctx.Err() == nil; attempt++ {
			var transportErr *agentExecTransportError
			if !errors.As(err, &transportErr) {
				break
			}
			out, err = e.RunCommand(ctx, EnvRunCommandInput{Command: "git", Args: args})
		}
	}
	if err != nil {
		return "", fmt.Errorf("git %s failed: %w", strings.Join(args, " "), err)
	}
	if out.ExitStatus != 0 {
		return "", fmt.Errorf("git %s failed (exit %d): %s", strings.Join(args, " "), out.ExitStatus, strings.TrimSpace(out.Stderr))
	}
	return strings.TrimSpace(out.Stdout), nil
}

func switchableEnv(c EnvContainer) (SwitchableEnv, error) {
	switchable, ok := c.Env.(SwitchableEnv)
	if !ok {
		return nil, fmt.Errorf("env %T does not support switching", c.Env)
	}
	return switchable, nil
}

type ExportForSwitchInput struct {
	EnvContainer EnvContainer `json:"envContainer"`
	Branch       string       `json:"branch"`
}

type ExportForSwitchOutput struct {
	Bundle SwitchBundle `json:"bundle"`
}

// ExportForSwitchActivity exports the flow branch and uncommitted work of the
// env being switched away from.
func ExportForSwitchActivity(ctx context.Context, input ExportForSwitchInput) (ExportForSwitchOutput, error) {
	switchable, err := switchableEnv(input.EnvContainer)
	if err != nil {
		return ExportForSwitchOutput{}, err
	}
	bundle, err := switchable.ExportForSwitch(ctx, input.Branch)
	if err != nil {
		return ExportForSwitchOutput{}, err
	}
	return ExportForSwitchOutput{Bundle: bundle}, nil
}

type RestoreFromSwitchInput struct {
	EnvContainer EnvContainer `json:"envContainer"`
	Bundle       SwitchBundle `json:"bundle"`
}

type RestoreFromSwitchOutput struct{}

// RestoreFromSwitchActivity applies an exported bundle to the env being
// switched to, which must already have the bundle's branch checked out.
func RestoreFromSwitchActivity(ctx context.Context, input RestoreFromSwitchInput) (RestoreFromSwitchOutput, error) {
	switchable, err := switchableEnv(input.EnvContainer)
	if err != nil {
		return RestoreFromSwitchOutput{}, err
	}
	if err := switchable.RestoreFromSwitch(ctx, input.Bundle); err != nil {
		return RestoreFromSwitchOutput{}, err
	}
	head, err := gitOutputForSwitch(ctx, switchable, "rev-parse", "HEAD")
	if err != nil {
		return RestoreFromSwitchOutput{}, err
	}
	if head != input.Bundle.HeadCommit {
		return RestoreFromSwitchOutput{}, fmt.Errorf("switched env HEAD is %s, expected %s", head, input.Bundle.HeadCommit)
	}
	return RestoreFromSwitchOutput{}, nil
}

func backupLocalSwitchWIP(ctx context.Context, e Env, bundle SwitchBundle) error {
	status, err := gitOutputForSwitch(ctx, e, "status", "--porcelain", "--untracked-files=all", "--ignore-submodules=none")
	if err != nil || status == "" {
		return err
	}

	message := fmt.Sprintf("sidekick: WIP saved during environment switch for %s at %s", bundle.Branch, bundle.HeadCommit)
	if _, err := gitOutputForSwitch(ctx, e, "stash", "push", "--include-untracked", "--message", message); err != nil {
		return err
	}
	// Stash cannot preserve every kind of dirty state, such as edits inside
	// submodules. Never proceed with a destructive restore in that case.
	status, err = gitOutputForSwitch(ctx, e, "status", "--porcelain", "--untracked-files=all", "--ignore-submodules=none")
	if err != nil {
		return err
	}
	if status != "" {
		return fmt.Errorf("local worktree is still dirty after stashing; refusing to overwrite remaining changes")
	}
	stash, err := gitOutputForSwitch(ctx, e, "rev-parse", "refs/stash")
	if err != nil {
		return err
	}
	ref := "refs/side/env-switch-wip/" + stash
	if _, err := gitOutputForSwitch(ctx, e, "update-ref", ref, stash); err != nil {
		return err
	}
	log.Info().Str("workDir", e.GetWorkingDirectory()).Str("recoveryRef", ref).
		Str("recoveryCommand", "git stash apply --index "+ref).
		Msg("saved WIP during environment switch")

	// Preserve the recovery ref before removing our entry from the user's list.
	entries, err := gitOutputForSwitch(ctx, e, "stash", "list", "--format=%gd %H")
	if err != nil {
		return err
	}
	for _, entry := range strings.Split(entries, "\n") {
		fields := strings.Fields(entry)
		if len(fields) == 2 && fields[1] == stash {
			_, err := gitOutputForSwitch(ctx, e, "stash", "drop", fields[0])
			return err
		}
	}
	return nil
}
