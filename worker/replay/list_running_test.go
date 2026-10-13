package main

import (
	"context"
	"net"
	"testing"
	"time"

	"sidekick/temporal"

	"github.com/stretchr/testify/require"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	workflowpb "go.temporal.io/api/workflow/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"
	"google.golang.org/grpc"
)

type replayVisibilityServer struct {
	workflowservice.UnimplementedWorkflowServiceServer
	list func(context.Context, *workflowservice.ListWorkflowExecutionsRequest) (*workflowservice.ListWorkflowExecutionsResponse, error)
}

func (s *replayVisibilityServer) GetSystemInfo(context.Context, *workflowservice.GetSystemInfoRequest) (*workflowservice.GetSystemInfoResponse, error) {
	return &workflowservice.GetSystemInfoResponse{}, nil
}

func (s *replayVisibilityServer) ListWorkflowExecutions(ctx context.Context, req *workflowservice.ListWorkflowExecutionsRequest) (*workflowservice.ListWorkflowExecutionsResponse, error) {
	resp, err := s.list(ctx, req)
	if err != nil {
		return nil, serviceerror.ToStatus(err).Err()
	}
	return resp, nil
}

func newReplayVisibilityClient(t *testing.T, list func(context.Context, *workflowservice.ListWorkflowExecutionsRequest) (*workflowservice.ListWorkflowExecutionsResponse, error)) client.Client {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	server := grpc.NewServer()
	workflowservice.RegisterWorkflowServiceServer(server, &replayVisibilityServer{list: list})
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)

	proxy, err := temporal.StartReadOnlyProxy("127.0.0.1:0", listener.Addr().String(), nil)
	require.NoError(t, err)
	t.Cleanup(proxy.Stop)

	c, err := client.Dial(client.Options{HostPort: proxy.Addr(), Namespace: client.DefaultNamespace})
	require.NoError(t, err)
	t.Cleanup(c.Close)
	return c
}

