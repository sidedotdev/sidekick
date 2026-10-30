package main

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/api/enums/v1"
	"go.temporal.io/api/history/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/api/workflowservice/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// The newest completed task determines eligibility. Reading from the end avoids
// downloading large payloads for histories that cannot be replayed by this build.
// The full history must still be checked because the run can advance meanwhile.
func replayHistoryPreflight(ctx context.Context, svc workflowservice.WorkflowServiceClient, namespace, workflowID, runID, currentRef string) (string, error) {
	var token []byte
	for {
		pageCtx, cancel := context.WithTimeout(ctx, historyPageTimeout)
		resp, err := svc.GetWorkflowExecutionHistoryReverse(pageCtx, &workflowservice.GetWorkflowExecutionHistoryReverseRequest{
			Namespace:       namespace,
			Execution:       &commonpb.WorkflowExecution{WorkflowId: workflowID, RunId: runID},
			MaximumPageSize: 100,
			NextPageToken:   token,
		})
		cancel()
		if err != nil {
			var unsupported *serviceerror.Unimplemented
			if errors.As(err, &unsupported) || status.Code(err) == codes.Unimplemented {
				return "", nil
			}
			return "", err
		}
		for _, event := range resp.GetHistory().GetEvents() {
			if event.GetEventType() == enums.EVENT_TYPE_WORKFLOW_TASK_COMPLETED {
				buildID := event.GetWorkflowTaskCompletedEventAttributes().GetWorkerVersion().GetBuildId()
				return replayBuildSkipReason(currentRef, buildID), nil
			}
		}
		token = resp.GetNextPageToken()
		if len(token) == 0 {
			return "history has no completed workflow task", nil
		}
	}
}

type preflightHistoryClient struct {
	workflowservice.WorkflowServiceClient
	pages        []*history.History
	forwardCalls int
	reverseCalls int
	tokens       []string
	err          error
}

func (c *preflightHistoryClient) GetWorkflowExecutionHistory(_ context.Context, _ *workflowservice.GetWorkflowExecutionHistoryRequest, _ ...grpc.CallOption) (*workflowservice.GetWorkflowExecutionHistoryResponse, error) {
	c.forwardCalls++
	return nil, errors.New("unexpected full-history fetch")
}

func (c *preflightHistoryClient) GetWorkflowExecutionHistoryReverse(_ context.Context, req *workflowservice.GetWorkflowExecutionHistoryReverseRequest, _ ...grpc.CallOption) (*workflowservice.GetWorkflowExecutionHistoryReverseResponse, error) {
	c.tokens = append(c.tokens, string(req.NextPageToken))
	c.reverseCalls++
	if c.err != nil {
		return nil, c.err
	}
	resp := &workflowservice.GetWorkflowExecutionHistoryReverseResponse{History: c.pages[c.reverseCalls-1]}
	if c.reverseCalls < len(c.pages) {
		resp.NextPageToken = []byte("older")
	}
	return resp, nil
}

func TestReplayHistoryPreflightReadsOnlyTail(t *testing.T) {
	t.Parallel()

	output, err := exec.Command("git", "rev-parse", "HEAD").Output()
	require.NoError(t, err)
	head := strings.TrimSpace(string(output))
	completed := func(build string) *history.HistoryEvent {
		return &history.HistoryEvent{
			EventType: enums.EVENT_TYPE_WORKFLOW_TASK_COMPLETED,
			Attributes: &history.HistoryEvent_WorkflowTaskCompletedEventAttributes{
				WorkflowTaskCompletedEventAttributes: &history.WorkflowTaskCompletedEventAttributes{
					WorkerVersion: &commonpb.WorkerVersionStamp{BuildId: build},
				},
			},
		}
	}
	started := &history.HistoryEvent{
		EventType: enums.EVENT_TYPE_WORKFLOW_TASK_STARTED,
		Attributes: &history.HistoryEvent_WorkflowTaskStartedEventAttributes{
			WorkflowTaskStartedEventAttributes: &history.WorkflowTaskStartedEventAttributes{
				WorkerVersion: &commonpb.WorkerVersionStamp{BuildId: head + "-dirty"},
			},
		},
	}
	for _, tt := range []struct {
		name       string
		pages      []*history.History
		wantReason string
		wantTokens []string
	}{
		{
			name: "eligible completed task despite dirty in-flight worker",
			pages: []*history.History{
				{Events: []*history.HistoryEvent{started, completed(head)}},
				{Events: []*history.HistoryEvent{completed(head + "-dirty")}},
			},
			wantTokens: []string{""},
		},
		{
			name: "dirty completed task despite eligible older worker",
			pages: []*history.History{
				{Events: []*history.HistoryEvent{completed(head + "-dirty")}},
				{Events: []*history.HistoryEvent{completed(head)}},
			},
			wantReason: "uncommitted code",
			wantTokens: []string{""},
		},
		{
			name: "completed task on older page",
			pages: []*history.History{
				{Events: []*history.HistoryEvent{started}},
				{Events: []*history.HistoryEvent{completed(head)}},
			},
			wantTokens: []string{"", "older"},
		},
		{
			name:       "no completed task",
			pages:      []*history.History{{Events: []*history.HistoryEvent{started}}},
			wantReason: "no completed workflow task",
			wantTokens: []string{""},
		},
		{
			name:       "missing worker version",
			pages:      []*history.History{{Events: []*history.HistoryEvent{completed("")}}},
			wantReason: "missing",
			wantTokens: []string{""},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			svc := &preflightHistoryClient{pages: tt.pages}
			reason, err := replayHistoryPreflight(context.Background(), svc, "default", "workflow", "run", head)
			require.NoError(t, err)
			if tt.wantReason == "" {
				require.Empty(t, reason)
			} else {
				require.Contains(t, reason, tt.wantReason)
			}
			require.Zero(t, svc.forwardCalls, "eligibility must not download the full history")
			require.Equal(t, tt.wantTokens, svc.tokens)
		})
	}
}

func TestReplayHistoryPreflightErrors(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name     string
		err      error
		fallback bool
	}{
		{name: "unsupported server", err: serviceerror.NewUnimplemented("unsupported"), fallback: true},
		{name: "unsupported gRPC endpoint", err: status.Error(codes.Unimplemented, "unsupported"), fallback: true},
		{name: "canceled", err: context.Canceled},
		{name: "deadline", err: context.DeadlineExceeded},
		{name: "transport failure", err: serviceerror.NewUnavailable("connection lost")},
		{name: "permission denied", err: status.Error(codes.PermissionDenied, "forbidden")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			svc := &preflightHistoryClient{err: tt.err}
			reason, err := replayHistoryPreflight(context.Background(), svc, "default", "workflow", "run", "HEAD")
			require.Empty(t, reason)
			if tt.fallback {
				require.NoError(t, err, "unsupported servers must fall back to fetching full history")
			} else {
				require.ErrorIs(t, err, tt.err)
			}
			require.Equal(t, 1, svc.reverseCalls)
			require.Zero(t, svc.forwardCalls)
		})
	}
}
