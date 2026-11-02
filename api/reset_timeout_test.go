package api

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"
	"google.golang.org/grpc"
)

func TestResetFlowHandler_RPCDeadline(t *testing.T) {
	t.Parallel()

	f := newResetFixture(t)
	f.subtask.ParentId = ""
	require.NoError(t, f.ctrl.service.PersistFlow(context.Background(), f.subtask))

	resetCalls := 0
	temporalClient, err := client.NewLazyClient(client.Options{
		HostPort:  "localhost:1",
		Namespace: "default",
		ConnectionOptions: client.ConnectionOptions{
			DialOptions: []grpc.DialOption{
				grpc.WithChainUnaryInterceptor(func(ctx context.Context, method string, req, reply interface{}, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
					switch response := reply.(type) {
					case *workflowservice.GetSystemInfoResponse:
						return nil
					case *workflowservice.SignalWorkflowExecutionResponse:
						return nil
					case *workflowservice.ResetWorkflowExecutionResponse:
						resetCalls++
						deadline, ok := ctx.Deadline()
						if assert.True(t, ok, "reset RPC must have a deadline") {
							assert.InDelta(t, time.Minute.Seconds(), time.Until(deadline).Seconds(), 1)
						}
						request := req.(*workflowservice.ResetWorkflowExecutionRequest)
						assert.Equal(t, "default", request.Namespace)
						assert.Equal(t, f.subtask.Id, request.WorkflowExecution.GetWorkflowId())
						assert.EqualValues(t, 42, request.WorkflowTaskFinishEventId)
						assert.NotEmpty(t, request.RequestId)
						response.RunId = "run_new"
						return nil
					default:
						return fmt.Errorf("unexpected Temporal RPC: %s", method)
					}
				}),
			},
		},
	})
	require.NoError(t, err)
	defer temporalClient.Close()
	f.ctrl.temporalClient = temporalClient
	f.ctrl.temporalNamespace = "default"

	response := f.postReset(t, f.subtask.Id, 42)
	assert.Equal(t, http.StatusOK, response.Code, response.Body.String())
	assert.JSONEq(t, `{"runId":"run_new"}`, response.Body.String())
	assert.Equal(t, 1, resetCalls)
}

type resetWorkflowService struct {
	workflowservice.WorkflowServiceClient
	reset func(context.Context, *workflowservice.ResetWorkflowExecutionRequest) (*workflowservice.ResetWorkflowExecutionResponse, error)
}

func (s resetWorkflowService) ResetWorkflowExecution(ctx context.Context, req *workflowservice.ResetWorkflowExecutionRequest, _ ...grpc.CallOption) (*workflowservice.ResetWorkflowExecutionResponse, error) {
	return s.reset(ctx, req)
}
