package domain

import (
	"context"
	"time"
)

// Worktree represents a git worktree associated with a flow
type Worktree struct {
	Id     string `json:"id"`
	FlowId string `json:"flowId"`
	// a worktree's name refers to its branch name
	Name             string    `json:"name"`
	Created          time.Time `json:"created"`
	WorkspaceId      string    `json:"workspaceId"`
	WorkingDirectory string    `json:"workingDirectory"`
}

// WorktreeStorage defines the interface for worktree-related database operations
type WorktreeStorage interface {
	PersistWorktree(ctx context.Context, worktree Worktree) error
	GetWorktree(ctx context.Context, workspaceId, worktreeId string) (Worktree, error)
	GetWorktrees(ctx context.Context, workspaceId string) ([]Worktree, error)
	GetWorktreesForFlow(ctx context.Context, workspaceId, flowId string) ([]Worktree, error)
	DeleteWorktree(ctx context.Context, workspaceId, worktreeId string) error
}
