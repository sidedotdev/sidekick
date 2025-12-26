package redis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sidekick/domain"
	"sidekick/srv"
	"time"

	"github.com/redis/go-redis/v9"
)

func (s Storage) PersistFlow(ctx context.Context, flow domain.Flow) error {
	now := time.Now().UTC()
	if flow.Created.IsZero() {
		flow.Created = now
	} else {
		flow.Created = flow.Created.UTC()
	}
	if flow.Updated.IsZero() {
		flow.Updated = now
	} else {
		flow.Updated = flow.Updated.UTC()
	}

	workflowJson, err := json.Marshal(flow)
	if err != nil {
		log.Println("Failed to convert topic record to JSON: ", err)
		return err
	}

	workflowKey := fmt.Sprintf("%s:%s", flow.WorkspaceId, flow.Id)
	err = s.Client.Set(ctx, workflowKey, workflowJson, 0).Err()
	if err != nil {
		log.Println("Failed to persist workflow record to Redis: ", err)
		return err
	}

	// allow querying all flows for a given parent_id
	parentFlowsKey := fmt.Sprintf("%s:%s:flows", flow.WorkspaceId, flow.ParentId)
	err = s.Client.SAdd(ctx, parentFlowsKey, flow.Id).Err()
	if err != nil {
		log.Println("Failed to add workflow id to parent flows set: ", err)
		return err
	}

	return nil
}

func (s Storage) GetFlowsForTask(ctx context.Context, workspaceId, taskId string) ([]domain.Flow, error) {
	parentId := taskId
	return s.GetChildFlows(ctx, workspaceId, parentId)
}

func (s Storage) GetChildFlows(ctx context.Context, workspaceId, parentId string) ([]domain.Flow, error) {
	flowsKey := fmt.Sprintf("%s:%s:flows", workspaceId, parentId)
	flowIds, err := s.Client.SMembers(ctx, flowsKey).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get flow ids from parentId=%s set: %w", parentId, err)
	}

	flows := make([]domain.Flow, 0)
	for _, flowId := range flowIds {
		flow, err := s.GetFlow(ctx, workspaceId, flowId)
		if err != nil {
			return nil, fmt.Errorf("failed to get flow record for parentId=%s and flowId=%s: %w", parentId, flowId, err)
		}
		flows = append(flows, flow)
	}

	return flows, nil
}

func (s Storage) GetFlow(ctx context.Context, workspaceId, flowId string) (domain.Flow, error) {
	workflowKey := fmt.Sprintf("%s:%s", workspaceId, flowId)
	workflowJson, err := s.Client.Get(ctx, workflowKey).Result()
	if err != nil {
		if err == redis.Nil {
			return domain.Flow{}, srv.ErrNotFound
		}
		log.Printf("Failed to get workflow record: %v\n", err)
		return domain.Flow{}, err
	}

	var flow domain.Flow
	if err := json.Unmarshal([]byte(workflowJson), &flow); err != nil {
		log.Println("Failed to unmarshal workflow record: ", err)
		return domain.Flow{}, err
	}

	return flow, nil
}

func (s Storage) DeleteFlow(ctx context.Context, workspaceId, flowId string) error {
	// Load the flow to get the ParentId for set removal
	flow, err := s.GetFlow(ctx, workspaceId, flowId)
	if err != nil {
		if errors.Is(err, srv.ErrNotFound) {
			return nil // Already deleted, idempotent
		}
		return fmt.Errorf("failed to get flow for deletion: %w", err)
	}

	flowKey := fmt.Sprintf("%s:%s", workspaceId, flowId)
	parentFlowsKey := fmt.Sprintf("%s:%s:flows", workspaceId, flow.ParentId)

	pipe := s.Client.Pipeline()
	pipe.Del(ctx, flowKey)
	pipe.SRem(ctx, parentFlowsKey, flowId)
	_, err = pipe.Exec(ctx)
	if err != nil {
		return fmt.Errorf("failed to delete flow: %w", err)
	}

	return nil
}
