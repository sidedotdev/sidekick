package domain

import (
	"context"
	"fmt"
	"time"
)

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
	TaskStatusComplete   TaskStatus = "complete" // considered to be "finished"
	TaskStatusFailed     TaskStatus = "failed"   // also considered to be "finished"
	TaskStatusCanceled   TaskStatus = "canceled" // also considered to be "finished"
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

// TaskStorage defines the interface for task-related database operations
type TaskStorage interface {
	PersistTask(ctx context.Context, task Task) error
	GetTask(ctx context.Context, workspaceId, taskId string) (Task, error)
	GetTasks(ctx context.Context, workspaceId string, statuses []TaskStatus) ([]Task, error)
	DeleteTask(ctx context.Context, workspaceId, taskId string) error
	GetArchivedTasks(ctx context.Context, workspaceId string, page, pageSize int64) ([]Task, int64, error)
}

// TaskStreamer defines the interface for task-related stream operations
type TaskStreamer interface {
	AddTaskChange(ctx context.Context, task Task) error
	GetTaskChanges(ctx context.Context, workspaceId, streamMessageStartId string, maxCount int64, blockDuration time.Duration) ([]Task, string, error)
	StreamTaskChanges(ctx context.Context, workspaceId, streamMessageStartId string) (<-chan Task, <-chan error)
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
		return "", fmt.Errorf("invalid TaskStatus: \"%s\"", s)
	}
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
	Archived    *time.Time             `json:"archived,omitempty"`
	Created     time.Time              `json:"created"`
	Updated     time.Time              `json:"updated"`
	FlowOptions map[string]interface{} `json:"flowOptions,omitempty"`
	StreamId    string                 `json:"streamId,omitempty"`
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
