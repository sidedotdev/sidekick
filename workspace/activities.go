package workspace

import (
	"context"
	"sidekick/domain"
	"sidekick/srv"
)

type Activities struct {
	Storage srv.Storage
}

func (a *Activities) GetWorkspace(workspaceID string) (domain.Workspace, error) {
	ctx := context.Background()
	workspace, err := a.Storage.GetWorkspace(ctx, workspaceID)
	if err != nil {
		return domain.Workspace{}, err
	}
	return workspace, nil
}

func (a *Activities) GetWorkspaceConfig(workspaceID string) (domain.WorkspaceConfig, error) {
	ctx := context.Background()
	config, err := a.Storage.GetWorkspaceConfig(ctx, workspaceID)
	if err != nil {
		return domain.WorkspaceConfig{}, err
	}
	return config, nil
}
