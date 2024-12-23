package redis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sidekick/domain"
	"sidekick/srv"
	"strings"

	"github.com/redis/go-redis/v9"
)

func (s Service) PersistWorkspace(ctx context.Context, workspace domain.Workspace) error {
	workspaceJson, err := json.Marshal(workspace)
	if err != nil {
		log.Println("Failed to convert workspace to JSON: ", err)
		return err
	}
	key := fmt.Sprintf("workspace:%s", workspace.Id)
	err = s.Client.Set(ctx, key, workspaceJson, 0).Err()
	if err != nil {
		log.Println("Failed to persist workspace to Redis: ", err)
		return err
	}

	// Add workspace to sorted set by workspace name
	err = s.Client.ZAdd(ctx, "global:workspaces", redis.Z{
		Score:  0,                                   // score is not used, we rely on the lexographical ordering of the member
		Member: workspace.Name + ":" + workspace.Id, // use name as member prefix to sort by name
	}).Err()
	if err != nil {
		log.Println("Failed to add workspace to sorted set: ", err)
		return err
	}

	return nil
}
func (s Service) GetWorkspace(ctx context.Context, workspaceId string) (domain.Workspace, error) {
	key := fmt.Sprintf("workspace:%s", workspaceId)
	workspaceJson, err := s.Client.Get(ctx, key).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return domain.Workspace{}, srv.ErrNotFound
		}
		return domain.Workspace{}, fmt.Errorf("failed to get workspace from Redis: %w", err)
	}
	var workspace domain.Workspace
	err = json.Unmarshal([]byte(workspaceJson), &workspace)
	if err != nil {
		return domain.Workspace{}, fmt.Errorf("failed to unmarshal workspace JSON: %w", err)
	}
	return workspace, nil
}

func (s Service) GetAllWorkspaces(ctx context.Context) ([]domain.Workspace, error) {
	var workspaces []domain.Workspace

	// Retrieve all workspace IDs from the Redis sorted set
	workspaceNameIds, err := s.Client.ZRange(ctx, "global:workspaces", 0, -1).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to get workspace IDs from Redis sorted set: %w", err)
	}

	for _, nameId := range workspaceNameIds {
		id := nameId[strings.LastIndex(nameId, ":")+1:]
		// Fetch workspace details using the ID
		workspaceJson, err := s.Client.Get(ctx, fmt.Sprintf("workspace:%s", id)).Result()
		if err != nil {
			return nil, fmt.Errorf("failed to get workspace data for ID %s: %w", id, err)
		}
		var workspace domain.Workspace
		err = json.Unmarshal([]byte(workspaceJson), &workspace)
		if err != nil {
			return nil, fmt.Errorf("failed to unmarshal workspace JSON: %w", err)
		}
		workspaces = append(workspaces, workspace)
	}

	return workspaces, nil
}

func (s Service) DeleteWorkspace(ctx context.Context, workspaceId string) error {
	// First get the workspace to get its name - ignore if not found
	workspace, err := s.GetWorkspace(ctx, workspaceId)
	if err != nil {
		if errors.Is(err, srv.ErrNotFound) {
			return nil // Workspace already deleted - that's fine
		}
		return fmt.Errorf("failed to get workspace before deletion: %w", err)
	}

	// Remove from sorted set
	err = s.Client.ZRem(ctx, "global:workspaces", workspace.Name+":"+workspaceId).Err()
	if err != nil {
		return fmt.Errorf("failed to remove workspace from sorted set: %w", err)
	}

	// Delete the workspace record
	key := fmt.Sprintf("workspace:%s", workspaceId)
	err = s.Client.Del(ctx, key).Err()
	if err != nil {
		return fmt.Errorf("failed to delete workspace record: %w", err)
	}

	// Delete workspace config if it exists
	configKey := fmt.Sprintf("%s:workspace_config", workspaceId)
	_ = s.Client.Del(ctx, configKey).Err() // Ignore error since config may not exist

	return nil
}

func (s Service) GetWorkspaceConfig(ctx context.Context, workspaceId string) (domain.WorkspaceConfig, error) {
	key := fmt.Sprintf("%s:workspace_config", workspaceId)
	configJson, err := s.Client.Get(ctx, key).Result()
	if err != nil {
		if err == redis.Nil {
			return domain.WorkspaceConfig{}, srv.ErrNotFound
		}
		return domain.WorkspaceConfig{}, fmt.Errorf("failed to get workspace config from Redis: %w", err)
	}

	var config domain.WorkspaceConfig
	if err := json.Unmarshal([]byte(configJson), &config); err != nil {
		return domain.WorkspaceConfig{}, fmt.Errorf("failed to unmarshal workspace config: %w", err)
	}

	return config, nil
}

func (s Service) PersistWorkspaceConfig(ctx context.Context, workspaceId string, config domain.WorkspaceConfig) error {
	if workspaceId == "" {
		return fmt.Errorf("workspaceId cannot be empty")
	}

	key := fmt.Sprintf("%s:workspace_config", workspaceId)
	configJson, err := json.Marshal(config)
	if err != nil {
		return fmt.Errorf("failed to marshal workspace config: %w", err)
	}

	if err := s.Client.Set(ctx, key, configJson, 0).Err(); err != nil {
		return fmt.Errorf("failed to persist workspace config to Redis: %w", err)
	}

	return nil
}
