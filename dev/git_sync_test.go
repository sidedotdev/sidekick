package dev

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

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
		name     string
		kind     string
		version  workflow.Version
		local    bool
		fail     bool
		wantSync bool
	}{
		{name: "author code", kind: "edit_code", version: 1, wantSync: true},
		{name: "commit agent", kind: "commit", version: 1, wantSync: true},
		{name: "parent may have deleted its worktree", kind: "coding", version: 1},
		{name: "development step", kind: "step.dev", version: 1, wantSync: true},
		{name: "conflict resolution", kind: "resolve_merge_conflicts", version: 1, wantSync: true},
		{name: "legacy replay", kind: "edit_code", version: workflow.DefaultVersion},
		{name: "read only", kind: "gather_context", version: 1},
		{name: "local environment", kind: "edit_code", version: 1, local: true},
		{name: "backup failure", kind: "edit_code", version: 1, fail: true, wantSync: true},
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
			tenv.OnActivity(env.SyncGitActivity, mock.Anything, mock.Anything).
				Return(func(context.Context, env.SyncGitInput) (env.SyncGitOutput, error) {
					calls.Add(1)
					if tt.fail {
						return env.SyncGitOutput{}, errors.New("backup unavailable")
					}
					return env.SyncGitOutput{}, nil
				}).Maybe()
			tenv.ExecuteWorkflow(func(ctx workflow.Context) error {
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
				return checkpointSubflowGit(dCtx, tt.kind)
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
