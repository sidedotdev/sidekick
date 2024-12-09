package workspace

import (
	"context"
	"errors"
	"sidekick/db"
	"sidekick/models"
)

type Activities struct {
	DatabaseAccessor db.DatabaseAccessor
}

func (a *Activities) GetWorkspaceConfig(workspaceID string) (models.WorkspaceConfig, error) {
	ctx := context.Background()
	config, err := a.DatabaseAccessor.GetWorkspaceConfig(ctx, workspaceID)
	if err != nil {
		return models.WorkspaceConfig{}, err
	}

	if config.LLM.Defaults == nil || len(config.LLM.Defaults) == 0 {
		return models.WorkspaceConfig{}, errors.New("missing LLM config for workspace")
	}

	if config.Embedding.Defaults == nil || len(config.Embedding.Defaults) == 0 {
		return models.WorkspaceConfig{}, errors.New("missing embedding config")
	}

	return config, nil
}
