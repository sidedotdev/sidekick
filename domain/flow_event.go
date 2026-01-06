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
	StatusChangeEventType     FlowEventType = "status_change"
	ChatMessageDeltaEventType FlowEventType = "chat_message_delta"
	EndStreamEventType        FlowEventType = "end_stream"
	CodeDiffEventType         FlowEventType = "code_diff"
	DevRunStartedEventType    FlowEventType = "dev_run_started"
	DevRunOutputEventType     FlowEventType = "dev_run_output"
	DevRunEndedEventType      FlowEventType = "dev_run_ended"
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
	Details   string        `json:"details"`
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

// StatusChangeEvent represents a status change in the flow.
type StatusChangeEvent struct {
	EventType FlowEventType `json:"eventType"`
	ParentId  string        `json:"parentId"`
	TargetId  string        `json:"targetId,omitempty"`
	Status    string        `json:"status"`
}

// GetParentId returns the parent ID of the StatusChangeEvent.
func (e StatusChangeEvent) GetParentId() string {
	return e.ParentId
}

// GetEventType returns the event type of the StatusChangeEvent.
func (e StatusChangeEvent) GetEventType() FlowEventType {
	return e.EventType
}

// Ensure StatusChangeEvent implements FlowEvent interface
var _ FlowEvent = (*StatusChangeEvent)(nil)

type CodeDiffEvent struct {
	EventType FlowEventType `json:"eventType"`
	SubflowId string        `json:"subflowId"`
	Diff      string        `json:"diff"`
}

func (e CodeDiffEvent) GetParentId() string {
	return e.SubflowId
}

func (e CodeDiffEvent) GetEventType() FlowEventType {
	return e.EventType
}

var _ FlowEvent = (*CodeDiffEvent)(nil)

// DevRunStartedEvent is emitted when a Dev Run process starts.
type DevRunStartedEvent struct {
	EventType      FlowEventType `json:"eventType"`
	FlowId         string        `json:"flowId"`
	DevRunId       string        `json:"devRunId"`
	CommandSummary string        `json:"commandSummary"`
	WorkingDir     string        `json:"workingDir"`
	Pid            int           `json:"pid"`
	Pgid           int           `json:"pgid"`
}

func (e DevRunStartedEvent) GetParentId() string {
	return e.FlowId
}

func (e DevRunStartedEvent) GetEventType() FlowEventType {
	return e.EventType
}

var _ FlowEvent = (*DevRunStartedEvent)(nil)

// DevRunOutputEvent streams stdout/stderr output from a running Dev Run.
type DevRunOutputEvent struct {
	EventType FlowEventType `json:"eventType"`
	DevRunId  string        `json:"devRunId"`
	Stream    string        `json:"stream"` // "stdout" or "stderr"
	Chunk     string        `json:"chunk"`
	Sequence  int64         `json:"sequence"`
	Timestamp int64         `json:"timestamp"`
}

func (e DevRunOutputEvent) GetParentId() string {
	return e.DevRunId
}

func (e DevRunOutputEvent) GetEventType() FlowEventType {
	return e.EventType
}

var _ FlowEvent = (*DevRunOutputEvent)(nil)

// DevRunEndedEvent is emitted when a Dev Run process ends.
type DevRunEndedEvent struct {
	EventType  FlowEventType `json:"eventType"`
	FlowId     string        `json:"flowId"`
	DevRunId   string        `json:"devRunId"`
	ExitStatus *int          `json:"exitStatus,omitempty"`
	Signal     string        `json:"signal,omitempty"`
	Error      string        `json:"error,omitempty"`
}

func (e DevRunEndedEvent) GetParentId() string {
	return e.FlowId
}

func (e DevRunEndedEvent) GetEventType() FlowEventType {
	return e.EventType
}

var _ FlowEvent = (*DevRunEndedEvent)(nil)

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

	case StatusChangeEventType:
		var statusChange StatusChangeEvent
		err := json.Unmarshal(data, &statusChange)
		if err != nil {
			return nil, err
		}
		return statusChange, nil

	case CodeDiffEventType:
		var codeDiff CodeDiffEvent
		err := json.Unmarshal(data, &codeDiff)
		if err != nil {
			return nil, err
		}
		return codeDiff, nil

	case DevRunStartedEventType:
		var devRunStarted DevRunStartedEvent
		err := json.Unmarshal(data, &devRunStarted)
		if err != nil {
			return nil, err
		}
		return devRunStarted, nil

	case DevRunOutputEventType:
		var devRunOutput DevRunOutputEvent
		err := json.Unmarshal(data, &devRunOutput)
		if err != nil {
			return nil, err
		}
		return devRunOutput, nil

	case DevRunEndedEventType:
		var devRunEnded DevRunEndedEvent
		err := json.Unmarshal(data, &devRunEnded)
		if err != nil {
			return nil, err
		}
		return devRunEnded, nil

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

// FlowEventContainer is a wrapper around FlowEvent interface to allow
// for robust JSON marshaling and unmarshaling, particularly for use
// in contexts like Temporal activity arguments where interface types
// are problematic.
type FlowEventContainer struct {
	FlowEvent FlowEvent
}

// MarshalJSON implements the json.Marshaler interface.
// It marshals the underlying concrete FlowEvent.
func (c FlowEventContainer) MarshalJSON() ([]byte, error) {
	if c.FlowEvent == nil {
		// Return JSON null if the event is nil
		return []byte("null"), nil
	}
	// Marshal the concrete event stored in the interface.
	// This works because json.Marshal calls the MarshalJSON method of the
	// concrete type if available, or uses reflection otherwise. Since our
	// concrete event types are simple structs with json tags, this correctly
	// marshals them including their "eventType" field.
	return json.Marshal(c.FlowEvent)
}

// UnmarshalJSON implements the json.Unmarshaler interface.
// It uses UnmarshalFlowEvent to determine the concrete type from the
// "eventType" field in the JSON data and unmarshals into that type.
func (c *FlowEventContainer) UnmarshalJSON(data []byte) error {
	// Handle null JSON value
	if string(data) == "null" {
		c.FlowEvent = nil
		return nil
	}

	// Use the existing helper function to unmarshal based on eventType
	flowEvent, err := UnmarshalFlowEvent(data)
	if err != nil {
		return fmt.Errorf("failed to unmarshal data into FlowEvent for container: %w", err)
	}
	c.FlowEvent = flowEvent
	return nil
}
