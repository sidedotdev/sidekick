package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"sidekick/mocks"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/api/enums/v1"
	failurepb "go.temporal.io/api/failure/v1"
	historypb "go.temporal.io/api/history/v1"
	workflowpb "go.temporal.io/api/workflow/v1"
	"go.temporal.io/api/workflowservice/v1"
	"google.golang.org/grpc"
)

type resetHistoryService struct {
	workflowservice.WorkflowServiceClient
	requests []*workflowservice.GetWorkflowExecutionHistoryReverseRequest
	pages    []*workflowservice.GetWorkflowExecutionHistoryReverseResponse
}

func (s *resetHistoryService) GetWorkflowExecutionHistoryReverse(_ context.Context, req *workflowservice.GetWorkflowExecutionHistoryReverseRequest, _ ...grpc.CallOption) (*workflowservice.GetWorkflowExecutionHistoryReverseResponse, error) {
	s.requests = append(s.requests, req)
	return s.pages[len(s.requests)-1], nil
}

func resetHistoryContext(path string, eventID string) (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, path, nil)
	ctx.Params = gin.Params{
		{Key: "workspaceId", Value: "ws-1"},
		{Key: "id", Value: "flow-1"},
		{Key: "eventId", Value: eventID},
	}
	return ctx, recorder
}

func TestResetHistoryReversePagination(t *testing.T) {
	t.Parallel()
	newer := &historypb.HistoryEvent{EventId: 12, EventType: enums.EVENT_TYPE_WORKFLOW_TASK_COMPLETED}
	older := &historypb.HistoryEvent{EventId: 8, EventType: enums.EVENT_TYPE_WORKFLOW_TASK_COMPLETED}
	service := &resetHistoryService{pages: []*workflowservice.GetWorkflowExecutionHistoryReverseResponse{
		{History: &historypb.History{Events: []*historypb.HistoryEvent{newer}}, NextPageToken: []byte("older")},
		{History: &historypb.History{Events: []*historypb.HistoryEvent{older}}},
	}}
	ctrl := NewMockController(t)
	client := ctrl.temporalClient.(*mocks.Client)
	client.On("WorkflowService").Return(service).Maybe()
	client.On("DescribeWorkflowExecution", mock.Anything, "flow-1", "").Return(
		&workflowservice.DescribeWorkflowExecutionResponse{
			WorkflowExecutionInfo: &workflowpb.WorkflowExecutionInfo{
				Execution: &commonpb.WorkflowExecution{WorkflowId: "flow-1", RunId: "run-1"},
			},
		}, nil).Once()

	ctx, recorder := resetHistoryContext("/history", "")
	ctrl.GetFlowHistoryHandler(ctx)
	require.Equal(t, http.StatusOK, recorder.Code)
	var first struct {
		Events     []HistoryEvent `json:"events"`
		NextCursor string         `json:"nextCursor"`
		RunID      string         `json:"runId"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &first))
	require.Len(t, first.Events, 1)
	require.Equal(t, int64(12), first.Events[0].EventId)
	require.Equal(t, "run-1", first.RunID)
	require.NotEmpty(t, first.NextCursor)
	require.Len(t, service.requests, 1)
	require.Equal(t, int32(10000), service.requests[0].MaximumPageSize)

	ctx, recorder = resetHistoryContext("/history?cursor="+url.QueryEscape(first.NextCursor), "")
	ctrl.GetFlowHistoryHandler(ctx)
	require.Equal(t, http.StatusOK, recorder.Code)
	var second struct {
		Events     []HistoryEvent `json:"events"`
		NextCursor string         `json:"nextCursor"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &second))
	require.Len(t, second.Events, 1)
	require.Equal(t, int64(12), *second.Events[0].ResetAfterEventId)
	require.Empty(t, second.NextCursor)
	require.Len(t, service.requests, 2)
	require.Equal(t, []byte("older"), service.requests[1].NextPageToken)
	require.Equal(t, "run-1", service.requests[1].Execution.RunId)
}

