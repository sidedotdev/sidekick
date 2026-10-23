package dev

import (
	"context"
	"sync/atomic"
	"testing"

	"sidekick/coding/git"
	"sidekick/domain"
	"sidekick/env"
	"sidekick/flow_action"
	"sidekick/srv/sqlite"
	"sidekick/utils"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

func TestGenericSubflowsDoNotCheckpointGit(t *testing.T) {
	t.Parallel()

	for _, kind := range []string{
		"edit_code", "commit", "step.dev", "perform_step",
		"resolve_merge_conflicts", "review_and_resolve", "pass_tests",
	} {
		for _, withResult := range []bool{false, true} {
			name := kind + "/without result"
			if withResult {
				name = kind + "/with result"
			}
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				storage := sqlite.NewTestSqliteStorage(t, "subflow_git")
				var suite testsuite.WorkflowTestSuite
				tenv := suite.NewTestWorkflowEnvironment()
				tenv.SetWorkerOptions(utils.TestWorkerOptions())
				tenv.RegisterActivityWithOptions(storage.PersistSubflow, activity.RegisterOptions{Name: "PersistSubflow"})

				var checkpoints atomic.Int32
				tenv.OnActivity(git.SyncGitActivity, mock.Anything, mock.Anything).
					Return(func(context.Context, git.SyncGitInput) (git.SyncGitOutput, error) {
						checkpoints.Add(1)
						return git.SyncGitOutput{}, nil
					}).Maybe()

				var flowID string
				tenv.ExecuteWorkflow(func(ctx workflow.Context) (string, error) {
					flowID = workflow.GetInfo(ctx).WorkflowExecution.ID
					dCtx := DevContext{ExecContext: flow_action.ExecContext{
						Context:     utils.NoRetryCtx(ctx),
						WorkspaceId: "ws_subflow_git",
						FlowScope:   &flow_action.FlowScope{},
						EnvContainer: &env.EnvContainer{Env: &env.ModalEnv{
							WorkingDirectory: "/repo/worktree",
							LocalRepoDir:     "/worker/repo",
							SandboxName:      "sandbox",
						}},
					}}
					if withResult {
						return RunSubflow(dCtx, kind, "Test Subflow", func(domain.Subflow) (string, error) {
							return "result", nil
						})
					}
					err := RunSubflowWithoutResult(dCtx, kind, "Test Subflow", func(domain.Subflow) error {
						return nil
					})
					return "result", err
				})
				require.NoError(t, tenv.GetWorkflowError())
				var result string
				require.NoError(t, tenv.GetWorkflowResult(&result))
				require.Equal(t, "result", result)
				require.Zero(t, checkpoints.Load())

				subflows, err := storage.GetSubflows(context.Background(), "ws_subflow_git", flowID)
				require.NoError(t, err)
				require.Len(t, subflows, 1)
				require.Equal(t, domain.SubflowStatusComplete, subflows[0].Status)
			})
		}
	}
}
