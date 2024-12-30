package redis

import (
	"context"
	"encoding/json"
	"fmt"
	"sidekick/domain"
	"sidekick/srv"

	"github.com/redis/go-redis/v9"
)

func (s Storage) PersistWorktree(ctx context.Context, worktree domain.Worktree) error {
	worktreeJSON, err := json.Marshal(worktree)
	if err != nil {
		return fmt.Errorf("failed to marshal worktree: %w", err)
	}

	worktreeKey := fmt.Sprintf("%s:%s", worktree.WorkspaceId, worktree.Id)
	err = s.Client.Set(ctx, worktreeKey, worktreeJSON, 0).Err()
	if err != nil {
		return fmt.Errorf("failed to persist worktree: %w", err)
	}

	// Add worktree ID to the set of worktrees for the workspace
	workspaceWorktreesKey := fmt.Sprintf("%s:worktrees", worktree.WorkspaceId)
	err = s.Client.SAdd(ctx, workspaceWorktreesKey, worktree.Id).Err()
	if err != nil {
		return fmt.Errorf("failed to add worktree to workspace set: %w", err)
	}

	// Add worktree ID to the set of worktrees for the flow
	flowWorktreesKey := fmt.Sprintf("%s:worktrees", worktree.FlowId)
	err = s.Client.SAdd(ctx, flowWorktreesKey, worktree.Id).Err()
	if err != nil {
		return fmt.Errorf("failed to add worktree to flow set: %w", err)
	}

	return nil
}

func (s Storage) GetWorktree(ctx context.Context, workspaceId, worktreeId string) (domain.Worktree, error) {
	worktreeKey := fmt.Sprintf(":%s:%s", workspaceId, worktreeId)
	worktreeJSON, err := s.Client.Get(ctx, worktreeKey).Result()
	if err != nil {
		if err == redis.Nil {
			return domain.Worktree{}, srv.ErrNotFound
		}
		return domain.Worktree{}, fmt.Errorf("failed to get worktree: %w", err)
	}

	var worktree domain.Worktree
	err = json.Unmarshal([]byte(worktreeJSON), &worktree)
	if err != nil {
		return domain.Worktree{}, fmt.Errorf("failed to unmarshal worktree: %w", err)
	}

	return worktree, nil
}

func (s Storage) GetWorktrees(ctx context.Context, workspaceId string) ([]domain.Worktree, error) {
	workspaceWorktreesKey := fmt.Sprintf("%s:worktrees", workspaceId)
	worktreeIds, err := s.Client.SMembers(ctx, workspaceWorktreesKey).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to get worktree IDs: %w", err)
	}

	worktrees := make([]domain.Worktree, 0, len(worktreeIds))
	for _, worktreeId := range worktreeIds {
		worktree, err := s.GetWorktree(ctx, workspaceId, worktreeId)
		if err != nil {
			if err == srv.ErrNotFound {
				continue
			}
			return nil, fmt.Errorf("failed to get worktree %s: %w", worktreeId, err)
		}
		worktrees = append(worktrees, worktree)
	}

	return worktrees, nil
}

func (s Storage) GetWorktreesForFlow(ctx context.Context, flowId string) ([]domain.Worktree, error) {
	flowWorktreesKey := fmt.Sprintf("%s:worktrees", flowId)
	worktreeIds, err := s.Client.SMembers(ctx, flowWorktreesKey).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to get worktree IDs: %w", err)
	}
	
	worktrees := make([]domain.Worktree, 0, len(worktreeIds))
	for _, worktreeId := range worktreeIds {
		worktree, err := s.GetWorktree(ctx, flowId, worktreeId)
		if err != nil {
			if err == srv.ErrNotFound {
				continue
			}
			return nil, fmt.Errorf("failed to get worktree %s: %w", worktreeId, err)
		}
		worktrees = append(worktrees, worktree)
	}

	return worktrees, nil
}

func (s Storage) DeleteWorktree(ctx context.Context, workspaceId, worktreeId string) error {
	worktreeKey := fmt.Sprintf("worktree:%s:%s", workspaceId, worktreeId)
	workspaceWorktreesKey := fmt.Sprintf("%s:worktrees", workspaceId)

	pipe := s.Client.Pipeline()
	pipe.Del(ctx, worktreeKey)
	pipe.SRem(ctx, workspaceWorktreesKey, worktreeId)
	_, err := pipe.Exec(ctx)
	if err != nil {
		return fmt.Errorf("failed to delete worktree: %w", err)
	}

	return nil
}
