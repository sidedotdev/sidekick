package dev

import (
	"errors"
	"fmt"
	"sidekick/common"
	"sidekick/domain"
	"sidekick/env"
	"sidekick/flow_action"
	"sidekick/srv"
	"sidekick/utils"
	"strings"
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

const (
	ActionTypeEnvSwitch = "env_switch"

	globalStateKeyCommandPermissions       = "commandPermissions"
	globalStateKeyCommandPermissionSources = "commandPermissionSources"

	maxSupersededSandboxStopAttempts = 30
)

// commandPermissionSources are the unmerged command permission layers a flow
// was set up with, from which env-dependent permissions are re-derived when
// the flow switches env type.
type commandPermissionSources struct {
	Local     common.CommandPermissionConfig
	Repo      common.CommandPermissionConfig
	Workspace common.CommandPermissionConfig
}

func recordCommandPermissionSources(gs *flow_action.GlobalState, sources commandPermissionSources) {
	gs.SetValue(globalStateKeyCommandPermissionSources, sources)
}

// effectiveCommandPermissions returns the flow's merged command permissions,
// preferring those published flow-wide by an env switch over the copy each
// DevContext received at setup.
func effectiveCommandPermissions(dCtx DevContext) common.CommandPermissionConfig {
	if dCtx.GlobalState != nil {
		if published, ok := dCtx.GlobalState.GetValue(globalStateKeyCommandPermissions).(common.CommandPermissionConfig); ok {
			return published
		}
	}
	return dCtx.RepoConfig.CommandPermissions
}

type envSwitchResult struct {
	EnvType string `json:"envType"`
	// HeadCommit is set when the flow's work was transferred to a new env.
	HeadCommit string `json:"headCommit,omitempty"`
	// SandboxName is set when the env was replaced by a successor sandbox.
	SandboxName string `json:"sandboxName,omitempty"`
}

// startEnvSwitch begins moving the flow onto desired — another env type, or
// the current type with different settings — in its own coroutine, returning
// once the tracking env_switch flow action exists. The caller must hold lock;
// the coroutine releases it once the switch ends.
func startEnvSwitch(ctx workflow.Context, dCtx *DevContext, lock workflow.Mutex, desired EnvSettings) (EnvUpdateResult, error) {
	state := newFlowEnvState(dCtx.GlobalState)
	from := currentEnvType(*dCtx)
	// Recorded before anything yields, so the env gate holds new env work and
	// further updates are rejected while the switch runs.
	state.setEnvSwitch(envSwitchState{From: from, To: desired.EnvType})

	var flowActionId string
	var startErr error
	started := false
	workflow.Go(dCtx.Context, func(gCtx workflow.Context) {
		// Deferred in this order so the switch no longer appears in
		// progress to an update that acquires the lock next.
		defer lock.Unlock()
		defer state.clearEnvSwitch()

		switchCtx := *dCtx
		switchCtx.Context = gCtx
		// The switch runs alongside the flow, so it must not be attributed
		// to whichever subflow the flow happens to be in.
		switchCtx.FlowScope = &flow_action.FlowScope{}
		actionCtx := switchCtx.ExecContext.NewActionContext(ActionTypeEnvSwitch)
		actionCtx.ActionParams = map[string]interface{}{"from": from, "to": desired.EnvType}

		_, err := flow_action.Track(actionCtx, func(actionCtx flow_action.ActionContext, flowAction *domain.FlowAction) (envSwitchResult, error) {
			flowActionId = flowAction.Id
			started = true
			if current := state.envSwitch(); current != nil {
				updated := *current
				updated.FlowActionId = flowAction.Id
				state.setEnvSwitch(updated)
			}
			return switchEnv(actionCtx.Context, dCtx, lock, desired)
		})
		if !started {
			startErr = err
			started = true
		}
	})

	if err := workflow.Await(ctx, func() bool { return started }); err != nil {
		return EnvUpdateResult{}, err
	}
	if startErr != nil {
		return EnvUpdateResult{}, fmt.Errorf("failed to start environment switch: %w", startErr)
	}
	return EnvUpdateResult{FlowActionId: flowActionId, Settings: currentEnvSettings(*dCtx)}, nil
}

// switchEnv prepares an env matching desired, publishes it, and only then
// lets go of the previous env. On failure the previous env stays active and
// whatever was provisioned is retired for flow-end cleanup. lock is the
// settings lock the caller holds for the duration of the switch.
func switchEnv(ctx workflow.Context, dCtx *DevContext, lock workflow.Mutex, desired EnvSettings) (envSwitchResult, error) {
	state := newFlowEnvState(dCtx.GlobalState)
	ctx = utils.ProvisioningRetryCtx(env.WithSwitchBypass(ctx))
	source := *dCtx.EnvContainer

	result, err := performEnvSwitch(ctx, dCtx, state, desired)
	if err != nil {
		if current := state.envSwitch(); current != nil && current.Target != nil {
			state.retire(retiredEnv{EnvContainer: *current.Target, Worktree: current.TargetWorktree})
			if dCtx.Worktree != nil && current.TargetWorktree != nil {
				restoreWorktreeRecord(ctx, *dCtx.Worktree)
			}
		}
		return envSwitchResult{}, err
	}
	stopSupersededSandbox(ctx, state, lock, source, *dCtx.EnvContainer)
	return result, nil
}

func performEnvSwitch(ctx workflow.Context, dCtx *DevContext, state flowEnvState, desired EnvSettings) (envSwitchResult, error) {
	if desired.EnvType == currentEnvType(*dCtx) {
		return reconfigureEnv(ctx, dCtx, state, desired)
	}
	sources, ok := dCtx.GlobalState.GetValue(globalStateKeyCommandPermissionSources).(commandPermissionSources)
	if !ok {
		return envSwitchResult{}, errors.New("cannot switch environments: the flow's command permission sources are unknown")
	}
	permissions, err := envDerivedCommandPermissions(ctx, desired.EnvType,
		common.RepoConfig{CommandPermissions: sources.Repo},
		common.LocalPublicConfig{CommandPermissions: sources.Local},
		domain.WorkspaceConfig{CommandPermissions: sources.Workspace},
	)
	if err != nil {
		return envSwitchResult{}, err
	}
	sandboxName := ""
	if retired := state.retiredOfType(env.EnvType(desired.EnvType)); retired != nil {
		sandboxName = reattachSandboxName(*retired)
	}
	return transferEnv(ctx, dCtx, state, desired, permissions, sandboxName)
}

// reattachSandboxName names the Modal sandbox a switch back to a retired env
// should use: the retired sandbox itself while it is still running, otherwise
// a successor, since stopping a Modal sandbox is terminal and Modal keeps the
// name reserved for a while afterwards. Other sandbox types derive their names
// from the repo, so the empty name leaves provisioning to its default.
func reattachSandboxName(retired retiredEnv) string {
	modalEnv, ok := retired.EnvContainer.Env.(*env.ModalEnv)
	if !ok {
		return ""
	}
	if retired.StopRequested || retired.Stopped {
		return env.ModalReplacementSandboxName(modalEnv.SandboxName)
	}
	return modalEnv.SandboxName
}

// reconfigureEnv applies desired's settings to an env of the type the flow
// already runs on. Port forwards are adjusted on the live env; changed
// sandbox settings replace the sandbox with a successor restored from its
// snapshot, or, when no usable snapshot exists, with a fresh sandbox the
// flow's work is transferred to.
func reconfigureEnv(ctx workflow.Context, dCtx *DevContext, state flowEnvState, desired EnvSettings) (envSwitchResult, error) {
	current := currentEnvConfig(*dCtx)
	desiredConfig := env.EnvConfig{PortForwards: desired.PortForwards, Modal: desired.Modal}
	modalEnv, isModal := dCtx.EnvContainer.Env.(*env.ModalEnv)
	if !isModal || !current.SandboxSettingsChanged(desiredConfig) {
		return adjustEnvConfig(ctx, dCtx, state, current, desiredConfig)
	}

	source := *dCtx.EnvContainer
	// Creation may succeed even when the activity fails or is cancelled before
	// returning its endpoint. Cleanup can address that sandbox by name alone.
	state.recordSwitchTarget(ensuredEnv{EnvContainer: env.EnvContainer{Env: &env.ModalEnv{
		SandboxName:      env.ModalReplacementSandboxName(modalEnv.SandboxName),
		WorkingDirectory: modalEnv.WorkingDirectory,
		LocalRepoDir:     modalEnv.LocalRepoDir,
	}}})
	var replaced env.ModalReplaceSandboxOutput
	err := workflow.ExecuteActivity(ctx, env.ModalReplaceSandboxActivity, env.ModalReplaceSandboxInput{
		EnvContainer: source,
		Config:       desired.Modal,
		PortForwards: desired.PortForwards,
	}).Get(ctx, &replaced)
	if err != nil {
		if !env.CannotReplaceFromSnapshot(err) {
			return envSwitchResult{}, fmt.Errorf("failed to replace Modal sandbox: %w", err)
		}
		workflow.GetLogger(ctx).Info("Modal sandbox cannot be replaced from its snapshot; transferring the flow's work to a fresh sandbox", "error", err)
		return transferEnv(ctx, dCtx, state, desired, effectiveCommandPermissions(*dCtx), env.ModalReplacementSandboxName(modalEnv.SandboxName))
	}

	target := ensuredEnv{EnvContainer: replaced.EnvContainer}
	if dCtx.Worktree != nil {
		target.Worktree = worktreeAt(*dCtx.Worktree, replaced.EnvContainer.Env.GetWorkingDirectory())
	}
	state.recordSwitchTarget(target)
	publishSwitchedEnv(dCtx, state, source, target, desired, effectiveCommandPermissions(*dCtx))
	return envSwitchResult{EnvType: desired.EnvType, SandboxName: replaced.EnvContainer.Env.(*env.ModalEnv).SandboxName}, nil
}

// adjustEnvConfig changes the live env's configuration in place, so nothing
// is retired.
func adjustEnvConfig(ctx workflow.Context, dCtx *DevContext, state flowEnvState, current, desired env.EnvConfig) (envSwitchResult, error) {
	var output env.UpdateEnvConfigOutput
	err := workflow.ExecuteActivity(ctx, env.UpdateEnvConfigActivity, env.UpdateEnvConfigInput{
		EnvContainer: *dCtx.EnvContainer,
		Current:      current,
		Desired:      desired,
	}).Get(ctx, &output)
	if err != nil {
		return envSwitchResult{}, err
	}
	publishEnvConfig(dCtx, state, output.EnvContainer, output.Config)
	return envSwitchResult{EnvType: currentEnvType(*dCtx)}, nil
}

// transferEnv moves the flow's branch and uncommitted work to a newly
// provisioned env and publishes it. sandboxName, when set, names the sandbox
// to provision instead of the flow's default one.
func transferEnv(ctx workflow.Context, dCtx *DevContext, state flowEnvState, desired EnvSettings, permissions common.CommandPermissionConfig, sandboxName string) (envSwitchResult, error) {
	source := *dCtx.EnvContainer
	branch, err := flowBranch(ctx, *dCtx)
	if err != nil {
		return envSwitchResult{}, err
	}

	var exported env.ExportForSwitchOutput
	err = workflow.ExecuteActivity(ctx, env.ExportForSwitchActivity, env.ExportForSwitchInput{
		EnvContainer: source,
		Branch:       branch,
	}).Get(ctx, &exported)
	if err != nil {
		return envSwitchResult{}, fmt.Errorf("failed to export flow state from %s env: %w", source.Env.GetType(), err)
	}
	bundle := exported.Bundle

	targetRepoConfig := dCtx.RepoConfig
	targetRepoConfig.EnvType = desired.EnvType
	targetRepoConfig.ModalConfig = desired.Modal
	targetRepoConfig.PortForwards = desired.PortForwards
	params := ensureEnvParams{
		WorkspaceId: dCtx.WorkspaceId,
		RepoDir:     bundle.LocalRepoDir,
		EnvType:     desired.EnvType,
		RepoMode:    string(env.RepoModeInPlace),
		StartBranch: &branch,
		RepoConfig:  targetRepoConfig,
		AllowReuse:  true,
		SandboxName: sandboxName,
		OnProgress:  state.recordSwitchTarget,
	}
	if dCtx.Worktree != nil {
		params.RepoMode = string(env.RepoModeWorktree)
		params.CreateWorktree = attachWorktree(ctx, *dCtx.Worktree)
	}
	target, err := ensureEnv(ctx, params)
	if err != nil {
		return envSwitchResult{}, fmt.Errorf("failed to provision %s env: %w", desired.EnvType, err)
	}
	state.recordSwitchTarget(target)

	restoreInput := env.RestoreFromSwitchInput{
		EnvContainer: target.EnvContainer,
		Bundle:       bundle,
	}
	if desired.EnvType == string(env.EnvTypeModal) || desired.EnvType == string(env.EnvTypeOpenShell) {
		switch source.Env.GetType() {
		case env.EnvTypeLocal, env.EnvTypeLocalGitWorktree, env.EnvTypeDevPod:
			restoreInput.SourceEnvContainer = &source
		}
	}
	err = workflow.ExecuteActivity(ctx, env.RestoreFromSwitchActivity, restoreInput).Get(ctx, nil)
	if err != nil {
		return envSwitchResult{}, fmt.Errorf("failed to restore flow state into %s env: %w", desired.EnvType, err)
	}

	publishSwitchedEnv(dCtx, state, source, target, desired, permissions)
	return envSwitchResult{EnvType: desired.EnvType, HeadCommit: bundle.HeadCommit}, nil
}

// publishSwitchedEnv makes target the flow's env and retires the env it
// replaces.
func publishSwitchedEnv(dCtx *DevContext, state flowEnvState, source env.EnvContainer, target ensuredEnv, desired EnvSettings, permissions common.CommandPermissionConfig) {
	var sourceWorktree *domain.Worktree
	if dCtx.Worktree != nil {
		worktree := *dCtx.Worktree
		sourceWorktree = &worktree
	}
	state.retire(retiredEnv{EnvContainer: source, Worktree: sourceWorktree})
	state.unretire(target.EnvContainer)

	if dCtx.Worktree != nil && target.Worktree != nil {
		dCtx.Worktree.WorkingDirectory = target.Worktree.WorkingDirectory
	}
	dCtx.RepoConfig.EnvType = desired.EnvType
	dCtx.RepoConfig.CommandPermissions = permissions
	dCtx.GlobalState.SetValue(globalStateKeyCommandPermissions, permissions)
	publishEnvConfig(dCtx, state, target.EnvContainer, env.EnvConfig{PortForwards: desired.PortForwards, Modal: desired.Modal})
}

// publishEnvConfig makes container the flow's env, with config as the
// configuration it reflects.
func publishEnvConfig(dCtx *DevContext, state flowEnvState, container env.EnvContainer, config env.EnvConfig) {
	*dCtx.EnvContainer = container
	dCtx.RepoConfig.ModalConfig = config.Modal
	dCtx.RepoConfig.PortForwards = config.PortForwards
	dCtx.GlobalState.SetValue(globalStateKeyModalConfig, config.Modal)
	dCtx.GlobalState.SetValue(globalStateKeyPortForwards, config.PortForwards)
	state.publish(dCtx.EnvContainer)
}

// stopSupersededSandbox stops the sandbox the flow just left, so a
// long-running command cannot keep it alive past its idle timeout. The
// flow's work already lives on the published env (a replaced sandbox was
// snapshotted first), so this runs in the background and keeps retrying
// rather than holding up the switch; whatever still fails is handled by
// flow-end cleanup.
//
// Each attempt holds the settings lock, so a switch back to this sandbox
// cannot start while an attempt is in flight (an attempt that times out may
// still be stopping the sandbox remotely, which the lock cannot cover). A
// switch back that lands between attempts unretires the sandbox, which ends
// the retries.
func stopSupersededSandbox(ctx workflow.Context, state flowEnvState, lock workflow.Mutex, previous, current env.EnvContainer) {
	sandbox, ok := previous.Env.(env.SandboxEnv)
	if !ok || sandbox.SandboxSharedAcrossFlows() || sameEnvInstance(previous, current) {
		return
	}
	state.markStopRequested(previous)
	input := env.StopSandboxInput{EnvType: previous.Env.GetType(), SandboxName: sandbox.GetSandboxName()}
	workflow.Go(ctx, func(ctx workflow.Context) {
		ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
			StartToCloseTimeout: 5 * time.Minute,
			RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1},
		})
		backoff := 10 * time.Second
		for attempt := 1; ; attempt++ {
			if err := lock.Lock(ctx); err != nil {
				return
			}
			done, err := stopRetiredSandbox(ctx, state, previous, input)
			lock.Unlock()
			if done {
				return
			}
			if attempt >= maxSupersededSandboxStopAttempts {
				workflow.GetLogger(ctx).Warn("Giving up stopping superseded sandbox; it will be cleaned up at flow end",
					"envType", input.EnvType, "sandboxName", input.SandboxName, "attempts", attempt, "error", err)
				return
			}
			workflow.GetLogger(ctx).Warn("Failed to stop superseded sandbox; retrying",
				"envType", input.EnvType, "sandboxName", input.SandboxName, "attempt", attempt, "error", err)
			if err := workflow.Sleep(ctx, backoff); err != nil {
				return
			}
			backoff = min(backoff*2, 10*time.Minute)
		}
	})
}

