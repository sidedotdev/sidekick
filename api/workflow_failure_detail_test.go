package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"sidekick/mocks"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/api/enums/v1"
	failurepb "go.temporal.io/api/failure/v1"
	historypb "go.temporal.io/api/history/v1"
)

type failureDetailHistoryIterator struct {
	event *historypb.HistoryEvent
}

func (i *failureDetailHistoryIterator) HasNext() bool {
	return i.event != nil
}

func (i *failureDetailHistoryIterator) Next() (*historypb.HistoryEvent, error) {
	event := i.event
	i.event = nil
	return event, nil
}

func TestGetFlowEventDetailHandlerFailure(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		event *historypb.HistoryEvent
		want  map[string]interface{}
	}{
		{
			name: "workflow task failure",
			event: &historypb.HistoryEvent{
				EventType: enums.EVENT_TYPE_WORKFLOW_TASK_FAILED,
				Attributes: &historypb.HistoryEvent_WorkflowTaskFailedEventAttributes{
					WorkflowTaskFailedEventAttributes: &historypb.WorkflowTaskFailedEventAttributes{
						Cause: enums.WORKFLOW_TASK_FAILED_CAUSE_NON_DETERMINISTIC_ERROR,
						Failure: &failurepb.Failure{
							Message:    "workflow replay failed",
							StackTrace: "workflow.Run()\n\tworkflow.go:42",
						},
					},
				},
			},
			want: map[string]interface{}{
				"cause":      "NonDeterministicError",
				"message":    "workflow replay failed",
				"stackTrace": "workflow.Run()\n\tworkflow.go:42",
			},
		},
		{
			name: "cause without failure payload",
			event: &historypb.HistoryEvent{
				EventType: enums.EVENT_TYPE_WORKFLOW_TASK_FAILED,
				Attributes: &historypb.HistoryEvent_WorkflowTaskFailedEventAttributes{
					WorkflowTaskFailedEventAttributes: &historypb.WorkflowTaskFailedEventAttributes{
						Cause: enums.WORKFLOW_TASK_FAILED_CAUSE_NON_DETERMINISTIC_ERROR,
					},
				},
			},
			want: map[string]interface{}{
				"cause":      "NonDeterministicError",
				"message":    "",
				"stackTrace": "",
			},
		},
		{
			name: "unrelated event",
			event: &historypb.HistoryEvent{
				EventType: enums.EVENT_TYPE_WORKFLOW_TASK_COMPLETED,
			},
		},
		{
			name: "missing attributes",
			event: &historypb.HistoryEvent{
				EventType: enums.EVENT_TYPE_WORKFLOW_TASK_FAILED,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctrl := NewMockController(t)
			tt.event.EventId = 7
			ctrl.temporalClient.(*mocks.Client).
				On("GetWorkflowHistory", mock.Anything, "flow-1", "", false, enums.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT).
				Return(&failureDetailHistoryIterator{event: tt.event}).Once()

			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			ctx.Request = httptest.NewRequest(http.MethodGet, "/", nil)
			ctx.Params = gin.Params{
				{Key: "workspaceId", Value: "ws-1"},
				{Key: "id", Value: "flow-1"},
				{Key: "eventId", Value: "7"},
			}
			ctrl.GetFlowEventDetailHandler(ctx)

			require.Equal(t, http.StatusOK, recorder.Code)
			var response struct {
				Event map[string]interface{} `json:"event"`
			}
			require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
			assert.Equal(t, float64(7), response.Event["eventId"])
			assert.Equal(t, []interface{}{}, response.Event["input"])
			assert.Equal(t, []interface{}{}, response.Event["output"])
			if tt.want == nil {
				assert.NotContains(t, response.Event, "failure")
			} else {
				assert.Equal(t, tt.want, response.Event["failure"])
			}
		})
	}
}
