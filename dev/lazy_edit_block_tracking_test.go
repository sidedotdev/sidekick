package dev

import (
	"context"
	"encoding/json"
	"fmt"
	"sidekick/domain"
	"sidekick/env"
	"sidekick/fflag"
	"sidekick/flow_action"
	"sidekick/persisted_ai"
	"sidekick/repotools"
	"sidekick/srv"
	"sidekick/srv/sqlite"
	"sidekick/utils"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

func TestLazyVisibilityTracksFinalBatchResult(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name           string
		ambiguous      int
		retrySuccesses int
		legacy         bool
	}{
		{name: "seven failed retries", ambiguous: 7},
		{name: "mixed retry outcomes", ambiguous: 7, retrySuccesses: 3},
		{name: "all retries succeed", ambiguous: 7, retrySuccesses: 7},
		{name: "no retries"},
		{name: "legacy separate actions", ambiguous: 7, retrySuccesses: 3, legacy: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			storage := sqlite.NewTestSqliteStorage(t, "lazy-edit-tracking")
			service := srv.NewDelegator(storage, srv.NewMemoryStreamer())
			const workspaceID = "ws_lazy_tracking"
			const flowID = "flow_lazy_tracking"
			require.NoError(t, storage.PersistFlow(context.Background(), domain.Flow{
				WorkspaceId: workspaceID,
				Id:          flowID,
				Status:      "in_progress",
			}))

			var suite testsuite.WorkflowTestSuite
			testEnv := suite.NewTestWorkflowEnvironment()
			testEnv.SetWorkerOptions(utils.TestWorkerOptions())
			testEnv.SetStartWorkflowOptions(client.StartWorkflowOptions{ID: flowID})
			testEnv.RegisterActivity(&flow_action.FlowActivities{Service: service})
			testEnv.OnGetVersion("flow-action-id-propagation", workflow.DefaultVersion, 1).
				Return(workflow.DefaultVersion).Maybe()
			if tc.legacy {
				testEnv.OnGetVersion("lazy-edit-block-tracking", workflow.DefaultVersion, 1).
					Return(workflow.DefaultVersion).Maybe()
			}

			var ffa *fflag.FFlagActivities
			testEnv.OnActivity(ffa.EvalBoolFlag, mock.Anything, mock.Anything).
				Return(false, nil).Maybe()

			blocks := make([]EditBlock, 13)
			for i := range blocks {
				blocks[i] = EditBlock{
					SequenceNumber: i + 1,
					FilePath:       "example.go",
					EditType:       "update",
					OldLines:       []string{fmt.Sprintf("old%d", i)},
					NewLines:       []string{fmt.Sprintf("new%d", i)},
				}
			}

			var cha *persisted_ai.ChatHistoryActivities
			if tc.ambiguous > 0 {
				testEnv.OnActivity(cha.ExtractVisibleCodeBlocksForFiles, mock.Anything, mock.Anything).
					Return(persisted_ai.ExtractVisibleCodeBlocksOutput{
						CodeBlocks: []repotools.CodeBlock{{
							FilePath: "example.go", StartLine: 1, EndLine: 100,
						}},
					}, nil).Once()
			}

			var da *DevActivities
			applyCalls := 0
			testEnv.OnActivity(da.ApplyEditBlocks, mock.Anything, mock.Anything).Return(
				func(_ context.Context, input ApplyEditBlockActivityInput) ([]ApplyEditBlockReport, error) {
					applyCalls++
					if applyCalls == 1 {
						require.Len(t, input.EditBlocks, len(blocks))
						reports := make([]ApplyEditBlockReport, len(blocks))
						for i, block := range input.EditBlocks {
							ambiguous := i >= len(blocks)-tc.ambiguous
							reports[i] = ApplyEditBlockReport{
								OriginalEditBlocks: []EditBlock{block},
								DidApply:           !ambiguous,
								AmbiguousMatch:     ambiguous,
							}
							if ambiguous {
								reports[i].Error = "Multiple matches found"
							}
						}
						return reports, nil
					}
					require.Len(t, input.EditBlocks, 1)
					block := input.EditBlocks[0]
					require.NotEmpty(t, block.VisibleFileRanges)
					applied := applyCalls-1 <= tc.retrySuccesses
					report := ApplyEditBlockReport{
						OriginalEditBlocks: []EditBlock{block},
						DidApply:           applied,
						AmbiguousMatch:     !applied,
					}
					if !applied {
						report.Error = "Still ambiguous after visibility filtering"
					}
					return []ApplyEditBlockReport{report}, nil
				},
			).Times(1 + tc.ambiguous)

			testEnv.ExecuteWorkflow(func(ctx workflow.Context) (applyEditBlocksResult, error) {
				dCtx := DevContext{ExecContext: flow_action.ExecContext{
					Context:      utils.NoRetryCtx(ctx),
					WorkspaceId:  workspaceID,
					GlobalState:  &flow_action.GlobalState{},
					EnvContainer: &env.EnvContainer{},
					FlowScope: &flow_action.FlowScope{
						Subflow: &domain.Subflow{FlowId: flowID},
					},
				}}
				return applyEditBlocksWithLazyVisibility(dCtx,
					&persisted_ai.ChatHistoryContainer{History: persisted_ai.NewLlm2ChatHistory("", "")}, blocks)
			})
			require.NoError(t, testEnv.GetWorkflowError())
			testEnv.AssertExpectations(t)

			var result applyEditBlocksResult
			require.NoError(t, testEnv.GetWorkflowResult(&result))
			require.Len(t, result.Reports, len(blocks))
			require.Equal(t, tc.retrySuccesses == tc.ambiguous, result.AllApplied)

			actions, err := storage.GetFlowActions(context.Background(), workspaceID, flowID)
			require.NoError(t, err)
			var editActions []domain.FlowAction
			for _, action := range actions {
				if action.ActionType == "apply_edit_blocks" {
					editActions = append(editActions, action)
				}
			}
			if tc.legacy {
				require.Len(t, editActions, 1+tc.ambiguous)
				return
			}
			require.Len(t, editActions, 1, "internal retries must not create separate edit actions")
			require.Equal(t, domain.ActionStatusComplete, editActions[0].ActionStatus)
			var reports []ApplyEditBlockReport
			require.NoError(t, json.Unmarshal([]byte(editActions[0].ActionResult), &reports))
			require.Len(t, reports, len(blocks))
			for i, report := range reports {
				require.Equal(t, []int{i + 1}, report.SequenceNumbers())
				require.Equal(t, i < len(blocks)-tc.ambiguous+tc.retrySuccesses, report.DidApply)
				require.Equal(t, result.Reports[i].Error, report.Error)
				require.Empty(t, report.OriginalEditBlocks[0].VisibleCodeBlocks)
				require.Empty(t, report.OriginalEditBlocks[0].VisibleFileRanges)
			}
		})
	}
}
