package workspace

import (
	"context"
	"errors"
	"sidekick/db"
	"sidekick/domain"
)

type Activities struct {
	DatabaseAccessor db.DatabaseAccessor
}

func (a *Activities) GetWorkspaceConfig(workspaceID string) (domain.WorkspaceConfig, error) {
	ctx := context.Background()
	config, err := a.DatabaseAccessor.GetWorkspaceConfig(ctx, workspaceID)
	if err != nil {
		return domain.WorkspaceConfig{}, err
	}

	if config.LLM.Defaults == nil || len(config.LLM.Defaults) == 0 {
		return domain.WorkspaceConfig{}, errors.New("missing LLM config for workspace")
	}

	if config.Embedding.Defaults == nil || len(config.Embedding.Defaults) == 0 {
		return domain.WorkspaceConfig{}, errors.New("missing embedding config")
	}

	return config, nil
}
