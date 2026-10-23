package dev

import (
	"context"
	"errors"
	"fmt"
	"sidekick/coding/git"
	"sidekick/common"
	"sidekick/domain"
	"sidekick/env"
	"sidekick/flow_action"
	"sidekick/secret_manager"
	"sidekick/srv"
	"sidekick/utils"
	"sidekick/workspace"
	"slices"
	"time"

	"github.com/rs/zerolog/log"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

type ContextGatherType string

const (
	ContextGatherTypeLegacy  ContextGatherType = "legacy"
	ContextGatherTypeExplore ContextGatherType = "explore"
)

func shouldGatherContext(contextGatherType ContextGatherType, editable bool) bool {
	return contextGatherType == ContextGatherTypeExplore && editable
}

type DevContext struct {
	flow_action.ExecContext
	Worktree          *domain.Worktree
	RepoConfig        common.RepoConfig
	ContextGatherType ContextGatherType
	// Idd indicates the work originates from an Intent Driven Development flow,
	// enabling the intent/ directory guidance in coding-agent prompts.
	Idd bool
	// AdvisorEnabled is a per-run toggle (chosen in the task modal) for the
	// background advisor. It defaults to true when unset in ConfigOverrides.
	AdvisorEnabled bool
}

// scheduleRepoDeepening starts background history backfill for a freshly
// synced (possibly shallow) sandbox repo without blocking setup; failures
// are only logged, since a shallow repo remains fully usable.
func scheduleRepoDeepening(ctx workflow.Context, envContainer env.EnvContainer, repoDir, remoteRepoDir string, branches []string) {
	deepenCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Minute,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 2},
	})
	future := workflow.ExecuteActivity(deepenCtx, env.DeepenRepoActivity, env.DeepenRepoInput{
		EnvContainer:  envContainer,
		LocalRepoDir:  repoDir,
		RemoteRepoDir: remoteRepoDir,
		Branches:      branches,
	})
	snapshotAfterDeepening := workflow.GetVersion(ctx, "snapshot-after-repo-deepening", workflow.DefaultVersion, 1) >= 1
	_, supportsSnapshot := envContainer.Env.(env.SnapshottingEnv)
	workflow.Go(ctx, func(gCtx workflow.Context) {
		var deepenOutput env.DeepenRepoOutput
		if err := future.Get(gCtx, &deepenOutput); err != nil {
			workflow.GetLogger(gCtx).Warn("Background repo deepening failed", "error", err)
			return
		}
		if !snapshotAfterDeepening || !deepenOutput.Deepened || !supportsSnapshot {
			return
		}

		snapshotCtx := workflow.WithActivityOptions(gCtx, workflow.ActivityOptions{
			StartToCloseTimeout: 30 * time.Minute,
			HeartbeatTimeout:    2 * time.Minute,
			WaitForCancellation: true,
			RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 1},
		})
		var snapshotOutput env.SnapshotEnvironmentOutput
		err := workflow.ExecuteActivity(snapshotCtx, env.SnapshotEnvironmentActivity, env.SnapshotEnvironmentInput{
			EnvContainer:  envContainer,
			RemoteRepoDir: remoteRepoDir,
		}).Get(snapshotCtx, &snapshotOutput)
		if err != nil {
			workflow.GetLogger(gCtx).Warn(
				"Post-deepening environment snapshot failed",
				"envType", envContainer.Env.GetType(),
				"error", err,
			)
			return
		}
		if !snapshotOutput.Snapshotted {
			workflow.GetLogger(gCtx).Warn(
				"Post-deepening environment snapshot retries exhausted",
				"envType", envContainer.Env.GetType(),
				"attempts", snapshotOutput.Attempts,
			)
		}
	})
}

// WithContext returns a new DevContext with the workflow.Context updated.
func (dCtx DevContext) WithContext(ctx workflow.Context) DevContext {
	newCtx := dCtx
	newCtx.Context = ctx
	return newCtx
}

func (dCtx DevContext) WithCancelOnPause() DevContext {
	ctx, cancel := workflow.WithCancel(dCtx.Context)
	dCtx.Context = ctx
	dCtx.GlobalState.AddCancelFunc(cancel)
	return dCtx
}

func SetupDevContext(ctx workflow.Context, workspaceId string, repoDir string, envType string, repoMode string, startBranch *string, requirements string, configOverrides common.ConfigOverrides) (DevContext, error) {
	initialExecCtx := flow_action.ExecContext{
		Context:     ctx,
		WorkspaceId: workspaceId,
		FlowScope: &flow_action.FlowScope{
			SubflowName: "Initialize",
		},
	}
	return flow_action.TrackSubflowFailureOnly(initialExecCtx, "flow_init", "Initialize", func(_ domain.Subflow) (DevContext, error) {
		actionCtx := initialExecCtx.NewActionContext("setup_dev_context")
		return flow_action.TrackFailureOnly(actionCtx, func(trackedCtx flow_action.ActionContext, _ *domain.FlowAction) (DevContext, error) {
			return setupDevContextAction(trackedCtx.Context, workspaceId, repoDir, envType, repoMode, startBranch, requirements, configOverrides)
		})
	})
}

