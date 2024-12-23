package redis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sidekick/domain"
)

// PersistSubflow stores a Subflow model in Redis and updates the flow's subflow set
func (s Storage) PersistSubflow(ctx context.Context, subflow domain.Subflow) error {
	if subflow.WorkspaceId == "" || subflow.Id == "" || subflow.FlowId == "" {
		return errors.New("workspaceId, subflow.Id, and subflow.FlowId cannot be empty")
	}

	subflowKey := fmt.Sprintf("%s:%s", subflow.WorkspaceId, subflow.Id)
	subflowSetKey := fmt.Sprintf("%s:%s:subflows", subflow.WorkspaceId, subflow.FlowId)

	subflowJSON, err := json.Marshal(subflow)
	if err != nil {
		return fmt.Errorf("failed to marshal subflow: %w", err)
	}

	pipe := s.Client.Pipeline()
	pipe.Set(ctx, subflowKey, subflowJSON, 0)
	pipe.SAdd(ctx, subflowSetKey, subflow.Id)

	_, err = pipe.Exec(ctx)

	return nil
}

// GetSubflows retrieves all Subflow models for a given flow ID
func (s Storage) GetSubflows(ctx context.Context, workspaceId, flowId string) ([]domain.Subflow, error) {
	if workspaceId == "" || flowId == "" {
		return nil, errors.New("workspaceId and flowId cannot be empty")
	}

	subflowSetKey := fmt.Sprintf("%s:%s:subflows", workspaceId, flowId)

	subflowIds, err := s.Client.SMembers(ctx, subflowSetKey).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve subflow IDs: %w", err)
	}

	subflows := make([]domain.Subflow, 0, len(subflowIds))
	for _, subflowId := range subflowIds {
		subflowKey := fmt.Sprintf("%s:%s", workspaceId, subflowId)
		subflowJSON, err := s.Client.Get(ctx, subflowKey).Result()
		if err != nil {
			return nil, fmt.Errorf("failed to retrieve subflow %s: %w", subflowId, err)
		}

		var subflow domain.Subflow
		if err := json.Unmarshal([]byte(subflowJSON), &subflow); err != nil {
			return nil, fmt.Errorf("failed to unmarshal subflow %s: %w", subflowId, err)
		}

		subflows = append(subflows, subflow)
	}

	return subflows, nil
}
