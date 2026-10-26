package main

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/api/enums/v1"
	historypb "go.temporal.io/api/history/v1"
	workflowpb "go.temporal.io/api/workflow/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"
)

func TestVisitRunningWorkflowCandidatesFiltersBeforeLimit(t *testing.T) {
	t.Parallel()

	output, err := exec.Command("git", "rev-parse", "--verify", "HEAD").Output()
	require.NoError(t, err)
	head := strings.TrimSpace(string(output))

	for _, tt := range []struct {
		name        string
		limit       int
		wantVisited []string
		wantReplay  []string
		wantPages   int
	}{
		{
			name:        "ineligible histories do not consume replay slots",
			limit:       2,
			wantVisited: []string{"dirty-1", "dirty-2", "clean-1", "dirty-3", "clean-2"},
			wantReplay:  []string{"clean-1", "clean-2"},
			wantPages:   2,
		},
		{
			name:        "stop at first eligible history",
			limit:       1,
			wantVisited: []string{"dirty-1", "dirty-2", "clean-1"},
			wantReplay:  []string{"clean-1"},
			wantPages:   2,
		},
		{
			name:        "exhaust visibility when too few eligible histories",
			limit:       10,
			wantVisited: []string{"dirty-1", "dirty-2", "clean-1", "dirty-3", "clean-2", "clean-3"},
			wantReplay:  []string{"clean-1", "clean-2", "clean-3"},
			wantPages:   2,
		},
		{name: "zero limit"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			pages := [][]string{
				{"dirty-1", "dirty-2"},
				{"clean-1", "dirty-3", "clean-2", "clean-3"},
			}
			requests := 0
			c := newReplayVisibilityClient(t, func(_ context.Context, req *workflowservice.ListWorkflowExecutionsRequest) (*workflowservice.ListWorkflowExecutionsResponse, error) {
				page := requests
				requests++
				if page >= len(pages) {
					t.Error("requested a page after visibility was exhausted")
					return nil, context.Canceled
				}
				if page == 0 {
					require.Empty(t, req.NextPageToken)
				} else {
					require.Equal(t, []byte("next"), req.NextPageToken)
				}
				response := &workflowservice.ListWorkflowExecutionsResponse{}
				for _, id := range pages[page] {
					response.Executions = append(response.Executions, &workflowpb.WorkflowExecutionInfo{
						Execution: &commonpb.WorkflowExecution{WorkflowId: id, RunId: "run-" + id},
						Status:    enums.WORKFLOW_EXECUTION_STATUS_RUNNING,
					})
				}
				if page == 0 {
					response.NextPageToken = []byte("next")
				}
				return response, nil
			})

			var visited, replayed []string
			err := visitRunningWorkflowCandidates(context.Background(), c.WorkflowService(), client.DefaultNamespace, tt.limit, func(wf listedWorkflow) bool {
				visited = append(visited, wf.id)
				buildID := head
				if strings.HasPrefix(wf.id, "dirty-") {
					buildID += "-dirty"
				}
				hist := &historypb.History{Events: []*historypb.HistoryEvent{{
					EventType: enums.EVENT_TYPE_WORKFLOW_TASK_COMPLETED,
					Attributes: &historypb.HistoryEvent_WorkflowTaskCompletedEventAttributes{
						WorkflowTaskCompletedEventAttributes: &historypb.WorkflowTaskCompletedEventAttributes{
							WorkerVersion: &commonpb.WorkerVersionStamp{BuildId: buildID},
						},
					},
				}}}
				if replayHistorySkipReason(head, hist) != "" {
					return false
				}
				replayed = append(replayed, wf.id)
				return true
			})
			require.NoError(t, err)
			require.Equal(t, tt.wantVisited, visited)
			require.Equal(t, tt.wantReplay, replayed)
			require.Equal(t, tt.wantPages, requests)
		})
	}
}