func setupDevContextAction(ctx workflow.Context, workspaceId string, repoDir string, envType string, repoMode string, startBranch *string, requirements string, configOverrides common.ConfigOverrides) (DevContext, error) {
	ctx = utils.NoRetryCtx(ctx)

	var err error
	var envContainer env.EnvContainer
	var worktree *domain.Worktree
	var localConfig common.LocalPublicConfig
	var workspaceConfig domain.WorkspaceConfig
	var llmConfig common.LLMConfig
	var embeddingConfig common.EmbeddingConfig
	var unscopedProviders []common.ModelProviderPublicConfig
	var unscopedLLMConfig common.LLMConfig
	var unscopedEmbeddingConfig common.EmbeddingConfig

	enableBranchNameGeneration := workflow.GetVersion(ctx, "branch-name-generation", workflow.DefaultVersion, 1) >= 1

	var tempLocalExecContext flow_action.ExecContext
	// for workflow backcompat/replay, we can't load configs early unless enabled
	if enableBranchNameGeneration {
		tempLocalExecContext, localConfig, workspaceConfig, err = NewTempLocalExecContext(ctx, workspaceId, repoDir, configOverrides)
		if err != nil {
			return DevContext{}, err
		}
		llmConfig = tempLocalExecContext.GetLLMConfig()
		embeddingConfig = tempLocalExecContext.GetEmbeddingConfig()
	} else {
		tempProviders := localConfig.Providers
		if configOverrides.Providers != nil {
			tempProviders = *configOverrides.Providers
		}
		tempLocalExecContext, err = newTempLocalExecContext(ctx, workspaceId, repoDir, defaultWorkspaceProfileId, tempProviders, llmConfig, embeddingConfig)
		if err != nil {
			return DevContext{}, err
		}
	}

	// createWorktree handles branch name generation, retry on conflicts, and persistence.
	// The createFn callback performs the env-specific worktree creation and returns the working directory.
	createWorktree := func(createFn func(wt domain.Worktree) (string, error)) (*domain.Worktree, error) {
		flowId := workflow.GetInfo(ctx).WorkflowExecution.ID
		var branchName string
		var workingDir string
		var wtId string

		if enableBranchNameGeneration {
			tempLocalRepoConfig, err := GetRepoConfig(tempLocalExecContext)
			if err != nil {
				return nil, fmt.Errorf("failed to get coding config: %v", err)
			}
			configOverrides.ApplyToRepoConfig(&tempLocalRepoConfig)
			tempLocalExecContext.DisableHumanInTheLoop = tempLocalRepoConfig.DisableHumanInTheLoop
			editHints := tempLocalRepoConfig.EditCode.Hints

			var excludeBranches []string
			for {
				branchName, err = GenerateBranchName(tempLocalExecContext, BranchNameRequest{
					Requirements:    requirements,
					Hints:           editHints,
					ExcludeBranches: excludeBranches,
				})
				if err != nil {
					return nil, fmt.Errorf("failed to generate branch name: %v", err)
				}

				wtId = ksuidSideEffect(ctx)
				workingDir, err = createFn(domain.Worktree{
					Id:          wtId,
					FlowId:      flowId,
					Name:        branchName,
					WorkspaceId: workspaceId,
				})
				if err == nil {
					break
				}

				// If branch already exists (race condition), exclude it and retry
				var appErr *temporal.ApplicationError
				if errors.As(err, &appErr) && appErr.Type() == env.ErrTypeBranchAlreadyExists {
					log.Warn().Err(err).Str("branch", branchName).Msg("Branch already exists, retrying with new name")
					excludeBranches = append(excludeBranches, branchName)
					continue
				}
				return nil, fmt.Errorf("failed to create worktree: %v", err)
			}
		} else {
			branchName = flowId
			wtId = ksuidSideEffect(ctx)
			workingDir, err = createFn(domain.Worktree{
				Id:          wtId,
				FlowId:      flowId,
				Name:        branchName,
				WorkspaceId: workspaceId,
			})
			if err != nil {
				return nil, fmt.Errorf("failed to create worktree: %v", err)
			}
		}

		wt := &domain.Worktree{
			Id:               wtId,
			FlowId:           flowId,
			Name:             branchName,
			WorkspaceId:      workspaceId,
			WorkingDirectory: workingDir,
		}
		storageCtx := utils.WithStorageActivityOptions(ctx)
		err = workflow.ExecuteActivity(storageCtx, srv.Activities.PersistWorktree, *wt).Get(storageCtx, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to persist worktree: %v", err)
		}
		return wt, nil
	}

	envType, repoMode = normalizeLegacyEnvType(envType, repoMode)

	// Fall back to repo config defaults when envType is not specified.
	// Gated behind a version check to avoid introducing new activity calls
	// during replay of old workflows that never called GetRepoConfig here.
	envFromConfigVersion := workflow.GetVersion(ctx, "env-from-repo-config", workflow.DefaultVersion, 1)
	if envType == "" && envFromConfigVersion >= 1 {
		tempRepoConfig, configErr := GetRepoConfig(tempLocalExecContext)
		if configErr == nil {
			configOverrides.ApplyToRepoConfig(&tempRepoConfig)
			if tempRepoConfig.EnvType != "" {
				envType = tempRepoConfig.EnvType
			}
			if repoMode == "" && tempRepoConfig.RepoMode != "" {
				repoMode = tempRepoConfig.RepoMode
			}
		}
	}

	// Providers load the repo config lazily at their original points in the
	// recorded activity sequence; in-place local envs never load it.
	ensured, err := ensureEnv(ctx, ensureEnvParams{
		WorkspaceId: workspaceId,
		RepoDir:     repoDir,
		EnvType:     envType,
		RepoMode:    repoMode,
		StartBranch: startBranch,
		LoadRepoConfig: func() (common.RepoConfig, error) {
			repoConfig, err := GetRepoConfig(tempLocalExecContext)
			if err != nil {
				return common.RepoConfig{}, err
			}
			configOverrides.ApplyToRepoConfig(&repoConfig)
			return repoConfig, nil
		},
		CreateWorktree: createWorktree,
	})
	if err != nil {
		return DevContext{}, err
	}
	envContainer = ensured.EnvContainer
	worktree = ensured.Worktree

	// for workflow backcompat/replay, we have to do this later
	profileId := tempLocalExecContext.GetProfileId()
	finalProviders := tempLocalExecContext.Providers
	if !enableBranchNameGeneration {
		resolved, configErr := getConfigs(ctx, workspaceId)
		if configErr != nil {
			return DevContext{}, configErr
		}
		localConfig = resolved.LocalConfig
		workspaceConfig = resolved.WorkspaceConfig
		profileId = resolved.ProfileId
		finalProviders, llmConfig, embeddingConfig = applyConfigOverrides(resolved, configOverrides)
		unscopedProviders, unscopedLLMConfig, unscopedEmbeddingConfig = unscopedOverriddenConfigs(resolved, configOverrides)
	}

	eCtx := flow_action.ExecContext{
		FlowScope:    &flow_action.FlowScope{},
		Context:      ctx,
		WorkspaceId:  workspaceId,
		EnvContainer: &envContainer,
		Secrets: &secret_manager.SecretManagerContainer{
			SecretManager: secret_manager.NewProfileSecretManager(profileId),
		},
		Providers:       finalProviders, // TODO merge with workspace providers
		EmbeddingConfig: embeddingConfig,
		GlobalState:     &flow_action.GlobalState{},
	}
	envState := newFlowEnvState(eCtx.GlobalState)
	envState.publish(eCtx.EnvContainer)
	eCtx.Context = env.WithEnvTracker(ctx, envState)
	eCtx.SetProfileId(profileId)
	eCtx.SetLLMConfig(llmConfig)
	eCtx.SetProviders(finalProviders)
	eCtx.SetEmbeddingConfig(embeddingConfig)
	if enableBranchNameGeneration {
		copyUnscopedConfigs(tempLocalExecContext, eCtx)
	} else {
		persistUnscopedConfigs(eCtx, unscopedProviders, unscopedLLMConfig, unscopedEmbeddingConfig)
	}

	// NOTE: it's important to do this *after* the eCtx has been created, since
	// that ensures we get the correct repo config for the given start branch
	repoConfig, err := GetRepoConfig(eCtx)
	if err != nil {
		var hint string
		if worktree != nil {
			hint = "Please commit your repo config (side.yml or side.yaml) and .sideignore files (generated via `side init`), and make sure they are available from the base branch of the worktree."
		} else {
			hint = "Please commit your repo config (side.yml or side.yaml) and .sideignore files (generated via `side init`)"
		}

		return DevContext{}, fmt.Errorf("failed to get repo config: %v\n\n%s", err, hint)
	}

	configOverrides.ApplyToRepoConfig(&repoConfig)
	eCtx.DisableHumanInTheLoop = repoConfig.DisableHumanInTheLoop

	recordCommandPermissionSources(eCtx.GlobalState, commandPermissionSources{
		Local:     localConfig.CommandPermissions,
		Repo:      repoConfig.CommandPermissions,
		Workspace: workspaceConfig.CommandPermissions,
	})
	repoConfig.CommandPermissions, err = envDerivedCommandPermissions(ctx, envType, repoConfig, localConfig, workspaceConfig)
	if err != nil {
		return DevContext{}, err
	}

	// Execute worktree setup script if configured and using worktree mode
	if worktree != nil && repoConfig.WorktreeSetup != "" {
		var output env.EnvRunCommandActivityOutput
		err = workflow.ExecuteActivity(ctx, env.EnvRunCommandActivity, env.EnvRunCommandActivityInput{
			EnvContainer: envContainer,
			Command:      "/usr/bin/env",
			Args:         []string{"sh", "-c", repoConfig.WorktreeSetup},
		}).Get(ctx, &output)
		if err != nil {
			return DevContext{}, fmt.Errorf("failed to execute worktree setup script: %v", err)
		} else if output.ExitStatus != 0 {
			err = fmt.Errorf("worktree setup script failed with exit status %d:\n\n%s", output.ExitStatus, output.Stderr)
			if v := workflow.GetVersion(ctx, "worktree-setup-script-error", workflow.DefaultVersion, 1); v >= 1 {
				return DevContext{}, err
			} else {
				log.Err(err).Msg("Ignoring failure for workflow backcompat")
			}
		}
	}

	// Version 1 retains the activity for replay compatibility. Worktree
	// provisioning performs this setup for new workflows.
	if v := workflow.GetVersion(ctx, "ensure-side-tmp-dir", workflow.DefaultVersion, 2); v == 1 {
		var output env.EnvRunCommandActivityOutput
		err = workflow.ExecuteActivity(ctx, env.EnvRunCommandActivity, env.EnvRunCommandActivityInput{
			EnvContainer: envContainer,
			Command:      "/usr/bin/env",
			Args:         []string{"sh", "-c", env.EnsureSideTmpScript},
		}).Get(ctx, &output)
		if err != nil {
			return DevContext{}, fmt.Errorf("failed to set up .side/tmp scratch directory: %v", err)
		} else if output.ExitStatus != 0 {
			return DevContext{}, fmt.Errorf("failed to set up .side/tmp scratch directory (exit status %d):\n\n%s", output.ExitStatus, output.Stderr)
		}
	}

	devCtx := DevContext{
		ExecContext:    eCtx,
		Worktree:       worktree,
		RepoConfig:     repoConfig,
		AdvisorEnabled: configOverrides.IsAdvisorEnabled(),
	}

	// Fetch and store git user config for commit authorship. This must run
	// against the local env (the developer's machine) rather than the flow's
	// env, since containerized environments (devpod/open shell) generally lack
	// the developer's git identity and we want commits attributed to them.
	if v := workflow.GetVersion(ctx, "git-user-config-in-global-state", workflow.DefaultVersion, 1); v >= 1 {
		var gitUserConfig git.GitUserConfig
		err = workflow.ExecuteActivity(env.WithPinnedEnv(ctx), git.GetGitUserConfigActivity, *tempLocalExecContext.EnvContainer).Get(ctx, &gitUserConfig)
		if err != nil {
			// Log but don't fail - commit authorship falls back to git config lookup
			log.Warn().Err(err).Msg("Failed to get git user config, will fall back to git config lookup")
		} else {
			eCtx.GlobalState.SetValue("committerName", gitUserConfig.Name)
			eCtx.GlobalState.SetValue("committerEmail", gitUserConfig.Email)
		}
	}

	if startBranch != nil && *startBranch != "" {
		eCtx.GlobalState.SetValue(common.KeyCurrentTargetBranch, *startBranch)
	} else if v := workflow.GetVersion(ctx, "target-branch-worktree-only-default", workflow.DefaultVersion, 1); v >= 1 {
		if envType == string(env.EnvTypeLocalGitWorktree) {
			eCtx.GlobalState.SetValue(common.KeyCurrentTargetBranch, "main")
		}
	} else {
		eCtx.GlobalState.SetValue(common.KeyCurrentTargetBranch, "main")
	}

	return devCtx, nil
}

