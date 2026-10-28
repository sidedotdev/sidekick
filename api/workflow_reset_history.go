package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"

	commonpb "go.temporal.io/api/common/v1"
	failurepb "go.temporal.io/api/failure/v1"
	historypb "go.temporal.io/api/history/v1"
	"go.temporal.io/api/workflowservice/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/reflect/protoreflect"
)

type resetHistoryCursor struct {
	FlowID string `json:"flowId"`
	RunID  string `json:"runId"`
	Token  []byte `json:"token"`
	After  int64  `json:"after"`
}

func decodeResetHistoryCursor(value, flowID string) (resetHistoryCursor, error) {
	var cursor resetHistoryCursor
	if value == "" {
		return cursor, nil
	}
	data, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return cursor, err
	}
	if err := json.Unmarshal(data, &cursor); err != nil {
		return cursor, err
	}
	if cursor.FlowID != flowID || cursor.RunID == "" || len(cursor.Token) == 0 || cursor.After < 0 {
		return cursor, errors.New("invalid history cursor")
	}
	return cursor, nil
}

func encodeResetHistoryCursor(cursor resetHistoryCursor) string {
	data, _ := json.Marshal(cursor)
	return base64.RawURLEncoding.EncodeToString(data)
}

func resetEventPointer(id int64) *int64 {
	if id == 0 {
		return nil
	}
	return &id
}

func (ctrl *Controller) reverseHistoryPage(ctx context.Context, flowID, runID string, token []byte, limit int32) (*workflowservice.GetWorkflowExecutionHistoryReverseResponse, error) {
	page, err := ctrl.temporalClient.WorkflowService().GetWorkflowExecutionHistoryReverse(ctx,
		&workflowservice.GetWorkflowExecutionHistoryReverseRequest{
			Namespace:       ctrl.temporalNamespace,
			Execution:       &commonpb.WorkflowExecution{WorkflowId: flowID, RunId: runID},
			MaximumPageSize: limit,
			NextPageToken:   token,
		})
	if err != nil {
		return nil, err
	}
	if page == nil {
		return nil, errors.New("empty history response")
	}
	if len(page.NextPageToken) > 0 && bytes.Equal(page.NextPageToken, token) {
		return nil, errors.New("history cursor did not advance")
	}
	return page, nil
}

// Lifecycle references are stable across history pages, unlike adjacency in
// history, where concurrent activities and workflow tasks interleave.
func resetHistoryGroupID(event *historypb.HistoryEvent) int64 {
	name := event.EventType.String()
	if !strings.HasPrefix(name, "ActivityTask") &&
		!strings.HasPrefix(name, "WorkflowTask") &&
		!strings.HasPrefix(name, "ChildWorkflowExecution") {
		return event.EventId
	}
	message := event.ProtoReflect()
	oneof := message.Descriptor().Oneofs().ByName("attributes")
	field := message.WhichOneof(oneof)
	if field == nil {
		return event.EventId
	}
	attributes := message.Get(field).Message()
	for _, name := range []protoreflect.Name{"scheduled_event_id", "initiated_event_id"} {
		if reference := attributes.Descriptor().Fields().ByName(name); reference != nil {
			if id := attributes.Get(reference).Int(); id > 0 {
				return id
			}
		}
	}
	return event.EventId
}

func resetHistoryFailure(event *historypb.HistoryEvent) *EventFailure {
	var failure *failurepb.Failure
	var cause string
	switch {
	case event.GetWorkflowTaskFailedEventAttributes() != nil:
		attrs := event.GetWorkflowTaskFailedEventAttributes()
		cause, failure = attrs.GetCause().String(), attrs.GetFailure()
	case event.GetWorkflowTaskTimedOutEventAttributes() != nil:
		cause = event.GetWorkflowTaskTimedOutEventAttributes().GetTimeoutType().String()
	case event.GetWorkflowExecutionFailedEventAttributes() != nil:
		cause, failure = "WorkflowExecutionFailed", event.GetWorkflowExecutionFailedEventAttributes().GetFailure()
	case event.GetWorkflowExecutionTimedOutEventAttributes() != nil:
		cause = "WorkflowExecutionTimedOut"
	case event.GetActivityTaskFailedEventAttributes() != nil:
		cause, failure = "ActivityTaskFailed", event.GetActivityTaskFailedEventAttributes().GetFailure()
	case event.GetActivityTaskTimedOutEventAttributes() != nil:
		cause, failure = "ActivityTaskTimedOut", event.GetActivityTaskTimedOutEventAttributes().GetFailure()
	case event.GetChildWorkflowExecutionFailedEventAttributes() != nil:
		cause, failure = "ChildWorkflowExecutionFailed", event.GetChildWorkflowExecutionFailedEventAttributes().GetFailure()
	default:
		return nil
	}
	result := &EventFailure{Cause: cause}
	for current := failure; current != nil; current = current.GetCause() {
		if result.Message != "" {
			result.Message += "\nCaused by: "
		}
		result.Message += current.GetMessage()
		if stack := current.GetStackTrace(); stack != "" {
			if result.StackTrace != "" {
				result.StackTrace += "\n"
			}
			result.StackTrace += stack
		}
	}
	return result
}

func resetHistoryRawEvent(event *historypb.HistoryEvent) json.RawMessage {
	data, err := protojson.Marshal(event)
	if err != nil {
		return nil
	}
	return data
}
