package temporal

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"

	"sidekick/common"
	"sidekick/srv/sqlite"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	commonpb "go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"
	historypb "go.temporal.io/api/history/v1"
	"go.temporal.io/api/serviceerror"
	workflowpb "go.temporal.io/api/workflow/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
	"google.golang.org/grpc"
)

type fakeWorkflowService struct {
	workflowservice.UnimplementedWorkflowServiceServer
	history *historypb.History
}

func (s *fakeWorkflowService) GetSystemInfo(ctx context.Context, req *workflowservice.GetSystemInfoRequest) (*workflowservice.GetSystemInfoResponse, error) {
	return &workflowservice.GetSystemInfoResponse{}, nil
}

func (s *fakeWorkflowService) ListWorkflowExecutions(ctx context.Context, req *workflowservice.ListWorkflowExecutionsRequest) (*workflowservice.ListWorkflowExecutionsResponse, error) {
	return &workflowservice.ListWorkflowExecutionsResponse{
		Executions: []*workflowpb.WorkflowExecutionInfo{{
			Execution: &commonpb.WorkflowExecution{WorkflowId: "wf1", RunId: "run1"},
			Type:      &commonpb.WorkflowType{Name: "TestWorkflow"},
			Status:    enumspb.WORKFLOW_EXECUTION_STATUS_RUNNING,
		}},
	}, nil
}

func (s *fakeWorkflowService) GetWorkflowExecutionHistory(ctx context.Context, req *workflowservice.GetWorkflowExecutionHistoryRequest) (*workflowservice.GetWorkflowExecutionHistoryResponse, error) {
	if s.history != nil {
		return &workflowservice.GetWorkflowExecutionHistoryResponse{History: s.history}, nil
	}
	return &workflowservice.GetWorkflowExecutionHistoryResponse{
		History: &historypb.History{Events: []*historypb.HistoryEvent{{
			EventId:   1,
			EventType: enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_STARTED,
		}}},
	}, nil
}

func TestReadOnlyProxy(t *testing.T) {
	t.Parallel()

	backendListener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	backend := grpc.NewServer()
	workflowservice.RegisterWorkflowServiceServer(backend, &fakeWorkflowService{})
	go func() { _ = backend.Serve(backendListener) }()
	t.Cleanup(backend.Stop)

	proxy, err := StartReadOnlyProxy("127.0.0.1:0", backendListener.Addr().String(), nil)
	require.NoError(t, err)
	t.Cleanup(proxy.Stop)

	// Dial exercises GetSystemInfo, which the proxy must allow.
	c, err := client.Dial(client.Options{HostPort: proxy.Addr(), Namespace: "default"})
	require.NoError(t, err)
	defer c.Close()

	ctx := context.Background()

	resp, err := c.ListWorkflow(ctx, &workflowservice.ListWorkflowExecutionsRequest{Query: "ExecutionStatus = 'Running'"})
	require.NoError(t, err)
	require.Len(t, resp.Executions, 1)
	assert.Equal(t, "wf1", resp.Executions[0].Execution.WorkflowId)

	iter := c.GetWorkflowHistory(ctx, "wf1", "run1", false, enumspb.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
	require.True(t, iter.HasNext())
	event, err := iter.Next()
	require.NoError(t, err)
	assert.Equal(t, enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_STARTED, event.EventType)

	mutations := map[string]error{
		"signal":    c.SignalWorkflow(ctx, "wf1", "run1", "some-signal", nil),
		"terminate": c.TerminateWorkflow(ctx, "wf1", "run1", "some-reason"),
		"cancel":    c.CancelWorkflow(ctx, "wf1", "run1"),
	}
	for name, err := range mutations {
		var unimplemented *serviceerror.Unimplemented
		assert.True(t, errors.As(err, &unimplemented), "%s must be rejected as unimplemented, got: %v", name, err)
	}
}

func TestReadOnlyProxyDecodesOffloadedPayloads(t *testing.T) {
	t.Parallel()

	storage := sqlite.NewTestSqliteStorage(t, "readonly_proxy_test")

	value := strings.Repeat("x", 64)
	original, err := converter.GetDefaultDataConverter().ToPayload(value)
	require.NoError(t, err)

	// Offload with a tiny threshold so the fake backend serves a reference
	// payload, like Temporal would for a payload the codec offloaded.
	offloadingCodec := common.NewPayloadCodec(storage, 1)
	encoded, err := offloadingCodec.Encode([]*commonpb.Payload{original})
	require.NoError(t, err)
	require.NotEqual(t, original.GetData(), encoded[0].GetData())

	backendListener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	backend := grpc.NewServer()
	workflowservice.RegisterWorkflowServiceServer(backend, &fakeWorkflowService{
		history: &historypb.History{Events: []*historypb.HistoryEvent{{
			EventId:   1,
			EventType: enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_STARTED,
			Attributes: &historypb.HistoryEvent_WorkflowExecutionStartedEventAttributes{
				WorkflowExecutionStartedEventAttributes: &historypb.WorkflowExecutionStartedEventAttributes{
					Input: &commonpb.Payloads{Payloads: encoded},
				},
			},
		}}},
	})
	go func() { _ = backend.Serve(backendListener) }()
	t.Cleanup(backend.Stop)

	proxy, err := StartReadOnlyProxy("127.0.0.1:0", backendListener.Addr().String(), storage)
	require.NoError(t, err)
	t.Cleanup(proxy.Stop)

	c, err := client.Dial(client.Options{HostPort: proxy.Addr(), Namespace: "default"})
	require.NoError(t, err)
	defer c.Close()

	iter := c.GetWorkflowHistory(context.Background(), "wf1", "run1", false, enumspb.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
	require.True(t, iter.HasNext())
	event, err := iter.Next()
	require.NoError(t, err)

	payloads := event.GetWorkflowExecutionStartedEventAttributes().GetInput().GetPayloads()
	require.Len(t, payloads, 1)
	var decoded string
	require.NoError(t, converter.GetDefaultDataConverter().FromPayload(payloads[0], &decoded))
	assert.Equal(t, value, decoded)
}