// stopActiveDevRun stops any active Dev Run for the workflow (best-effort, for cleanup).
// Only runs for workflows that support Dev Run (version check for replay compatibility).
func stopActiveDevRun(dCtx DevContext) {
	if dCtx.Worktree == nil {
		return
	}

	// Version gate: only stop Dev Run for new workflows to avoid replay nondeterminism
	v := workflow.GetVersion(dCtx, "dev-run-cleanup", workflow.DefaultVersion, 1)
	if v < 1 {
		return
	}

	// Retrieve Dev Run entry from GlobalState
	entry := GetDevRunEntry(dCtx.ExecContext.GlobalState)
	if entry == nil {
		return
	}

	flowInfo := workflow.GetInfo(dCtx)

	// Stop all active dev run instances
	for commandId, instance := range entry {
		devRunCtx := DevRunContext{
			DevRunId:     instance.DevRunId,
			WorkspaceId:  dCtx.WorkspaceId,
			FlowId:       flowInfo.WorkflowExecution.ID,
			WorktreeDir:  dCtx.EnvContainer.Env.GetWorkingDirectory(),
			SourceBranch: dCtx.Worktree.Name,
		}
		var dra *DevRunActivities
		var stopOutput StopDevRunOutput
		err := workflow.ExecuteActivity(dCtx, dra.StopDevRun, StopDevRunInput{
			DevRunConfig: dCtx.RepoConfig.DevRun,
			CommandId:    commandId,
			Context:      devRunCtx,
			Instance:     instance,
		}).Get(dCtx, &stopOutput)
		if err != nil {
			workflow.GetLogger(dCtx).Warn("Failed to stop Dev Run during cleanup", "commandId", commandId, "error", err)
		}
	}

	// Clear stored Dev Run state
	ClearDevRunEntry(dCtx.ExecContext.GlobalState)
}

