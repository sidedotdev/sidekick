package dev

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"sidekick/coding/git"
	"sidekick/env"
	"sidekick/flow_action"
	"sidekick/utils"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

func TestSubflowGitCheckpoint(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		version     workflow.Version
		local       bool
		noContainer bool
		canceled    bool
		fail        bool
		wantSync    bool
	}{
		{name: "explicit checkpoint", version: 1, wantSync: true},
		{name: "legacy replay", version: workflow.DefaultVersion},
		{name: "local environment", version: 1, local: true},
		{name: "no environment", version: 1, noContainer: true},
		{name: "canceled subflow", version: 1, canceled: true, wantSync: true},
		{name: "backup failure", version: 1, fail: true, wantSync: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var suite testsuite.WorkflowTestSuite
			tenv := suite.NewTestWorkflowEnvironment()
			tenv.SetWorkerOptions(utils.TestWorkerOptions())
			tenv.OnGetVersion("subflow-git-checkpoint", workflow.DefaultVersion, 1).
				Return(tt.version).Maybe()
			var calls atomic.Int32
			tenv.OnActivity(git.SyncGitActivity, mock.Anything, mock.Anything).
				Return(func(context.Context, git.SyncGitInput) (git.SyncGitOutput, error) {
					calls.Add(1)
					if tt.fail {
						return git.SyncGitOutput{}, errors.New("backup unavailable")
					}
					return git.SyncGitOutput{}, nil
				}).Maybe()
			tenv.ExecuteWorkflow(func(ctx workflow.Context) error {
				if tt.canceled {
					var cancel workflow.CancelFunc
					ctx, cancel = workflow.WithCancel(ctx)
					cancel()
				}
				var e env.Env = &env.ModalEnv{
					WorkingDirectory: "/repo/worktree",
					LocalRepoDir:     "/worker/repo",
					SandboxName:      "sandbox",
				}
				if tt.local {
					e = &env.LocalEnv{WorkingDirectory: "/repo"}
				}
				dCtx := DevContext{ExecContext: flow_action.ExecContext{
					Context:      utils.NoRetryCtx(ctx),
					EnvContainer: &env.EnvContainer{Env: e},
				}}
				if tt.noContainer {
					dCtx.EnvContainer = nil
				}
				return checkpointSubflowGit(dCtx)
			})
			if tt.fail {
				require.ErrorContains(t, tenv.GetWorkflowError(), "backup unavailable")
			} else {
				require.NoError(t, tenv.GetWorkflowError())
			}
			if tt.wantSync {
				require.EqualValues(t, 1, calls.Load())
			} else {
				require.Zero(t, calls.Load())
			}
		})
	}
}
