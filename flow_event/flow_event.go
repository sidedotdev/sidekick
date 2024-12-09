package flow_event

import (
	"encoding/json"
	"fmt"
	"sidekick/llm"
)

// FlowEventType represents the different types of flow events.
type FlowEventType string

const (
	ProgressTextEventType     FlowEventType = "progress_text"
	ChatMessageDeltaEventType FlowEventType = "chat_message_delta"
	EndStreamEventType        FlowEventType = "end_stream"
)

// EndStream represents the end of a flow event stream.
type EndStream struct {
	EventType FlowEventType `json:"eventType"`
	ParentId  string        `json:"parentId"`
}

func (e EndStream) GetParentId() string {
	return e.ParentId
}

func (e EndStream) GetEventType() FlowEventType {
	return e.EventType
}

var _ FlowEvent = EndStream{}

// FlowEvent is an interface representing a flow event with a parent ID.
type FlowEvent interface {
	GetParentId() string
	GetEventType() FlowEventType
}

// ProgressText represents the progress text updates in a flow action. The text
// for this event is the latest full progress text, eg "Running tests...".
type ProgressText struct {
	Text      string        `json:"text"`
	EventType FlowEventType `json:"eventType"`
	// either a FlowAction or Subflow may be a parent of a ProgressText
	ParentId string `json:"parentId"`
}

func (e ProgressText) GetParentId() string {
	return e.ParentId
}

func (e ProgressText) GetEventType() FlowEventType {
	return e.EventType
}

var _ FlowEvent = ProgressText{}

type ChatMessageDelta struct {
	EventType        FlowEventType        `json:"eventType"`
	FlowActionId     string               `json:"flowActionId"`
	ChatMessageDelta llm.ChatMessageDelta `json:"chatMessageDelta"`
}

func (e ChatMessageDelta) GetParentId() string {
	return e.FlowActionId
}

func (e ChatMessageDelta) GetEventType() FlowEventType {
	return e.EventType
}

var _ FlowEvent = ChatMessageDelta{}

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
		var progressText ProgressText
		err := json.Unmarshal(data, &progressText)
		if err != nil {
			return nil, err
		}
		return progressText, nil

	case ChatMessageDeltaEventType:
		var chatMessageDelta ChatMessageDelta
		err := json.Unmarshal(data, &chatMessageDelta)
		if err != nil {
			return nil, err
		}
		return chatMessageDelta, nil

	case EndStreamEventType:
		var endStream EndStream
		err := json.Unmarshal(data, &endStream)
		if err != nil {
			return nil, err
		}
		return endStream, nil

	default:
		return nil, fmt.Errorf("unknown flow eventType: %s", event.EventType)
	}
}