// cleanup on cancel for resources created during setupDevContextAction
func handleFlowCancel(dCtx DevContext) {
	if !errors.Is(dCtx.Err(), workflow.ErrCanceled) {
		return
	}
	// Use disconnected context to ensure cleanup can complete during
	// cancellation. It is pinned since a switch cancelled mid-flight would
	// otherwise hold back cleanup of the very envs it was working with.
	disconnectedCtx, _ := workflow.NewDisconnectedContext(dCtx)
	disconnectedCtx = env.WithPinnedEnv(disconnectedCtx)

	_ = signalWorkflowClosure(disconnectedCtx, "canceled")

	// Stop any active Dev Run before worktree cleanup (version gated for replay compatibility)
	if dCtx.Worktree != nil {
		v := workflow.GetVersion(disconnectedCtx, "dev-run-cleanup", workflow.DefaultVersion, 1)
		if v >= 1 {
			entry := GetDevRunEntry(dCtx.ExecContext.GlobalState)
			if entry != nil {
				flowInfo := workflow.GetInfo(dCtx)
				for commandId, instance := range entry {
					devRunCtx := DevRunContext{
						DevRunId:     instance.DevRunId,
						WorkspaceId:  dCtx.WorkspaceId,
						FlowId:       flowInfo.WorkflowExecution.ID,
						WorktreeDir:  dCtx.EnvContainer.Env.GetWorkingDirectory(),
						SourceBranch: dCtx.Worktree.Name,
					}
					var dra *DevRunActivities
					var stopOutput StopDevRunOutput
					err := workflow.ExecuteActivity(disconnectedCtx, dra.StopDevRun, StopDevRunInput{
						DevRunConfig: dCtx.RepoConfig.DevRun,
						CommandId:    commandId,
						Context:      devRunCtx,
						Instance:     instance,
					}).Get(disconnectedCtx, &stopOutput)
					if err != nil {
						workflow.GetLogger(dCtx).Warn("Failed to stop Dev Run during workflow cancellation", "commandId", commandId, "error", err)
					}
				}
				ClearDevRunEntry(dCtx.ExecContext.GlobalState)
			}
		}
	}

	if dCtx.Worktree != nil {
		// If hibernated, wake the worktree first so CleanupWorktreeActivity can operate on it.
		// The explicit WakeWorktreeActivity call (rather than relying on auto-wake)
		// is necessary here because CleanupWorktreeActivity needs a functional
		// worktree and we're in a cancellation context where auto-wake may not
		// trigger in time.
		val := dCtx.ExecContext.GlobalState.GetValue(globalStateKeyHibernated)
		hibernated, _ := val.(bool)
		if hibernated {
			var wakeOutput git.WakeWorktreeOutput
			err := workflow.ExecuteActivity(disconnectedCtx, git.WakeWorktreeActivity, git.WakeWorktreeInput{
				EnvContainer: *dCtx.EnvContainer,
			}).Get(disconnectedCtx, &wakeOutput)
			if err != nil {
				workflow.GetLogger(dCtx).Error("Failed to wake hibernated worktree during cancellation", "error", err)
			}
			dCtx.ExecContext.GlobalState.SetValue(globalStateKeyHibernated, nil)
		}

		future := workflow.ExecuteActivity(disconnectedCtx, git.CleanupWorktreeActivity, dCtx.EnvContainer, dCtx.EnvContainer.Env.GetWorkingDirectory(), dCtx.Worktree.Name, "Sidekick task cancelled")
		if err := future.Get(disconnectedCtx, nil); err != nil {
			workflow.GetLogger(dCtx).Error("Failed to cleanup worktree during workflow cancellation", "error", err, "worktree", dCtx.Worktree.Name)
		}
	}

	if dCtx.EnvContainer != nil {
		if sandboxEnv, ok := dCtx.EnvContainer.Env.(env.SandboxEnv); ok {
			envType := sandboxEnv.GetType()
			sandboxName := sandboxEnv.GetSandboxName()
			if dCtx.Worktree != nil {
				// With the worktree gone there is nothing to resume later, so
				// delete rather than stop (they only differ for providers
				// with a stop-without-delete lifecycle).
				err := workflow.ExecuteActivity(disconnectedCtx, env.DeleteSandboxActivity, env.DeleteSandboxInput{
					EnvType:     envType,
					SandboxName: sandboxName,
				}).Get(disconnectedCtx, nil)
				if err != nil {
					workflow.GetLogger(dCtx).Error("Failed to delete sandbox during cancellation", "envType", envType, "error", err)
				}
			} else {
				err := workflow.ExecuteActivity(disconnectedCtx, env.StopSandboxActivity, env.StopSandboxInput{
					EnvType:     envType,
					SandboxName: sandboxName,
				}).Get(disconnectedCtx, nil)
				if err != nil {
					workflow.GetLogger(dCtx).Error("Failed to stop sandbox during cancellation", "envType", envType, "error", err)
				}
			}
		}
	}

	cleanupRetiredEnvs(disconnectedCtx, dCtx, "Sidekick task cancelled")
}

