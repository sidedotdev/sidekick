package domain

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// record that is 1:1 with a temporal workflow
type Flow struct {
	WorkspaceId string    `json:"workspaceId"`
	Id          string    `json:"id"`
	Type        FlowType  `json:"type"`     // flow type
	ParentId    string    `json:"parentId"` // parent id of arbitrary type (eg task)
	Status      string    `json:"status"`   // flow status
	Created     time.Time `json:"created"`
	Updated     time.Time `json:"updated"`
}

func (f Flow) MarshalJSON() ([]byte, error) {
	type Alias Flow
	return json.Marshal(&struct {
		Alias
		Created time.Time `json:"created"`
		Updated time.Time `json:"updated"`
	}{
		Alias:   Alias(f),
		Created: UTCTime(f.Created),
		Updated: UTCTime(f.Updated),
	})
}

const FlowStatusPaused = "paused"

// TODO /gen remove type: we want to support external flow types not defined in
// this codebase
type FlowType = string

const (
	FlowTypeBasicDev   FlowType = "basic_dev"
	FlowTypePlannedDev FlowType = "planned_dev"
)

func StringToFlowType(s string) (FlowType, error) {
	switch s {
	case "basic_dev":
		return FlowTypeBasicDev, nil
	case "planned_dev":
		return FlowTypePlannedDev, nil
	default:
		return "", fmt.Errorf("Invalid flow type: \"%s\"", s)
	}
}

// FlowStorage defines the interface for flow-related database operations
type FlowStorage interface {
	PersistFlow(ctx context.Context, flow Flow) error
	GetFlow(ctx context.Context, workspaceId, flowId string) (Flow, error)
	GetFlowsForTask(ctx context.Context, workspaceId, taskId string) ([]Flow, error)
}
