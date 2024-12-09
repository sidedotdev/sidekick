package models

import (
	"fmt"
	"sidekick/common"
	"time"
)

// a workspace is the unit of organization for flows/tasks/etc and will be
// associated with specific users (which don't yet exist) and will have some
// top-level configuration, eg the repo directory
type Workspace struct {
	Id           string    `json:"id"`
	Name         string    `json:"name"`         // name of the workspace
	LocalRepoDir string    `json:"localRepoDir"` // local code repository directory
	Created      time.Time `json:"created"`      // creation timestamp of the workspace
	Updated      time.Time `json:"updated"`      // last update timestamp of the workspace
}

// TODO /gen move to workspace package, along with corresponding accessor
// methods to new Accessor type within workspace package, extracted from db
// package.
type WorkspaceConfig struct {
	LLM       common.LLMConfig       `json:"llm"`
	Embedding common.EmbeddingConfig `json:"embedding"`
}

// record that is 1:1 with a temporal workflow
type Flow struct {
	WorkspaceId string   `json:"workspaceId"`
	Id          string   `json:"id"`
	TopicId     string   `json:"topicId"`  // topic id
	Type        FlowType `json:"type"`     // flow type
	ParentId    string   `json:"parentId"` // parent id of arbitrary type (eg task)
	Status      string   `json:"status"`   // flow status
}

type FlowType = string

const (
	FlowTypeBasicDev   FlowType = "basic_dev"
	FlowTypePlannedDev FlowType = "planned_dev"
)

type FileSummaryEmbedding struct {
	Path      string    `json:"path"`      // path of the file
	Embedding []float32 `json:"embedding"` // embedding of the file summary
}

type LinkType = string

const (
	LinkTypeBlocks    LinkType = "blocks"
	LinkTypeBlockedBy LinkType = "blocked_by"
	LinkTypeParent    LinkType = "parent"
	LinkTypeChild     LinkType = "child"
)

type TaskLink struct {
	LinkType     string `json:"linkType"`
	TargetTaskId string `json:"targetTaskId"`
}

type TaskStatus string

const (
	TaskStatusDrafting   TaskStatus = "drafting"
	TaskStatusToDo       TaskStatus = "to_do"
	TaskStatusInProgress TaskStatus = "in_progress"
	TaskStatusBlocked    TaskStatus = "blocked"
	TaskStatusComplete   TaskStatus = "complete"
	TaskStatusFailed     TaskStatus = "failed"
	TaskStatusCanceled   TaskStatus = "canceled"
)

var AllTaskStatuses []TaskStatus = []TaskStatus{
	TaskStatusDrafting,
	TaskStatusToDo,
	TaskStatusInProgress,
	TaskStatusBlocked,
	TaskStatusComplete,
	TaskStatusFailed,
	TaskStatusCanceled,
}

// Task represents the structure of tasks to be stored in the database.
type Task struct {
	WorkspaceId string                 `json:"workspaceId"`
	Id          string                 `json:"id"`
	Title       string                 `json:"title"`
	Description string                 `json:"description"`
	Status      TaskStatus             `json:"status"`
	Links       []TaskLink             `json:"links,omitempty"`
	AgentType   AgentType              `json:"agentType"`
	FlowType    FlowType               `json:"flowType"`
	Created     time.Time              `json:"created"`
	Updated     time.Time              `json:"updated"`
	FlowOptions map[string]interface{} `json:"flowOptions,omitempty"`
}

type ActionStatus = string

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

type AgentType string

const (
	AgentTypeHuman AgentType = "human"
	AgentTypeLLM   AgentType = "llm"
	AgentTypeNone  AgentType = "none"
)

func StringToAgentType(s string) (AgentType, error) {
	switch s {
	case "human":
		return AgentTypeHuman, nil
	case "llm":
		return AgentTypeLLM, nil
	case "none":
		return AgentTypeNone, nil
	case "":
		// default
		return AgentTypeLLM, nil
	default:
		return "", fmt.Errorf("Invalid agent type: \"%s\"", s)
	}
}

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

func StringToTaskStatus(s string) (TaskStatus, error) {
	switch s {
	case "to_do":
		return TaskStatusToDo, nil
	case "in_progress":
		return TaskStatusInProgress, nil
	case "complete":
		return TaskStatusComplete, nil
	case "drafting":
		return TaskStatusDrafting, nil
	case "blocked":
		return TaskStatusBlocked, nil
	case "failed":
		return TaskStatusFailed, nil
	case "canceled":
		return TaskStatusCanceled, nil
	default:
		return "", fmt.Errorf("invalid TaskStatus: %s", s)
	}
}

// Message represents the structure of messages to be stored in the database.
type Message struct {
	WorkspaceId string    `json:"workspaceId"`
	TopicId     string    `json:"topicId"` // partition key - topic id
	Id          string    `json:"id"`      // sort key - message id
	Role        string    `json:"role"`
	Content     string    `json:"content,omitempty"`
	Created     time.Time `json:"created"`
	FlowId      string    `json:"flowId,omitempty"` // optional: associated flow id, if any
	Status      string    `json:"status"`           // "unstarted", "partial", "complete"
}

const (
	MessageStatusStarted  = "started"
	MessageStatusComplete = "complete"
)

// TODO NewMessage

type PartialMessage struct {
	// FIXME we don't need this I think?
	//MessageId string `json:"messageId"`
	Role    string `json:"role,omitempty"`
	Content string `json:"content,omitempty"`
	// TODO model tool calls properly
	// ToolCalls []llm.ToolCall `json:"toolCalls,omitempty"`
}

// Topic represents the structure of topics to be stored in the database.
type Topic struct {
	WorkspaceId string    `json:"workspaceId"` // partition key - workspace id
	Id          string    `json:"id"`          // sort key - topic id
	Title       string    `json:"title"`       // title - title of the topic
	Created     time.Time `json:"created"`     // created - creation timestamp of the topic
	Updated     time.Time `json:"updated"`     // updated - last update timestamp of the topic
}