// cleanupRetiredEnvs cleans up the envs the flow switched away from, plus the
// target of a switch still in progress, by the same rules as the active env.
// Failures are logged rather than returned so they never block the flow from
// ending.
func cleanupRetiredEnvs(ctx workflow.Context, dCtx DevContext, reason string) {
	entries := retiredEnvsToCleanUp(dCtx)
	if len(entries) == 0 {
		return
	}
	if workflow.GetVersion(ctx, "retired-env-cleanup", workflow.DefaultVersion, 1) < 1 {
		return
	}
	// Retired envs are cleaned up as recorded, never redirected to the flow
	// env, and with bounded retries since they may already be gone.
	ctx = utils.DefaultRetryCtx(env.WithPinnedEnv(ctx))
	for _, entry := range entries {
		deleteSandbox := entry.Worktree != nil || dCtx.Worktree != nil
		cleanupRetiredEnv(ctx, entry, reason, deleteSandbox)
	}
}

// retiredEnvsToCleanUp excludes the active env, which is cleaned up
// separately, and lists each env once even if it was both retired and the
// target of the in-progress switch.
func retiredEnvsToCleanUp(dCtx DevContext) []retiredEnv {
	if dCtx.GlobalState == nil {
		return nil
	}
	state := newFlowEnvState(dCtx.GlobalState)
	candidates := slices.Clone(state.retiredEnvs())
	if inProgress := state.envSwitch(); inProgress != nil && inProgress.Target != nil {
		candidates = append(candidates, retiredEnv{EnvContainer: *inProgress.Target, Worktree: inProgress.TargetWorktree})
	}

	seen := map[env.EnvInstance]bool{}
	if dCtx.EnvContainer != nil && dCtx.EnvContainer.Env != nil {
		seen[env.InstanceOf(*dCtx.EnvContainer)] = true
	}
	var entries []retiredEnv
	for _, candidate := range candidates {
		if candidate.EnvContainer.Env == nil {
			continue
		}
		instance := env.InstanceOf(candidate.EnvContainer)
		if seen[instance] {
			continue
		}
		seen[instance] = true
		entries = append(entries, candidate)
	}
	return entries
}

