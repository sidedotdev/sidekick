package redis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sidekick/domain"
	"sidekick/srv"

	"github.com/redis/go-redis/v9"
)

func (s Service) PersistWorkflow(ctx context.Context, flow domain.Flow) error {
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

func (s Service) GetFlowsForTask(ctx context.Context, workspaceId, taskId string) ([]domain.Flow, error) {
	parentId := taskId
	return s.GetChildFlows(ctx, workspaceId, parentId)
}

func (s Service) GetChildFlows(ctx context.Context, workspaceId, parentId string) ([]domain.Flow, error) {
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
		flow, err := s.GetWorkflow(ctx, workspaceId, flowId)
		if err != nil {
			return nil, fmt.Errorf("failed to get flow record for parentId=%s and flowId=%s: %w", parentId, flowId, err)
		}
		flows = append(flows, flow)
	}

	return flows, nil
}

func (s Service) GetWorkflow(ctx context.Context, workspaceId, flowId string) (domain.Flow, error) {
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

// PersistSubflow stores a Subflow model in Redis and updates the flow's subflow set
func (s Service) PersistSubflow(ctx context.Context, subflow domain.Subflow) error {
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
func (s Service) GetSubflows(ctx context.Context, workspaceId, flowId string) ([]domain.Subflow, error) {
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

func (s Service) PersistFlowAction(ctx context.Context, flowAction domain.FlowAction) error {
	if flowAction.Id == "" {
		return fmt.Errorf("missing Id field in FlowAction model")
	}
	if flowAction.FlowId == "" {
		return fmt.Errorf("missing FlowId field in FlowAction model")
	}
	if flowAction.WorkspaceId == "" {
		return fmt.Errorf("missing WorkspaceId field in FlowAction model")
	}

	flowActionJson, err := json.Marshal(flowAction)
	if err != nil {
		log.Println("Failed to convert flow action record to JSON: ", err)
		return err
	}

	// Check if the flow action is new
	key := fmt.Sprintf("%s:%s", flowAction.WorkspaceId, flowAction.Id)
	exists, err := s.Client.Exists(ctx, key).Result()
	if err != nil {
		log.Println("Failed to check if flow action exists in Redis: ", err)
		return err
	}

	// Persist the flow action record itself
	err = s.Client.Set(ctx, key, flowActionJson, 0).Err()
	if err != nil {
		log.Println("Failed to persist flow action to Redis: ", err)
		return err
	}

	// FIXME move this to the caller to orchestrate between the storage and stream services
	s.AddFlowActionChange(ctx, flowAction)

	// If the flow action is new, append its ID to a list of flow action IDs
	if exists == 0 {
		listKey := fmt.Sprintf("%s:%s:flow_action_ids", flowAction.WorkspaceId, flowAction.FlowId)
		err = s.Client.RPush(ctx, listKey, flowAction.Id).Err()
		if err != nil {
			log.Println("Failed to append flow action ID to Redis stream: ", err)
			return err
		}
	}

	return nil
}

func (s Service) GetFlowActions(ctx context.Context, workspaceId, flowId string) ([]domain.FlowAction, error) {
	listKey := fmt.Sprintf("%s:%s:flow_action_ids", workspaceId, flowId)
	ids, err := s.Client.LRange(ctx, listKey, 0, -1).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, nil
		}
		log.Println("Failed to get flow action IDs from Redis: ", err)
		return nil, err
	}

	var flowActions []domain.FlowAction
	for _, id := range ids {
		key := fmt.Sprintf("%s:%s", workspaceId, id)
		flowActionJson, err := s.Client.Get(ctx, key).Result()
		if err != nil {
			log.Println("Failed to get flow action from Redis: ", err)
			return nil, err
		}

		var flowAction domain.FlowAction
		err = json.Unmarshal([]byte(flowActionJson), &flowAction)
		if err != nil {
			log.Println("Failed to unmarshal flow action JSON: ", err)
			return nil, err
		}

		flowActions = append(flowActions, flowAction)
	}

	return flowActions, nil
}

func (s Service) GetFlowAction(ctx context.Context, workspaceId, flowActionId string) (domain.FlowAction, error) {
	key := fmt.Sprintf("%s:%s", workspaceId, flowActionId)
	val, err := s.Client.Get(ctx, key).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return domain.FlowAction{}, srv.ErrNotFound
		}
		return domain.FlowAction{}, err
	}

	var flowAction domain.FlowAction
	err = json.Unmarshal([]byte(val), &flowAction)
	if err != nil {
		return domain.FlowAction{}, err
	}

	return flowAction, nil
}