func TestResetDetailReadsOnlyRecentReversePage(t *testing.T) {
	t.Parallel()
	event := &historypb.HistoryEvent{EventId: 90, EventType: enums.EVENT_TYPE_WORKFLOW_TASK_COMPLETED}
	service := &resetHistoryService{pages: []*workflowservice.GetWorkflowExecutionHistoryReverseResponse{
		{History: &historypb.History{Events: []*historypb.HistoryEvent{event}}, NextPageToken: []byte("older")},
	}}
	ctrl := NewMockController(t)
	client := ctrl.temporalClient.(*mocks.Client)
	client.On("WorkflowService").Return(service).Once()
	ctx, recorder := resetHistoryContext("/history/90?runId=run-1", "90")
	ctrl.GetFlowEventDetailHandler(ctx)
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Len(t, service.requests, 1)
	require.Equal(t, "run-1", service.requests[0].Execution.RunId)
}

func TestResetHistoryLifecycleMetadata(t *testing.T) {
	t.Parallel()
	events := []*historypb.HistoryEvent{
		{
			EventId: 9, EventType: enums.EVENT_TYPE_WORKFLOW_EXECUTION_FAILED,
			Attributes: &historypb.HistoryEvent_WorkflowExecutionFailedEventAttributes{
				WorkflowExecutionFailedEventAttributes: &historypb.WorkflowExecutionFailedEventAttributes{
					Failure: &failurepb.Failure{Message: "execution failed", StackTrace: "workflow.go:42"},
				},
			},
		},
		{
			EventId: 8, EventType: enums.EVENT_TYPE_ACTIVITY_TASK_COMPLETED,
			Attributes: &historypb.HistoryEvent_ActivityTaskCompletedEventAttributes{
				ActivityTaskCompletedEventAttributes: &historypb.ActivityTaskCompletedEventAttributes{ScheduledEventId: 5},
			},
		},
		{
			EventId: 5, EventType: enums.EVENT_TYPE_ACTIVITY_TASK_SCHEDULED,
			Attributes: &historypb.HistoryEvent_ActivityTaskScheduledEventAttributes{
				ActivityTaskScheduledEventAttributes: &historypb.ActivityTaskScheduledEventAttributes{
					ActivityType: &commonpb.ActivityType{Name: "my activity"},
				},
			},
		},
	}
	service := &resetHistoryService{pages: []*workflowservice.GetWorkflowExecutionHistoryReverseResponse{
		{History: &historypb.History{Events: events}},
	}}
	ctrl := NewMockController(t)
	client := ctrl.temporalClient.(*mocks.Client)
	client.On("WorkflowService").Return(service)
	cursor := encodeResetHistoryCursor(resetHistoryCursor{FlowID: "flow-1", RunID: "run-1", Token: []byte("page")})
	ctx, recorder := resetHistoryContext("/history?cursor="+url.QueryEscape(cursor), "")
	ctrl.GetFlowHistoryHandler(ctx)
	require.Equal(t, http.StatusOK, recorder.Code)
	var response struct {
		Events []struct {
			GroupID int64         `json:"groupId"`
			Failure *EventFailure `json:"failure"`
			Name    string        `json:"name"`
		} `json:"events"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	require.Len(t, response.Events, 3)
	require.NotNil(t, response.Events[0].Failure)
	require.Equal(t, "execution failed", response.Events[0].Failure.Message)
	require.Equal(t, int64(5), response.Events[1].GroupID)
	require.Equal(t, int64(5), response.Events[2].GroupID)
	require.Equal(t, "my activity", response.Events[2].Name)
}

func TestResetHistoryRejectsNonAdvancingToken(t *testing.T) {
	t.Parallel()
	service := &resetHistoryService{pages: []*workflowservice.GetWorkflowExecutionHistoryReverseResponse{
		{History: &historypb.History{}, NextPageToken: []byte("same")},
	}}
	ctrl := NewMockController(t)
	ctrl.temporalClient.(*mocks.Client).On("WorkflowService").Return(service)
	cursor := encodeResetHistoryCursor(resetHistoryCursor{FlowID: "flow-1", RunID: "run-1", Token: []byte("same")})
	ctx, recorder := resetHistoryContext("/history?cursor="+url.QueryEscape(cursor), "")
	ctrl.GetFlowHistoryHandler(ctx)
	require.Equal(t, http.StatusInternalServerError, recorder.Code)
}