func cleanupRetiredEnv(ctx workflow.Context, entry retiredEnv, reason string, deleteSandbox bool) {
	logger := workflow.GetLogger(ctx)
	container := entry.EnvContainer
	envType := container.Env.GetType()
	workingDir := container.Env.GetWorkingDirectory()

	if entry.Worktree != nil && !entry.Stopped {
		err := workflow.ExecuteActivity(ctx, git.CleanupWorktreeActivity, container, workingDir, entry.Worktree.Name, reason).Get(ctx, nil)
		if err != nil {
			logger.Warn("Failed to cleanup retired env worktree", "envType", envType, "workingDir", workingDir, "error", err)
		}
	}

	sandboxEnv, ok := container.Env.(env.SandboxEnv)
	if !ok {
		return
	}
	sandboxName := sandboxEnv.GetSandboxName()
	var err error
	if deleteSandbox {
		err = workflow.ExecuteActivity(ctx, env.DeleteSandboxActivity, env.DeleteSandboxInput{
			EnvType:     envType,
			SandboxName: sandboxName,
		}).Get(ctx, nil)
	} else {
		err = workflow.ExecuteActivity(ctx, env.StopSandboxActivity, env.StopSandboxInput{
			EnvType:     envType,
			SandboxName: sandboxName,
		}).Get(ctx, nil)
	}
	if err != nil {
		logger.Warn("Failed to cleanup retired env sandbox", "envType", envType, "sandboxName", sandboxName, "error", err)
	}
}

// resolvedConfigs holds the configuration resolved for a workspace flow, with
// providers and model selectors restricted to the workspace's profile.
type resolvedConfigs struct {
	LocalConfig     common.LocalPublicConfig
	WorkspaceConfig domain.WorkspaceConfig
	LLMConfig       common.LLMConfig
	EmbeddingConfig common.EmbeddingConfig
	ProfileId       string

	// declaredProviders is the provider catalog before profile filtering, which
	// tells providers excluded by the profile apart from undeclared built-in
	// ones when configs are filtered again after per-flow overrides.
	declaredProviders []common.ModelProviderPublicConfig

	// unscopedLLMConfig/unscopedEmbeddingConfig retain the pre-profile-filter
	// model selectors so a mid-flow profile change can re-derive
	// profile-scoped configs from them.
	unscopedLLMConfig       common.LLMConfig
	unscopedEmbeddingConfig common.EmbeddingConfig

	profileFilterEnabled bool
}

func getConfigs(ctx workflow.Context, workspaceId string) (resolvedConfigs, error) {
	var wa *workspace.Activities
	logger := workflow.GetLogger(ctx)

	enableConfigMode := workflow.GetVersion(ctx, "workspace-config-mode", workflow.DefaultVersion, 1) >= 1
	// gated so that replays of workflows started before profiles existed keep
	// their original configs and activity inputs
	enableProfileFiltering := workflow.GetVersion(ctx, "workspace-profile-config-filtering", workflow.DefaultVersion, 1) >= 1

	resolved := resolvedConfigs{ProfileId: defaultWorkspaceProfileId, profileFilterEnabled: enableProfileFiltering}

	localConfigErr := workflow.ExecuteActivity(ctx, common.GetLocalConfig).Get(ctx, &resolved.LocalConfig)
	resolved.declaredProviders = resolved.LocalConfig.Providers

	err := workflow.ExecuteActivity(ctx, wa.GetWorkspaceConfig, workspaceId).Get(ctx, &resolved.WorkspaceConfig)
	if err != nil {
		return resolved, fmt.Errorf("failed to get workspace config: %v", err)
	}

	var configMode string
	if enableConfigMode {
		var ws domain.Workspace
		err = workflow.ExecuteActivity(ctx, wa.GetWorkspace, workspaceId).Get(ctx, &ws)
		if err != nil {
			return resolved, fmt.Errorf("failed to get workspace: %v", err)
		}
		configMode = ws.ConfigMode
		if enableProfileFiltering {
			resolved.ProfileId = ws.EffectiveProfileId()
		}
	} else {
		configMode = "merge"
	}

	if localConfigErr != nil {
		var appErr *temporal.ApplicationError
		if errors.As(localConfigErr, &appErr) {
			switch appErr.Type() {
			case "LocalConfigNotFound":
				if configMode == "local" {
					return resolved, fmt.Errorf("failed to get local config: %v", localConfigErr)
				}
				logger.Info("Local config not found; proceeding with workspace config (mode=" + configMode + ").")
			case "LocalConfigNoDefaults":
				if configMode == "local" {
					return resolved, fmt.Errorf("failed to get local config: %v", localConfigErr)
				}
				workspaceHasDefaults := len(resolved.WorkspaceConfig.LLM.Defaults) > 0 || len(resolved.WorkspaceConfig.Embedding.Defaults) > 0
				if !workspaceHasDefaults {
					return resolved, fmt.Errorf("no default models configured in local and workspace configs; configure defaults in one source or switch config mode")
				}
				logger.Info("Local config lacks defaults; proceeding with workspace defaults (mode=" + configMode + ").")
			default:
				return resolved, fmt.Errorf("failed to get local config: %v", localConfigErr)
			}
		} else {
			return resolved, fmt.Errorf("failed to get local config: %v", localConfigErr)
		}
	}

	switch configMode {
	case "local":
		resolved.LLMConfig = resolved.LocalConfig.LLM
		resolved.EmbeddingConfig = resolved.LocalConfig.Embedding
	case "workspace":
		resolved.LLMConfig = resolved.WorkspaceConfig.LLM
		resolved.EmbeddingConfig = resolved.WorkspaceConfig.Embedding
	default:
		resolved.LLMConfig, resolved.EmbeddingConfig = mergeConfigs(resolved.LocalConfig.LLM, resolved.LocalConfig.Embedding, resolved.WorkspaceConfig.LLM, resolved.WorkspaceConfig.Embedding)
	}

	resolved.unscopedLLMConfig = resolved.LLMConfig
	resolved.unscopedEmbeddingConfig = resolved.EmbeddingConfig

	if !resolved.profileFilterEnabled {
		return resolved, nil
	}

	return applyProfileFilter(resolved)
}

