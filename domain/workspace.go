package domain

import (
	"context"
	"encoding/json"
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
	ConfigMode   string    `json:"configMode"`   // configuration mode: 'local', 'workspace', or 'merge'
	Created      time.Time `json:"created"`      // creation timestamp of the workspace
	Updated      time.Time `json:"updated"`      // last update timestamp of the workspace
}

func (w Workspace) MarshalJSON() ([]byte, error) {
	type Alias Workspace
	return json.Marshal(&struct {
		Alias
		Created time.Time `json:"created"`
		Updated time.Time `json:"updated"`
	}{
		Alias:   Alias(w),
		Created: UTCTime(w.Created),
		Updated: UTCTime(w.Updated),
	})
}

// TODO /gen move to workspace package, along with corresponding accessor
// methods to new Accessor type within workspace package, extracted from db
// package.
type WorkspaceConfig struct {
	LLM                common.LLMConfig               `json:"llm"`
	Embedding          common.EmbeddingConfig         `json:"embedding"`
	CommandPermissions common.CommandPermissionConfig `json:"commandPermissions,omitempty"`
}

// WorkspaceStorage defines the interface for workspace-related database operations
type WorkspaceStorage interface {
	PersistWorkspace(ctx context.Context, workspace Workspace) error
	GetWorkspace(ctx context.Context, workspaceId string) (Workspace, error)
	GetAllWorkspaces(ctx context.Context) ([]Workspace, error)
	GetWorkspaceConfig(ctx context.Context, workspaceId string) (WorkspaceConfig, error)
	PersistWorkspaceConfig(ctx context.Context, workspaceId string, config WorkspaceConfig) error
	DeleteWorkspace(ctx context.Context, workspaceId string) error
}