// stopRetiredSandbox stops sandbox unless the flow has meanwhile returned to
// it or it was already stopped. done reports whether nothing is left to do.
func stopRetiredSandbox(ctx workflow.Context, state flowEnvState, sandbox env.EnvContainer, input env.StopSandboxInput) (done bool, err error) {
	if current := state.current(); current != nil && current.Env != nil && sameEnvInstance(*current, sandbox) {
		return true, nil
	}
	if entry := state.retiredEntry(sandbox); entry == nil || entry.Stopped {
		return true, nil
	}
	if err := workflow.ExecuteActivity(ctx, env.StopSandboxActivity, input).Get(ctx, nil); err != nil {
		return false, err
	}
	state.markStopped(sandbox)
	return true, nil
}

// flowBranch returns the branch holding the flow's work: the flow worktree's
// branch, or in in-place mode whatever the env has checked out.
func flowBranch(ctx workflow.Context, dCtx DevContext) (string, error) {
	if dCtx.Worktree != nil {
		return dCtx.Worktree.Name, nil
	}
	var output env.EnvRunCommandActivityOutput
	err := workflow.ExecuteActivity(ctx, env.EnvRunCommandActivity, env.EnvRunCommandActivityInput{
		EnvContainer: *dCtx.EnvContainer,
		Command:      "git",
		Args:         []string{"rev-parse", "--abbrev-ref", "HEAD"},
	}).Get(ctx, &output)
	if err != nil {
		return "", fmt.Errorf("failed to determine checked-out branch: %w", err)
	}
	if output.ExitStatus != 0 {
		return "", fmt.Errorf("failed to determine checked-out branch (exit %d): %s", output.ExitStatus, strings.TrimSpace(output.Stderr))
	}
	branch := strings.TrimSpace(output.Stdout)
	if branch == "" || branch == "HEAD" {
		return "", errors.New("cannot switch environments while HEAD is detached")
	}
	return branch, nil
}

// restoreWorktreeRecord re-persists the flow worktree as it was before a
// failed switch persisted the target's working directory.
func restoreWorktreeRecord(ctx workflow.Context, worktree domain.Worktree) {
	disconnectedCtx, _ := workflow.NewDisconnectedContext(ctx)
	storageCtx := utils.WithStorageActivityOptions(disconnectedCtx)
	if err := workflow.ExecuteActivity(storageCtx, srv.Activities.PersistWorktree, worktree).Get(storageCtx, nil); err != nil {
		workflow.GetLogger(ctx).Warn("Failed to restore worktree record after failed env switch", "worktree", worktree.Name, "error", err)
	}
}
