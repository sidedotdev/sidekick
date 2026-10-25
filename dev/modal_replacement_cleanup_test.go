package dev

import (
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"sidekick/common"
	"sidekick/domain"
	"sidekick/env"
	"sidekick/flow_action"
	"sidekick/utils"
)

type modalReplacementCleanupResult struct {
	Current  env.EnvContainer
	Retired  []retiredEnv
	Canceled bool
	Error    string
}

func modalReplacementCleanupWorkflow(ctx workflow.Context, worktreeMode bool) (modalReplacementCleanupResult, error) {
	ctx = utils.NoRetryCtx(ctx)
	gs := &flow_action.GlobalState{}
	gs.InitValues()
	state := newFlowEnvState(gs)
	container := env.EnvContainer{Env: &env.ModalEnv{
		SandboxName:      "cleanup-predecessor",
		WorkingDirectory: "/root/repo",
		LocalRepoDir:     "/host/repo",
		SSHHost:          "predecessor.host",
		SSHPort:          2222,
	}}
	state.publish(&container)
	state.setEnvSwitch(envSwitchState{From: "modal", To: "modal"})
	dCtx := DevContext{
		ExecContext: flow_action.ExecContext{
			Context:      env.WithEnvTracker(ctx, state),
			GlobalState:  gs,
			EnvContainer: &container,
		},
		RepoConfig: common.RepoConfig{ModalConfig: common.ModalEnvConfig{Memory: 1024}},
	}
	// A flow's repo-mode override can differ from the repository default.
	if worktreeMode {
		dCtx.RepoConfig.RepoMode = string(env.RepoModeInPlace)
		dCtx.Worktree = &domain.Worktree{
			Name:             "side/cleanup",
			WorkingDirectory: "/root/repo",
		}
	} else {
		dCtx.RepoConfig.RepoMode = string(env.RepoModeWorktree)
	}
	if err := workflow.SetQueryHandler(ctx, "cleanupCandidates", func() ([]retiredEnv, error) {
		return retiredEnvsToCleanUp(dCtx), nil
	}); err != nil {
		return modalReplacementCleanupResult{}, err
	}
	lock := workflow.NewMutex(ctx)
	if err := lock.Lock(ctx); err != nil {
		return modalReplacementCleanupResult{}, err
	}
	_, err := switchEnv(dCtx, &dCtx, lock, EnvSettings{
		EnvType: "modal",
		Modal:   common.ModalEnvConfig{Memory: 2048},
	})
	lock.Unlock()
	result := modalReplacementCleanupResult{
		Current:  container,
		Retired:  state.retiredEnvs(),
		Canceled: temporal.IsCanceledError(err),
	}
	if err != nil {
		result.Error = err.Error()
	}
	state.clearEnvSwitch()
	cleanupCtx, cancel := workflow.NewDisconnectedContext(ctx)
	defer cancel()
	cleanupRetiredEnvs(cleanupCtx, dCtx, "replacement failed")
	return result, nil
}

func TestModalReplacementFailureCleansUpSuccessor(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		stage    string
		worktree bool
	}{
		{"snapshot", false},
		{"port forwards", false},
		{"cancellation", false},
		{"snapshot", true},
		{"port forwards", true},
		{"cancellation", true},
	} {
		mode := "in_place"
		if tc.worktree {
			mode = "worktree"
		}
		stage := tc.stage
		t.Run(mode+"/"+stage, func(t *testing.T) {
			t.Parallel()
			wfEnv := newEnvSettingsTestEnv(t)
			successorName := env.ModalReplacementSandboxName("cleanup-predecessor")
			replacement := wfEnv.OnActivity(env.ModalReplaceSandboxActivity, mock.Anything, mock.Anything).
				Return(env.ModalReplaceSandboxOutput{},
					temporal.NewNonRetryableApplicationError("successor "+stage+" failed", "ReplacementFailed", nil)).Once()
			var inProgress []retiredEnv
			if stage == "cancellation" {
				replacement.After(time.Hour)
				wfEnv.RegisterDelayedCallback(func() {
					value, err := wfEnv.QueryWorkflow("cleanupCandidates")
					require.NoError(t, err)
					require.NoError(t, value.Get(&inProgress))
					wfEnv.CancelWorkflow()
				}, time.Second)
			}
			if tc.worktree {
				wfEnv.OnActivity(env.DeleteSandboxActivity, mock.Anything, env.DeleteSandboxInput{
					EnvType:     env.EnvTypeModal,
					SandboxName: successorName,
				}).Return(env.DeleteSandboxOutput{}, nil).Once()
			} else {
				wfEnv.OnActivity(env.StopSandboxActivity, mock.Anything, env.StopSandboxInput{
					EnvType:     env.EnvTypeModal,
					SandboxName: successorName,
				}).Return(env.StopSandboxOutput{}, nil).Once()
			}

			wfEnv.ExecuteWorkflow(modalReplacementCleanupWorkflow, tc.worktree)
			require.NoError(t, wfEnv.GetWorkflowError())
			var result modalReplacementCleanupResult
			require.NoError(t, wfEnv.GetWorkflowResult(&result))
			require.NotEmpty(t, result.Error)
			require.Equal(t, stage == "cancellation", result.Canceled)
			current := result.Current.Env.(*env.ModalEnv)
			require.Equal(t, "cleanup-predecessor", current.SandboxName)
			require.Equal(t, "predecessor.host", current.SSHHost)
			require.Len(t, result.Retired, 1)
			partial := result.Retired[0]
			require.Equal(t, successorName, partial.EnvContainer.Env.(*env.ModalEnv).SandboxName)
			require.Empty(t, partial.EnvContainer.Env.(*env.ModalEnv).SSHHost)
			require.Nil(t, partial.Worktree, "an unconfirmed endpoint must not receive worktree commands")
			if stage == "cancellation" {
				require.Len(t, inProgress, 1, "cleanup must know the target before the activity returns")
				require.Equal(t, successorName, inProgress[0].EnvContainer.Env.(*env.ModalEnv).SandboxName)
			}
		})
	}
}
