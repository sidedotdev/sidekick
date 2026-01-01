package domain

import (
	"context"
	"encoding/json"
	"time"
)

type ActionStatus = string

const (
	// pending as akin to requested
	ActionStatusPending  ActionStatus = "pending"
	ActionStatusStarted  ActionStatus = "started"
	ActionStatusComplete ActionStatus = "complete"
	ActionStatusFailed   ActionStatus = "failed"
)

type FlowAction struct {
	Id                 string                 `json:"id"`
	SubflowName        string                 `json:"subflow"`            // TODO: remove in favor of SubflowId
	SubflowDescription string                 `json:"subflowDescription"` // TODO: remove in favor of SubflowId
	SubflowId          string                 `json:"subflowId,omitempty"`
	FlowId             string                 `json:"flowId"`
	WorkspaceId        string                 `json:"workspaceId"`
	Created            time.Time              `json:"created"`
	Updated            time.Time              `json:"updated"`
	ActionType         string                 `json:"actionType"`
	ActionParams       map[string]interface{} `json:"actionParams"`
	ActionStatus       ActionStatus           `json:"actionStatus"`
	ActionResult       string                 `json:"actionResult"`
	IsHumanAction      bool                   `json:"isHumanAction"`
	IsCallbackAction   bool                   `json:"isCallbackAction"`
}

func (fa FlowAction) MarshalJSON() ([]byte, error) {
	type Alias FlowAction
	return json.Marshal(&struct {
		Alias
		Created time.Time `json:"created"`
		Updated time.Time `json:"updated"`
	}{
		Alias:   Alias(fa),
		Created: UTCTime(fa.Created),
		Updated: UTCTime(fa.Updated),
	})
}

// FlowStorage defines the interface for flow-related database operations
type FlowActionStorage interface {
	PersistFlowAction(ctx context.Context, flowAction FlowAction) error
	GetFlowActions(ctx context.Context, workspaceId, flowId string) ([]FlowAction, error)
	GetFlowAction(ctx context.Context, workspaceId, flowActionId string) (FlowAction, error)
}

// FlowActionStreamer defines the interface for flow-related stream operations
type FlowActionStreamer interface {
	AddFlowActionChange(ctx context.Context, flowAction FlowAction) error
	StreamFlowActionChanges(ctx context.Context, workspaceId, flowId, streamMessageStartId string) (<-chan FlowAction, <-chan error)
}
