package dev

import (
	"context"
	"testing"

	"sidekick/domain"
	"sidekick/flow_action"
	"sidekick/srv/sqlite"
	"sidekick/utils"

	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

func TestEnsurePrerequisitesPersistence(t *testing.T) {
	t.Parallel()

	for _, legacy := range []bool{false, true} {
		name := "new workflows skip the placeholder"
		if legacy {
			name = "legacy workflows retain the placeholder"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			storage := sqlite.NewTestSqliteStorage(t, "prerequisites")
			var suite testsuite.WorkflowTestSuite
			env := suite.NewTestWorkflowEnvironment()
			env.SetWorkerOptions(utils.TestWorkerOptions())
			env.RegisterActivityWithOptions(storage.PersistSubflow, activity.RegisterOptions{Name: "PersistSubflow"})
			if legacy {
				env.OnGetVersion("skip-empty-prerequisites", workflow.DefaultVersion, 1).
					Return(workflow.DefaultVersion).Once()
			}

			testWorkflow := func(ctx workflow.Context) (string, error) {
				dCtx := DevContext{ExecContext: flow_action.ExecContext{
					Context:     utils.NoRetryCtx(ctx),
					WorkspaceId: "ws_prerequisites",
					FlowScope:   &flow_action.FlowScope{},
				}}
				return workflow.GetInfo(ctx).WorkflowExecution.ID, EnsurePrerequisites(dCtx)
			}
			env.ExecuteWorkflow(testWorkflow)
			require.True(t, env.IsWorkflowCompleted())
			require.NoError(t, env.GetWorkflowError())

			var flowID string
			require.NoError(t, env.GetWorkflowResult(&flowID))
			subflows, err := storage.GetSubflows(context.Background(), "ws_prerequisites", flowID)
			require.NoError(t, err)
			if legacy {
				require.Len(t, subflows, 1)
				require.Equal(t, "Ensure Prerequisites", subflows[0].Name)
				require.NotNil(t, subflows[0].Type)
				require.Equal(t, "prereqs", *subflows[0].Type)
				require.Equal(t, domain.SubflowStatusComplete, subflows[0].Status)
			} else {
				require.Empty(t, subflows)
			}
			env.AssertExpectations(t)
		})
	}
}
