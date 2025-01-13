package workspace

import (
	"context"
	"sidekick/domain"
	"sidekick/srv"
)

type Activities struct {
	Storage srv.Storage
}

func (a *Activities) GetWorkspaceConfig(workspaceID string) (domain.WorkspaceConfig, error) {
	ctx := context.Background()
	config, err := a.Storage.GetWorkspaceConfig(ctx, workspaceID)
	if err != nil {
		return domain.WorkspaceConfig{}, err
	}
	return config, nil
}
