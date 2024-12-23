package redis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sidekick/domain"
	"sidekick/srv"
	"sidekick/utils"
	"time"

	"github.com/redis/go-redis/v9"
)


func (s Service) PersistTask(ctx context.Context, task domain.Task) error {
	taskJson, err := json.Marshal(task)
	if err != nil {
		log.Println("Failed to convert task record to JSON: ", err)
		return err
	}

	// Persist the task record itself
	key := fmt.Sprintf("%s:%s", task.WorkspaceId, task.Id)
	err = s.Client.Set(ctx, key, taskJson, 0).Err()
	if err != nil {
		log.Println("Failed to persist task to Redis: ", err)
		return err
	}

	// Add the task id to the appropriate set based on the task status, and remove from others
	for _, status := range []domain.TaskStatus{domain.TaskStatusDrafting, domain.TaskStatusToDo, domain.TaskStatusInProgress, domain.TaskStatusComplete, domain.TaskStatusBlocked, domain.TaskStatusFailed, domain.TaskStatusCanceled} {
		statusKey := fmt.Sprintf("%s:kanban:%s", task.WorkspaceId, status)
		if status == task.Status {
			err = s.Client.SAdd(ctx, statusKey, task.Id).Err()
			if err != nil {
				log.Println("Failed to add task id to set: ", err)
				return err
			}
		} else {
			err = s.Client.SRem(ctx, statusKey, task.Id).Err()
			if err != nil {
				log.Println("Failed to remove task id from set: ", err)
				return err
			}
		}
	}

	// FIXME move this to the caller to orchestrate between the storage and stream services
	err = s.AddTaskChange(ctx, task)
	if err != nil {
		log.Println("Failed to add task change: ", err)
		return err
	}

	return nil
}

func (s Service) DeleteTask(ctx context.Context, workspaceId, taskId string) error {
	task, err := s.GetTask(ctx, workspaceId, taskId)
	if err != nil {
		return err
	}

	key := fmt.Sprintf("%s:%s", workspaceId, taskId)
	err = s.Client.Del(ctx, key).Err()
	if err != nil {
		log.Println("Failed to delete task from main record in Redis: ", err)
		return err
	}

	// Delete task from kanban sets
	kanbanKey := fmt.Sprintf("%s:kanban:%s", workspaceId, task.Status)
	err = s.Client.SRem(ctx, kanbanKey, taskId).Err()
	if err != nil {
		log.Println("Failed to delete task from kanban sets in Redis: ", err)
		return err
	}

	return nil
}


func (s Service) GetTasks(ctx context.Context, workspaceId string, statuses []domain.TaskStatus) ([]domain.Task, error) {
	var taskIds []string
	for _, status := range statuses {
		statusKey := fmt.Sprintf("%s:kanban:%s", workspaceId, status)
		statusTaskIds, err := s.Client.SMembers(ctx, statusKey).Result()
		if err != nil {
			if errors.Is(err, redis.Nil) {
				log.Printf("Missing kanban task ids for %s status set: %v\n", status, err)
				continue
			}
			return nil, err
		}
		taskIds = append(taskIds, statusTaskIds...)
	}

	taskKeys := make([]string, len(taskIds))
	for i, taskId := range taskIds {
		taskKeys[i] = fmt.Sprintf("%s:%s", workspaceId, taskId)
	}

	var taskJsons []interface{}
	var err error

	if len(taskKeys) > 0 {
		taskJsons, err = s.Client.MGet(ctx, taskKeys...).Result()
		if err != nil {
			log.Println("Failed to get tasks from Redis: ", err)
			return nil, err
		}
	}

	var tasks []domain.Task
	for _, taskJson := range taskJsons {
		var task domain.Task
		err = json.Unmarshal([]byte(taskJson.(string)), &task)
		if err != nil {
			log.Println("Failed to unmarshal task: ", err)
			continue
		}
		tasks = append(tasks, task)
	}

	return tasks, nil
}

// AddFlowActionChange persists a flow action to the changes stream.
// TODO /gen add to the FlowStreamService interface
func (s Service) AddFlowActionChange(ctx context.Context, flowAction domain.FlowAction) error {
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

func (s Service) GetFlowActionChanges(ctx context.Context, workspaceId, flowId, streamMessageStartId string, maxCount int64, blockDuration time.Duration) ([]domain.FlowAction, string, error) {
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
			created, _ := time.Parse(message.Values["created"].(string), time.RFC3339)
			updated, _ := time.Parse(message.Values["updated"].(string), time.RFC3339)
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

func (s Service) GetTask(ctx context.Context, workspaceId string, taskId string) (domain.Task, error) {
	key := fmt.Sprintf("%s:%s", workspaceId, taskId)
	taskRecord, err := s.Client.Get(ctx, key).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return domain.Task{}, srv.ErrNotFound
		}
		return domain.Task{}, err
	}
	var task domain.Task
	err = json.Unmarshal([]byte(taskRecord), &task)
	if err != nil {
		return domain.Task{}, err
	}
	return task, nil
}

// AddTaskChange persists a task to the changes stream.
// TODO /gen add to the TaskStreamService interface
func (s Service) AddTaskChange(ctx context.Context, task domain.Task) error {
	streamKey := fmt.Sprintf("%s:task_changes", task.WorkspaceId)
	taskMap, err := toMap(task)
	if err != nil {
		return fmt.Errorf("AddTaskChange - failed to convert task to map: %w", err)
	}
	flowOptions, err := json.Marshal(task.FlowOptions)
	if err != nil {
		return fmt.Errorf("AddTaskChange - failed to marshal flow options: %w", err)
	}
	taskMap["flowOptions"] = string(flowOptions)

	err = s.Client.XAdd(ctx, &redis.XAddArgs{
		Stream: streamKey,
		Values: taskMap,
	}).Err()
	if err != nil {
		return fmt.Errorf("AddTaskChange - failed to append task to changes stream: %w", err)
	}

	return nil
}

func (s Service) GetTaskChanges(ctx context.Context, workspaceId, streamMessageStartId string, maxCount int64, blockDuration time.Duration) ([]domain.Task, string, error) {
	streamKey := fmt.Sprintf("%s:task_changes", workspaceId)
	if streamMessageStartId == "" {
		streamMessageStartId = "$"
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
		return nil, "", err
	}
	if len(streams) == 0 {
		return nil, "", fmt.Errorf("no streams returned for stream key %s", streamKey)
	}

	var tasks []domain.Task
	for _, message := range streams[0].Messages {
		var task domain.Task
		utils.Transcode(message.Values, &task)
		tasks = append(tasks, task)
	}

	// Return the last message id value to continue from
	lastMessageId := streams[0].Messages[len(streams[0].Messages)-1].ID

	return tasks, lastMessageId, nil
}