// applyProfileFilter restricts providers and model selectors to those available
// within the workspace's profile.
func applyProfileFilter(resolved resolvedConfigs) (resolvedConfigs, error) {
	providers, llmConfig, embeddingConfig := profileScopedConfig(resolved.ProfileId, resolved.declaredProviders, resolved.LLMConfig, resolved.EmbeddingConfig)
	if len(resolved.LLMConfig.Defaults) > 0 && len(llmConfig.Defaults) == 0 {
		return resolved, fmt.Errorf("no default LLM models are available for profile %q; associate a provider with that profile or change the workspace profile", resolved.ProfileId)
	}

	resolved.LocalConfig.Providers = providers
	resolved.LLMConfig = llmConfig
	resolved.EmbeddingConfig = embeddingConfig

	return resolved, nil
}

// applyConfigOverrides applies per-flow config overrides on top of the resolved
// configuration, then restricts the result to the workspace profile again so
// that overrides cannot reintroduce providers or models from other profiles.
func applyConfigOverrides(resolved resolvedConfigs, configOverrides common.ConfigOverrides) ([]common.ModelProviderPublicConfig, common.LLMConfig, common.EmbeddingConfig) {
	providers := resolved.declaredProviders
	if configOverrides.Providers != nil {
		providers = *configOverrides.Providers
	}
	llmConfig := resolved.LLMConfig
	if configOverrides.LLM != nil {
		llmConfig = *configOverrides.LLM
	}
	embeddingConfig := resolved.EmbeddingConfig
	if configOverrides.Embedding != nil {
		embeddingConfig = *configOverrides.Embedding
	}

	if !resolved.profileFilterEnabled {
		return providers, llmConfig, embeddingConfig
	}

	return profileScopedConfig(resolved.ProfileId, providers, llmConfig, embeddingConfig)
}

// unscopedOverriddenConfigs applies per-flow config overrides to the
// pre-profile-filter configuration, yielding the inputs from which a mid-flow
// profile change re-derives profile-scoped configs.
func unscopedOverriddenConfigs(resolved resolvedConfigs, configOverrides common.ConfigOverrides) ([]common.ModelProviderPublicConfig, common.LLMConfig, common.EmbeddingConfig) {
	providers := resolved.declaredProviders
	if configOverrides.Providers != nil {
		providers = *configOverrides.Providers
	}
	llmConfig := resolved.unscopedLLMConfig
	if configOverrides.LLM != nil {
		llmConfig = *configOverrides.LLM
	}
	embeddingConfig := resolved.unscopedEmbeddingConfig
	if configOverrides.Embedding != nil {
		embeddingConfig = *configOverrides.Embedding
	}
	return providers, llmConfig, embeddingConfig
}

// profileScopedConfig restricts providers and model selectors to the given
// profile. The providers given must be the full declared catalog: model
// selectors referencing a provider missing from it are kept, since built-in
// providers carry no profile association and remain available in every profile.
func profileScopedConfig(profileId string, providers []common.ModelProviderPublicConfig, llmConfig common.LLMConfig, embeddingConfig common.EmbeddingConfig) ([]common.ModelProviderPublicConfig, common.LLMConfig, common.EmbeddingConfig) {
	return common.ProvidersForProfile(providers, profileId),
		common.LLMConfigForProfile(llmConfig, providers, profileId),
		common.EmbeddingConfigForProfile(embeddingConfig, providers, profileId)
}

// mergeConfigs merges local and workspace configurations with workspace config overriding local config
func mergeConfigs(localLLM common.LLMConfig, localEmbedding common.EmbeddingConfig, workspaceLLM common.LLMConfig, workspaceEmbedding common.EmbeddingConfig) (common.LLMConfig, common.EmbeddingConfig) {
	finalLLMConfig := localLLM
	finalEmbeddingConfig := localEmbedding

	if len(workspaceLLM.Defaults) > 0 {
		finalLLMConfig.Defaults = workspaceLLM.Defaults
	}
	for key, models := range workspaceLLM.UseCaseConfigs {
		finalLLMConfig.UseCaseConfigs[key] = models
	}
	if len(workspaceEmbedding.Defaults) > 0 {
		finalEmbeddingConfig.Defaults = workspaceEmbedding.Defaults
	}
	for key, models := range workspaceEmbedding.UseCaseConfigs {
		finalEmbeddingConfig.UseCaseConfigs[key] = models
	}

	return finalLLMConfig, finalEmbeddingConfig
}

type DevActionContext struct {
	DevContext
	ActionType   string
	ActionParams map[string]interface{}
}

func (actionCtx DevActionContext) WithContext(ctx workflow.Context) DevActionContext {
	newActionCtx := actionCtx
	newActionCtx.DevContext = actionCtx.DevContext.WithContext(ctx)
	return newActionCtx
}

func (actionCtx DevActionContext) WithLlmHeartbeatCtx() DevActionContext {
	newActionCtx := actionCtx
	newActionCtx.DevContext = actionCtx.DevContext.WithContext(utils.LlmHeartbeatCtx(actionCtx))
	return newActionCtx
}

func (actionCtx DevActionContext) WithCancelOnPause() DevActionContext {
	ctx, cancel := workflow.WithCancel(actionCtx.Context)
	actionCtx.Context = ctx
	actionCtx.GlobalState.AddCancelFunc(cancel)
	return actionCtx
}

