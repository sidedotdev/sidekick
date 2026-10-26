package main

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"go.temporal.io/api/history/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/api/workflowservice/v1"
	"google.golang.org/grpc"
)

type checkpointHistoryClient struct {
	workflowservice.WorkflowServiceClient
	fetch func(context.Context, *workflowservice.GetWorkflowExecutionHistoryRequest) (*workflowservice.GetWorkflowExecutionHistoryResponse, error)
}

func (c checkpointHistoryClient) GetWorkflowExecutionHistory(ctx context.Context, req *workflowservice.GetWorkflowExecutionHistoryRequest, _ ...grpc.CallOption) (*workflowservice.GetWorkflowExecutionHistoryResponse, error) {
	return c.fetch(ctx, req)
}

func TestHistoryCacheCheckpointsInterruptedDownload(t *testing.T) {
	t.Parallel()

	for _, fetchErr := range []error{
		context.DeadlineExceeded,
		context.Canceled,
		serviceerror.NewUnavailable("connection lost"),
	} {
		t.Run(fetchErr.Error(), func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			pages := make(map[string]fakeHistoryPage)
			for page := 0; page < 12; page++ {
				token, next := fmt.Sprint(page), fmt.Sprint(page+1)
				if page == 0 {
					token = ""
				}
				if page == 11 {
					next = ""
				}
				pages[token] = fakeHistoryPage{firstEventID: int64(page*2 + 1), count: 2, next: next}
			}
			fetch := fakePageFetcher(pages, nil)
			interrupt := true
			var tokens []string
			svc := checkpointHistoryClient{
				fetch: func(_ context.Context, req *workflowservice.GetWorkflowExecutionHistoryRequest) (*workflowservice.GetWorkflowExecutionHistoryResponse, error) {
					token := string(req.NextPageToken)
					tokens = append(tokens, token)
					if interrupt && token == "10" {
						// The process can be killed while this RPC is in flight.
						cached := loadCachedHistory(dir, "workflow", "run")
						require.NotNil(t, cached, "completed pages must be checkpointed before the download finishes")
						require.NotEmpty(t, cached.events)
						return nil, fetchErr
					}
					return fetch(req.NextPageToken)
				},
			}

			hist, _, err := fetchWorkflowHistoryCached(context.Background(), svc, "default", dir, "workflow", "run")
			require.ErrorIs(t, err, fetchErr)
			require.Nil(t, hist, "an incomplete history must never be replayed")

			cached := loadCachedHistory(dir, "workflow", "run")
			require.NotNil(t, cached)
			require.Len(t, cached.events, 20)
			require.Equal(t, "10", string(cached.resumeToken))

			interrupt = false
			tokens = nil
			hist, resumed, err := fetchWorkflowHistoryCached(context.Background(), svc, "default", dir, "workflow", "run")
			require.NoError(t, err)
			require.True(t, resumed)
			require.Equal(t, []string{"10", "11"}, tokens)
			want := make([]*history.HistoryEvent, 24)
			for i := range want {
				want[i] = &history.HistoryEvent{EventId: int64(i + 1)}
			}
			require.Equal(t, eventIDs(want), eventIDs(hist.Events))
		})
	}
}
