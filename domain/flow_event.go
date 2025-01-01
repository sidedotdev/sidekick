package domain

import (
	"context"
	"encoding/json"
	"fmt"
	"sidekick/common"
)

// FlowEventType represents the different types of flow events.
type FlowEventType string

const (
	ProgressTextEventType     FlowEventType = "progress_text"
	StatusChangeEventType	  FlowEventType = "status_change"
	ChatMessageDeltaEventType FlowEventType = "chat_message_delta"
	EndStreamEventType        FlowEventType = "end_stream"
)

// EndStreamEvent represents the end of a flow event stream.
type EndStreamEvent struct {
	EventType FlowEventType `json:"eventType"`
	ParentId  string        `json:"parentId"`
}

func (e EndStreamEvent) GetParentId() string {
	return e.ParentId
}

func (e EndStreamEvent) GetEventType() FlowEventType {
	return e.EventType
}

var _ FlowEvent = EndStreamEvent{}

// FlowEvent is an interface representing a flow event with a parent ID.
type FlowEvent interface {
	GetParentId() string
	GetEventType() FlowEventType
}

// ProgressTextEvent represents the progress text updates in a flow action. The text
// for this event is the latest full progress text, eg "Running tests...".
type ProgressTextEvent struct {
	Text      string        `json:"text"`
	EventType FlowEventType `json:"eventType"`
	// either a FlowAction, Subflow or Flow may be a parent of a ProgressText
	ParentId string `json:"parentId"`
}

func (e ProgressTextEvent) GetParentId() string {
	return e.ParentId
}

func (e ProgressTextEvent) GetEventType() FlowEventType {
	return e.EventType
}

var _ FlowEvent = ProgressTextEvent{}

type ChatMessageDeltaEvent struct {
	EventType        FlowEventType           `json:"eventType"`
	FlowActionId     string                  `json:"flowActionId"`
	ChatMessageDelta common.ChatMessageDelta `json:"chatMessageDelta"`
}

func (e ChatMessageDeltaEvent) GetParentId() string {
	return e.FlowActionId
}

func (e ChatMessageDeltaEvent) GetEventType() FlowEventType {
	return e.EventType
}

var _ FlowEvent = ChatMessageDeltaEvent{}

// UnmarshalFlowEvent unmarshals a JSON byte slice into a FlowEvent based on the "eventType" field.
func UnmarshalFlowEvent(data []byte) (FlowEvent, error) {
	var event struct {
		EventType FlowEventType `json:"eventType"`
	}

	err := json.Unmarshal(data, &event)
	if err != nil {
		return nil, err
	}

	switch event.EventType {
	case ProgressTextEventType:
		var progressText ProgressTextEvent
		err := json.Unmarshal(data, &progressText)
		if err != nil {
			return nil, err
		}
		return progressText, nil

	case ChatMessageDeltaEventType:
		var chatMessageDelta ChatMessageDeltaEvent
		err := json.Unmarshal(data, &chatMessageDelta)
		if err != nil {
			return nil, err
		}
		return chatMessageDelta, nil

	case EndStreamEventType:
		var endStream EndStreamEvent
		err := json.Unmarshal(data, &endStream)
		if err != nil {
			return nil, err
		}
		return endStream, nil

	default:
		return nil, fmt.Errorf("unknown flow eventType: %s", event.EventType)
	}
}

type FlowEventSubscription struct {
	ParentId             string `json:"parentId"`
	StreamMessageStartId string `json:"streamMessageStartId,omitempty"`
}

type FlowEventStreamer interface {
	AddFlowEvent(ctx context.Context, workspaceId string, flowId string, flowEvent FlowEvent) error
	EndFlowEventStream(ctx context.Context, workspaceId, flowId, eventStreamParentId string) error
	StreamFlowEvents(ctx context.Context, workspaceId, flowId string, subscriptionCh <-chan FlowEventSubscription) (<-chan FlowEvent, <-chan error)
}
