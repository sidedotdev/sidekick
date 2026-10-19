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

	"go.temporal.io/sdk/workflow"
)

const (
	ActionTypeEnvSwitch = "env_switch"

	globalStateKeyCommandPermissions       = "commandPermissions"
	globalStateKeyCommandPermissionSources = "commandPermissionSources"
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
	EnvType    string `json:"envType"`
	HeadCommit string `json:"headCommit"`
}

// startEnvSwitch begins switching the flow to desired.EnvType in its own
// coroutine, returning once the tracking env_switch flow action exists. The
// caller must hold lock; the coroutine releases it once the switch ends.
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
			return switchEnv(actionCtx.Context, dCtx, desired)
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

// switchEnv moves the flow's branch and uncommitted work to a newly
// provisioned env of desired.EnvType and publishes it. On failure the
// previous env stays active and whatever was provisioned is retired; no env
// is deleted either way.
func switchEnv(ctx workflow.Context, dCtx *DevContext, desired EnvSettings) (envSwitchResult, error) {
	state := newFlowEnvState(dCtx.GlobalState)
	ctx = utils.ProvisioningRetryCtx(env.WithSwitchBypass(ctx))

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
	return result, nil
}

func performEnvSwitch(ctx workflow.Context, dCtx *DevContext, state flowEnvState, desired EnvSettings) (envSwitchResult, error) {
	sources, ok := dCtx.GlobalState.GetValue(globalStateKeyCommandPermissionSources).(commandPermissionSources)
	if !ok {
		return envSwitchResult{}, errors.New("cannot switch environments: the flow's command permission sources are unknown")
	}

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

	err = workflow.ExecuteActivity(ctx, env.RestoreFromSwitchActivity, env.RestoreFromSwitchInput{
		EnvContainer: target.EnvContainer,
		Bundle:       bundle,
	}).Get(ctx, nil)
	if err != nil {
		return envSwitchResult{}, fmt.Errorf("failed to restore flow state into %s env: %w", desired.EnvType, err)
	}

	err = workflow.ExecuteActivity(ctx, env.VerifySwitchActivity, env.VerifySwitchInput{
		EnvContainer: target.EnvContainer,
		ExpectedHead: bundle.HeadCommit,
	}).Get(ctx, nil)
	if err != nil {
		return envSwitchResult{}, fmt.Errorf("failed to verify %s env: %w", desired.EnvType, err)
	}

	permissions, err := envDerivedCommandPermissions(ctx, desired.EnvType,
		common.RepoConfig{CommandPermissions: sources.Repo},
		common.LocalPublicConfig{CommandPermissions: sources.Local},
		domain.WorkspaceConfig{CommandPermissions: sources.Workspace},
	)
	if err != nil {
		return envSwitchResult{}, err
	}

	publishSwitchedEnv(dCtx, state, source, target, desired, permissions)
	return envSwitchResult{EnvType: desired.EnvType, HeadCommit: bundle.HeadCommit}, nil
}

func publishSwitchedEnv(dCtx *DevContext, state flowEnvState, source env.EnvContainer, target ensuredEnv, desired EnvSettings, permissions common.CommandPermissionConfig) {
	var sourceWorktree *domain.Worktree
	if dCtx.Worktree != nil {
		worktree := *dCtx.Worktree
		sourceWorktree = &worktree
	}
	state.retire(retiredEnv{EnvContainer: source, Worktree: sourceWorktree})
	state.unretire(env.IdentityOf(target.EnvContainer))

	*dCtx.EnvContainer = target.EnvContainer
	if dCtx.Worktree != nil && target.Worktree != nil {
		dCtx.Worktree.WorkingDirectory = target.Worktree.WorkingDirectory
	}

	dCtx.RepoConfig.EnvType = desired.EnvType
	dCtx.RepoConfig.ModalConfig = desired.Modal
	dCtx.RepoConfig.PortForwards = desired.PortForwards
	dCtx.RepoConfig.CommandPermissions = permissions
	dCtx.GlobalState.SetValue(globalStateKeyModalConfig, desired.Modal)
	dCtx.GlobalState.SetValue(globalStateKeyPortForwards, desired.PortForwards)
	dCtx.GlobalState.SetValue(globalStateKeyCommandPermissions, permissions)
	state.publish(dCtx.EnvContainer)
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