func Track[T any](devActionCtx DevActionContext, f func(trackedCtx DevActionContext, flowAction *domain.FlowAction) (T, error)) (defaultT T, err error) {
	// TODO /gen check if the devContext.State.Paused is true, and if so, wait
	// indefinitely for a temporal signal to resume before continuing
	return flow_action.Track(devActionCtx.FlowActionContext(), func(trackedActionCtx flow_action.ActionContext, flowAction *domain.FlowAction) (T, error) {
		trackedDevActionCtx := devActionCtx
		trackedDevActionCtx.Context = trackedActionCtx.Context
		return f(trackedDevActionCtx, flowAction)
	})
}

func TrackHuman[T any](devActionCtx DevActionContext, f func(trackedCtx DevActionContext, flowAction *domain.FlowAction) (T, error)) (T, error) {
	return flow_action.TrackHuman(devActionCtx.FlowActionContext(), func(trackedActionCtx flow_action.ActionContext, flowAction *domain.FlowAction) (T, error) {
		trackedDevActionCtx := devActionCtx
		trackedDevActionCtx.Context = trackedActionCtx.Context
		return f(trackedDevActionCtx, flowAction)
	})
}

func RunSubflow[T any](dCtx DevContext, subflowType, subflowName string, f func(subflow domain.Subflow) (T, error)) (T, error) {
	return flow_action.TrackSubflow(dCtx.ExecContext, subflowType, subflowName, f)
}

func RunSubflowWithoutResult(dCtx DevContext, subflowType, subflowName string, f func(subflow domain.Subflow) error) (err error) {
	return flow_action.TrackSubflowWithoutResult(dCtx.ExecContext, subflowType, subflowName, f)
}

// WithChildSubflow has been removed. Use RunSubflow or RunSubflowWithoutResult instead.

func (dCtx *DevContext) NewActionContext(actionType string) DevActionContext {
	return DevActionContext{
		DevContext:   *dCtx,
		ActionType:   actionType,
		ActionParams: map[string]interface{}{},
	}
}

func (devActionCtx *DevActionContext) FlowActionContext() flow_action.ActionContext {
	return flow_action.ActionContext{
		ExecContext:  devActionCtx.ExecContext,
		ActionType:   devActionCtx.ActionType,
		ActionParams: devActionCtx.ActionParams,
	}
}

// newTempLocalExecContext constructs a minimal ExecContext suitable for one-off
// LLM operations invoked directly from a workflow (e.g. generating branch names
// or task titles), before the full per-worktree dev context is available. It
// creates a local env rooted at repoDir and wires in the default secret manager
// chain. This context should not be used for long-lived operations. Its
// activities are pinned to the local repo env, which may share its identity
// with the flow env (in-place local mode) but must never follow env switches.
func newTempLocalExecContext(
	ctx workflow.Context,
	workspaceId string,
	repoDir string,
	profileId string,
	providers []common.ModelProviderPublicConfig,
	llmConfig common.LLMConfig,
	embeddingConfig common.EmbeddingConfig,
) (flow_action.ExecContext, error) {
	tempLocalEnv, err := env.NewLocalEnv(context.Background(), env.LocalEnvParams{RepoDir: repoDir})
	if err != nil {
		return flow_action.ExecContext{}, fmt.Errorf("failed to create temp local env: %v", err)
	}
	eCtx := flow_action.ExecContext{
		FlowScope:    &flow_action.FlowScope{},
		Context:      env.WithPinnedEnv(ctx),
		WorkspaceId:  workspaceId,
		EnvContainer: &env.EnvContainer{Env: tempLocalEnv},
		Secrets: &secret_manager.SecretManagerContainer{
			SecretManager: secret_manager.NewProfileSecretManager(profileId),
		},
		Providers:       providers,
		EmbeddingConfig: embeddingConfig,
		GlobalState:     &flow_action.GlobalState{},
	}
	eCtx.SetProfileId(profileId)
	eCtx.SetLLMConfig(llmConfig)
	eCtx.SetProviders(providers)
	eCtx.SetEmbeddingConfig(embeddingConfig)
	return eCtx, nil
}

// defaultWorkspaceProfileId scopes secret resolution and model provider
// selection until the workspace's own profile association has been loaded.
const defaultWorkspaceProfileId = common.DefaultProfileId

// NewTempLocalExecContext is a workflow-facing wrapper around newTempLocalExecContext
// that loads local/workspace configs via activities and applies any overrides.
// Suitable for inline workflow helpers that need a basic ExecContext for LLM calls
// (e.g. branch name or title generation) without the full per-worktree dev context
// setup. It also returns the loaded local and workspace configs so callers that
// need them downstream (e.g. for command permission merging) can avoid repeating
// the same activity calls.
func NewTempLocalExecContext(ctx workflow.Context, workspaceId, repoDir string, configOverrides common.ConfigOverrides) (flow_action.ExecContext, common.LocalPublicConfig, domain.WorkspaceConfig, error) {
	resolved, err := getConfigs(ctx, workspaceId)
	if err != nil {
		return flow_action.ExecContext{}, resolved.LocalConfig, resolved.WorkspaceConfig, err
	}
	providers, llmConfig, embeddingConfig := applyConfigOverrides(resolved, configOverrides)

	eCtx, err := newTempLocalExecContext(ctx, workspaceId, repoDir, resolved.ProfileId, providers, llmConfig, embeddingConfig)
	if err != nil {
		return flow_action.ExecContext{}, resolved.LocalConfig, resolved.WorkspaceConfig, err
	}
	unscopedProviders, unscopedLLMConfig, unscopedEmbeddingConfig := unscopedOverriddenConfigs(resolved, configOverrides)
	persistUnscopedConfigs(eCtx, unscopedProviders, unscopedLLMConfig, unscopedEmbeddingConfig)
	return eCtx, resolved.LocalConfig, resolved.WorkspaceConfig, nil
}
