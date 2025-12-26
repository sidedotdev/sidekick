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

var _ domain.FlowActionStorage = (*Storage)(nil)
var _ domain.FlowActionStreamer = (*Streamer)(nil)

func (s Storage) PersistFlowAction(ctx context.Context, flowAction domain.FlowAction) error {
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

func (s Storage) GetFlowActions(ctx context.Context, workspaceId, flowId string) ([]domain.FlowAction, error) {
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

func (s Storage) GetFlowAction(ctx context.Context, workspaceId, flowActionId string) (domain.FlowAction, error) {
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

func (s Storage) DeleteFlowActionsForFlow(ctx context.Context, workspaceId, flowId string) error {
	panic("DeleteFlowActionsForFlow not implemented for redis storage")
}

// AddFlowActionChange persists a flow action to the changes stream.
func (s Streamer) AddFlowActionChange(ctx context.Context, flowAction domain.FlowAction) error {
	streamKey := fmt.Sprintf("%s:%s:flow_action_changes", flowAction.WorkspaceId, flowAction.FlowId)
	actionParams, err := json.Marshal(flowAction.ActionParams)
	if err != nil {
		log.Println("Failed to marshal action params: ", err)
		return err
	}
	flowActionMap, err := toMap(flowAction)
	if err != nil {
		log.Println("Failed to append flow action to changes stream: ", err)
		return err
	}
	// TODO Maybe we need to do the same for actionResult
	flowActionMap["actionParams"] = string(actionParams)
	err = s.Client.XAdd(ctx, &redis.XAddArgs{
		Stream: streamKey,
		Values: flowActionMap,
	}).Err()
	if err != nil {
		log.Println("Failed to append flow action to changes stream: ", err)
		return err
	}

	return nil
}

func (s Streamer) GetFlowActionChanges(ctx context.Context, workspaceId, flowId, streamMessageStartId string, maxCount int64, blockDuration time.Duration) ([]domain.FlowAction, string, error) {
	streamKey := fmt.Sprintf("%s:%s:flow_action_changes", workspaceId, flowId)
	if streamMessageStartId == "" {
		streamMessageStartId = "0"
	}
	if maxCount == 0 {
		maxCount = 100
	}
	streams, err := s.Client.XRead(ctx, &redis.XReadArgs{
		Streams: []string{streamKey, streamMessageStartId},
		Count:   maxCount,
		Block:   blockDuration,
	}).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, "", nil
		}
		return nil, "", err
	}
	if len(streams) == 0 {
		return nil, "", fmt.Errorf("no streams returned for stream key %s", streamKey)
	}

	// TODO use db.MGet to get all the flow actions at once initially before
	// switching to a stream
	var flowActions []domain.FlowAction
	for i, message := range streams[0].Messages {
		// TODO /gen/req/planned migrate to using "flowAction" key set to
		// flowAction, plus "end" key set to true
		flowActionId, ok := message.Values["id"].(string)
		if !ok {
			return nil, "", fmt.Errorf("missing 'id' key in flow_action_changes message %d: %v", i, message)
		}

		if flowActionId != "end" {
			actionParams := make(map[string]interface{})
			err := json.Unmarshal([]byte(message.Values["actionParams"].(string)), &actionParams)
			if err != nil {
				return nil, "", fmt.Errorf("failed to unmarshal action params: %v", err)
			}
			subflowId, ok := message.Values["subflowId"].(string)
			if !ok {
				subflowId = ""
			}
			description, ok := message.Values["subflowDescription"].(string)
			if !ok {
				description = ""
			}
			isHumanAction, ok := message.Values["isHumanAction"].(string)
			if !ok {
				isHumanAction = ""
			}
			isCallbackAction, ok := message.Values["isCallbackAction"].(string)
			if !ok {
				isCallbackAction = ""
			}
			createdStr, ok := message.Values["created"].(string)
			if !ok {
				return nil, "", fmt.Errorf("missing 'created' key in flow_action_changes message %d: %v", i, message)
			}
			created, err := time.Parse(time.RFC3339Nano, createdStr)
			if err != nil {
				return nil, "", fmt.Errorf("failed to parse 'created' timestamp in message %d: %v", i, err)
			}
			updatedStr, ok := message.Values["updated"].(string)
			if !ok {
				return nil, "", fmt.Errorf("missing 'updated' key in flow_action_changes message %d: %v", i, message)
			}
			updated, err := time.Parse(time.RFC3339Nano, updatedStr)
			if err != nil {
				return nil, "", fmt.Errorf("failed to parse 'updated' timestamp in message %d: %v", i, err)
			}
			flowActions = append(flowActions, domain.FlowAction{
				WorkspaceId:        workspaceId,
				FlowId:             flowId,
				SubflowId:          subflowId,
				Id:                 flowActionId,
				SubflowName:        message.Values["subflow"].(string),
				SubflowDescription: description,
				ActionType:         message.Values["actionType"].(string),
				ActionStatus:       message.Values["actionStatus"].(string),
				ActionParams:       actionParams,
				ActionResult:       message.Values["actionResult"].(string),
				IsHumanAction:      isHumanAction == "1",
				IsCallbackAction:   isCallbackAction == "1",
				Created:            created,
				Updated:            updated,
			})
		} else {
			return flowActions, "end", nil
		}
	}

	// Return the last message id value to continue from
	lastMessageId := streams[0].Messages[len(streams[0].Messages)-1].ID

	return flowActions, lastMessageId, nil
}

func (s *Streamer) StreamFlowActionChanges(ctx context.Context, workspaceId, flowId, streamMessageStartId string) (<-chan domain.FlowAction, <-chan error) {
	flowActionChan := make(chan domain.FlowAction)
	errChan := make(chan error, 1)

	go func() {
		defer close(flowActionChan)
		defer close(errChan)

		continueMessageId := streamMessageStartId
		for {
			select {
			case <-ctx.Done():
				return
			default:
				blockDuration := 250 * time.Millisecond
				flowActions, latestContinueMessageId, err := s.GetFlowActionChanges(ctx, workspaceId, flowId, continueMessageId, 100, blockDuration)
				if err != nil {
					errChan <- err
					return
				}

				for _, flowAction := range flowActions {
					select {
					case <-ctx.Done():
						return
					case flowActionChan <- flowAction:
					}
				}

				continueMessageId = latestContinueMessageId
			}
		}
	}()

	return flowActionChan, errChan
}
