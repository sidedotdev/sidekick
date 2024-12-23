package domain

import (
	"fmt"
	"time"
)

// record that is 1:1 with a temporal workflow
type Flow struct {
	WorkspaceId string   `json:"workspaceId"`
	Id          string   `json:"id"`
	Type        FlowType `json:"type"`     // flow type
	ParentId    string   `json:"parentId"` // parent id of arbitrary type (eg task)
	Status      string   `json:"status"`   // flow status
}

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

type SubflowStatus string

const (
	SubflowStatusInProgress SubflowStatus = "in_progress"
	SubflowStatusStarted    SubflowStatus = "started"
	SubflowStatusComplete   SubflowStatus = "complete"
	SubflowStatusFailed     SubflowStatus = "failed"
)

// Subflow represents a subflow within a flow
type Subflow struct {
	WorkspaceId     string        `json:"workspaceId"`
	Id              string        `json:"id"`                        // Unique identifier, prefixed with 'sf_'
	Name            string        `json:"name"`                      // Name of the subflow
	Description     string        `json:"description,omitempty"`     // Description of the subflow, if any
	Status          SubflowStatus `json:"status"`                    // Status of the subflow
	ParentSubflowId string        `json:"parentSubflowId,omitempty"` // ID of the parent subflow, if any
	FlowId          string        `json:"flowId"`                    // ID of the flow this subflow belongs to
	Result          string        `json:"result,omitempty"`          // Result of the subflow, if any
}

type ActionStatus = string

const (
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
