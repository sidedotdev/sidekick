package domain

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

type ProjectPriority string

const (
	ProjectPriorityNone   ProjectPriority = "none"
	ProjectPriorityLow    ProjectPriority = "low"
	ProjectPriorityMedium ProjectPriority = "medium"
	ProjectPriorityHigh   ProjectPriority = "high"
	ProjectPriorityUrgent ProjectPriority = "urgent"
)

var AllProjectPriorities = []ProjectPriority{
	ProjectPriorityNone,
	ProjectPriorityLow,
	ProjectPriorityMedium,
	ProjectPriorityHigh,
	ProjectPriorityUrgent,
}

func StringToProjectPriority(s string) (ProjectPriority, error) {
	switch s {
	case "none":
		return ProjectPriorityNone, nil
	case "low":
		return ProjectPriorityLow, nil
	case "medium":
		return ProjectPriorityMedium, nil
	case "high":
		return ProjectPriorityHigh, nil
	case "urgent":
		return ProjectPriorityUrgent, nil
	default:
		return "", fmt.Errorf("invalid ProjectPriority: \"%s\"", s)
	}
}

// SortBucket returns the ordering bucket for a priority: lower values sort
// first, so more urgent projects appear before less urgent ones.
func (p ProjectPriority) SortBucket() int {
	switch p {
	case ProjectPriorityUrgent:
		return 0
	case ProjectPriorityHigh:
		return 1
	case ProjectPriorityMedium:
		return 2
	case ProjectPriorityLow:
		return 3
	default:
		return 4
	}
}

// ProjectStorage defines the interface for project-related database operations
type ProjectStorage interface {
	PersistProject(ctx context.Context, project Project) error
	GetProject(ctx context.Context, workspaceId, projectId string) (Project, error)
	// GetProjects returns all projects in a workspace, sorted by priority
	// bucket (most urgent first) then rank.
	GetProjects(ctx context.Context, workspaceId string) ([]Project, error)
	DeleteProject(ctx context.Context, workspaceId, projectId string) error
}

// Project represents a group of tasks that can be prioritized and ordered.
type Project struct {
	WorkspaceId string          `json:"workspaceId"`
	Id          string          `json:"id"`
	Title       string          `json:"title"`
	Description string          `json:"description,omitempty"`
	Priority    ProjectPriority `json:"priority"`
	// Rank is a lexicographic sort key for fine-grained ordering of projects
	// within the same priority bucket.
	Rank    string    `json:"rank,omitempty"`
	Created time.Time `json:"created"`
	Updated time.Time `json:"updated"`
}

func (p Project) MarshalJSON() ([]byte, error) {
	type Alias Project
	return json.Marshal(&struct {
		Alias
		Created time.Time `json:"created"`
		Updated time.Time `json:"updated"`
	}{
		Alias:   Alias(p),
		Created: UTCTime(p.Created),
		Updated: UTCTime(p.Updated),
	})
}