func TestListRecentRunningWorkflowsSlowProxy(t *testing.T) {
	t.Parallel()

	c := newReplayVisibilityClient(t, func(ctx context.Context, req *workflowservice.ListWorkflowExecutionsRequest) (*workflowservice.ListWorkflowExecutionsResponse, error) {
		// Exceed the SDK's ten-second ListWorkflow deadline without
		// depending on an external Temporal server or tunnel.
		timer := time.NewTimer(12 * time.Second)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timer.C:
			return &workflowservice.ListWorkflowExecutionsResponse{
				Executions: []*workflowpb.WorkflowExecutionInfo{{
					Execution: &commonpb.WorkflowExecution{WorkflowId: "workflow", RunId: "run"},
					Status:    enums.WORKFLOW_EXECUTION_STATUS_RUNNING,
				}},
			}, nil
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	workflows, err := listRecentRunningWorkflows(ctx, c.WorkflowService(), client.DefaultNamespace, 1)
	require.NoError(t, err)
	require.Equal(t, []listedWorkflow{{
		id:     "workflow",
		runID:  "run",
		status: enums.WORKFLOW_EXECUTION_STATUS_RUNNING,
	}}, workflows)
}

func TestListRecentRunningWorkflowsPagination(t *testing.T) {
	t.Parallel()
	execution := func(id string) *workflowpb.WorkflowExecutionInfo {
		return &workflowpb.WorkflowExecutionInfo{
			Execution: &commonpb.WorkflowExecution{WorkflowId: id, RunId: "run-" + id},
			Status:    enums.WORKFLOW_EXECUTION_STATUS_RUNNING,
		}
	}
	tests := []struct {
		name      string
		limit     int
		pages     []*workflowservice.ListWorkflowExecutionsResponse
		pageSizes []int32
		wantIDs   []string
	}{
		{
			name:  "multiple pages",
			limit: 3,
			pages: []*workflowservice.ListWorkflowExecutionsResponse{
				{Executions: []*workflowpb.WorkflowExecutionInfo{execution("a")}, NextPageToken: []byte("next")},
				{Executions: []*workflowpb.WorkflowExecutionInfo{execution("b"), execution("c")}},
			},
			pageSizes: []int32{3, 2},
			wantIDs:   []string{"a", "b", "c"},
		},
		{
			name:  "empty intermediate page",
			limit: 3,
			pages: []*workflowservice.ListWorkflowExecutionsResponse{
				{NextPageToken: []byte("next")},
				{Executions: []*workflowpb.WorkflowExecutionInfo{execution("a")}},
			},
			pageSizes: []int32{3, 3},
			wantIDs:   []string{"a"},
		},
		{
			name:  "stop at limit even if server returns more",
			limit: 1,
			pages: []*workflowservice.ListWorkflowExecutionsResponse{
				{Executions: []*workflowpb.WorkflowExecutionInfo{execution("a"), execution("b")}, NextPageToken: []byte("unused")},
			},
			pageSizes: []int32{1},
			wantIDs:   []string{"a"},
		},
		{
			name:      "cap large requests",
			limit:     1000,
			pages:     []*workflowservice.ListWorkflowExecutionsResponse{{}},
			pageSizes: []int32{100},
		},
		{
			name:  "zero limit",
			limit: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			requests := make(chan *workflowservice.ListWorkflowExecutionsRequest, len(tt.pages)+1)
			c := newReplayVisibilityClient(t, func(_ context.Context, req *workflowservice.ListWorkflowExecutionsRequest) (*workflowservice.ListWorkflowExecutionsResponse, error) {
				page := len(requests)
				requests <- req
				if page >= len(tt.pages) {
					return nil, serviceerror.NewInvalidArgument("unexpected page request")
				}
				return tt.pages[page], nil
			})

			got, err := listRecentRunningWorkflows(context.Background(), c.WorkflowService(), client.DefaultNamespace, tt.limit)
			require.NoError(t, err)
			require.Len(t, got, len(tt.wantIDs))
			for i, id := range tt.wantIDs {
				require.Equal(t, listedWorkflow{
					id: id, runID: "run-" + id, status: enums.WORKFLOW_EXECUTION_STATUS_RUNNING,
				}, got[i])
			}
			require.Len(t, requests, len(tt.pageSizes))
			for i, size := range tt.pageSizes {
				req := <-requests
				require.Equal(t, client.DefaultNamespace, req.Namespace)
				require.Equal(t, "ExecutionStatus = 'Running'", req.Query)
				require.Equal(t, size, req.PageSize)
				if i == 0 {
					require.Empty(t, req.NextPageToken)
				} else {
					require.Equal(t, tt.pages[i-1].NextPageToken, req.NextPageToken)
				}
			}
		})
	}
}

func TestListRecentRunningWorkflowsCancellation(t *testing.T) {
	t.Parallel()
	started := make(chan struct{})
	c := newReplayVisibilityClient(t, func(ctx context.Context, _ *workflowservice.ListWorkflowExecutionsRequest) (*workflowservice.ListWorkflowExecutionsResponse, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, err := listRecentRunningWorkflows(ctx, c.WorkflowService(), client.DefaultNamespace, 1)
		result <- err
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("listing never reached the server")
	}
	cancel()
	select {
	case err := <-result:
		require.Error(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("listing did not stop after cancellation")
	}
}

func TestListRecentRunningWorkflowsError(t *testing.T) {
	t.Parallel()
	c := newReplayVisibilityClient(t, func(context.Context, *workflowservice.ListWorkflowExecutionsRequest) (*workflowservice.ListWorkflowExecutionsResponse, error) {
		return nil, serviceerror.NewPermissionDenied("visibility denied", "")
	})
	got, err := listRecentRunningWorkflows(context.Background(), c.WorkflowService(), client.DefaultNamespace, 1)
	require.Nil(t, got)
	var denied *serviceerror.PermissionDenied
	require.ErrorAs(t, err, &denied)
}
