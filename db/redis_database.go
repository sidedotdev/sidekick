package db

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"

	"os"
	"sidekick/models"
	"sidekick/utils"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	zlog "github.com/rs/zerolog/log"
)

var ErrNotFound = errors.New("not found")

func (db RedisDatabase) CheckConnection(ctx context.Context) error {
	_, err := db.Client.Ping(context.Background()).Result()
	return err
}

func (db RedisDatabase) GetWorkspaceConfig(ctx context.Context, workspaceId string) (models.WorkspaceConfig, error) {
	key := fmt.Sprintf("%s:workspace_config", workspaceId)
	configJson, err := db.Client.Get(ctx, key).Result()
	if err != nil {
		if err == redis.Nil {
			return models.WorkspaceConfig{}, ErrNotFound
		}
		return models.WorkspaceConfig{}, fmt.Errorf("failed to get workspace config from Redis: %w", err)
	}

	var config models.WorkspaceConfig
	if err := json.Unmarshal([]byte(configJson), &config); err != nil {
		return models.WorkspaceConfig{}, fmt.Errorf("failed to unmarshal workspace config: %w", err)
	}

	return config, nil
}

func (db RedisDatabase) PersistWorkspaceConfig(ctx context.Context, workspaceId string, config models.WorkspaceConfig) error {
	if workspaceId == "" {
		return fmt.Errorf("workspaceId cannot be empty")
	}

	key := fmt.Sprintf("%s:workspace_config", workspaceId)
	configJson, err := json.Marshal(config)
	if err != nil {
		return fmt.Errorf("failed to marshal workspace config: %w", err)
	}

	if err := db.Client.Set(ctx, key, configJson, 0).Err(); err != nil {
		return fmt.Errorf("failed to persist workspace config to Redis: %w", err)
	}

	return nil
}

type RedisDatabase struct {
	Client *redis.Client
}

// PersistSubflow stores a Subflow model in Redis and updates the flow's subflow set
func (db RedisDatabase) PersistSubflow(ctx context.Context, subflow models.Subflow) error {
	if subflow.WorkspaceId == "" || subflow.Id == "" || subflow.FlowId == "" {
		return errors.New("workspaceId, subflow.Id, and subflow.FlowId cannot be empty")
	}

	subflowKey := fmt.Sprintf("%s:%s", subflow.WorkspaceId, subflow.Id)
	subflowSetKey := fmt.Sprintf("%s:%s:subflows", subflow.WorkspaceId, subflow.FlowId)

	subflowJSON, err := json.Marshal(subflow)
	if err != nil {
		return fmt.Errorf("failed to marshal subflow: %w", err)
	}

	pipe := db.Client.Pipeline()
	pipe.Set(ctx, subflowKey, subflowJSON, 0)
	pipe.SAdd(ctx, subflowSetKey, subflow.Id)

	_, err = pipe.Exec(ctx)
	if err != nil {
		return fmt.Errorf("failed to persist subflow: %w", err)
	}

	return nil
}

// GetSubflows retrieves all Subflow models for a given flow ID
func (db RedisDatabase) GetSubflows(ctx context.Context, workspaceId, flowId string) ([]models.Subflow, error) {
	if workspaceId == "" || flowId == "" {
		return nil, errors.New("workspaceId and flowId cannot be empty")
	}

	subflowSetKey := fmt.Sprintf("%s:%s:subflows", workspaceId, flowId)

	subflowIds, err := db.Client.SMembers(ctx, subflowSetKey).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve subflow IDs: %w", err)
	}

	subflows := make([]models.Subflow, 0, len(subflowIds))
	for _, subflowId := range subflowIds {
		subflowKey := fmt.Sprintf("%s:%s", workspaceId, subflowId)
		subflowJSON, err := db.Client.Get(ctx, subflowKey).Result()
		if err != nil {
			return nil, fmt.Errorf("failed to retrieve subflow %s: %w", subflowId, err)
		}

		var subflow models.Subflow
		if err := json.Unmarshal([]byte(subflowJSON), &subflow); err != nil {
			return nil, fmt.Errorf("failed to unmarshal subflow %s: %w", subflowId, err)
		}

		subflows = append(subflows, subflow)
	}

	return subflows, nil
}

func NewRedisDatabase() *RedisDatabase {
	redisAddr := os.Getenv("REDIS_ADDRESS")
	if redisAddr == "" {
		zlog.Info().Msg("Redis address defaulting to localhost:6379")
		redisAddr = "localhost:6379" // Default address
	}
	rdb := redis.NewClient(&redis.Options{
		Addr: redisAddr,
	})
	return &RedisDatabase{Client: rdb}
}

func (db RedisDatabase) GetMessages(ctx context.Context, workspaceId, topicId string) ([]models.Message, error) {
	sortedSetKey := fmt.Sprintf("%s:%s:messages_sorted_set", workspaceId, topicId)
	messageIDs, err := db.Client.ZRange(ctx, sortedSetKey, 0, -1).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to get message IDs from sorted set: %w", err)
	}

	messages := make([]models.Message, 0)
	for _, messageID := range messageIDs {
		key := fmt.Sprintf("%s:%s:%s", workspaceId, topicId, messageID)
		messageJson, err := db.Client.Get(ctx, key).Result()

		if err != nil {
			return []models.Message{}, fmt.Errorf("failed to get message from Redis: %w", err)
		}

		var messageRecord models.Message
		if err := json.Unmarshal([]byte(messageJson), &messageRecord); err != nil {
			log.Println("Failed to unmarshal message record: ", err)
			return []models.Message{}, err
		}
		messages = append(messages, messageRecord)
	}

	return messages, nil
}

func (db RedisDatabase) PersistWorkflow(ctx context.Context, flow models.Flow) error {
	workflowJson, err := json.Marshal(flow)
	if err != nil {
		log.Println("Failed to convert topic record to JSON: ", err)
		return err
	}

	workflowKey := fmt.Sprintf("%s:%s", flow.WorkspaceId, flow.Id)
	err = db.Client.Set(ctx, workflowKey, workflowJson, 0).Err()
	if err != nil {
		log.Println("Failed to persist workflow record to Redis: ", err)
		return err
	}

	// allow querying all flows for a given parent_id
	parentFlowsKey := fmt.Sprintf("%s:%s:flows", flow.WorkspaceId, flow.ParentId)
	err = db.Client.SAdd(ctx, parentFlowsKey, flow.Id).Err()
	if err != nil {
		log.Println("Failed to add workflow id to parent flows set: ", err)
		return err
	}

	return nil
}

func (db RedisDatabase) GetFlowsForTask(ctx context.Context, workspaceId, taskId string) ([]models.Flow, error) {
	parentId := taskId
	return db.GetChildFlows(ctx, workspaceId, parentId)
}

func (db RedisDatabase) GetChildFlows(ctx context.Context, workspaceId, parentId string) ([]models.Flow, error) {
	flowsKey := fmt.Sprintf("%s:%s:flows", workspaceId, parentId)
	flowIds, err := db.Client.SMembers(ctx, flowsKey).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get flow ids from parentId=%s set: %w", parentId, err)
	}

	flows := make([]models.Flow, 0)
	for _, flowId := range flowIds {
		flow, err := db.GetWorkflow(ctx, workspaceId, flowId)
		if err != nil {
			return nil, fmt.Errorf("failed to get flow record for parentId=%s and flowId=%s: %w", parentId, flowId, err)
		}
		flows = append(flows, flow)
	}

	return flows, nil
}

func (db RedisDatabase) GetWorkflow(ctx context.Context, workspaceId, flowId string) (models.Flow, error) {
	workflowKey := fmt.Sprintf("%s:%s", workspaceId, flowId)
	workflowJson, err := db.Client.Get(ctx, workflowKey).Result()
	if err != nil {
		if err == redis.Nil {
			return models.Flow{}, ErrNotFound
		}
		log.Printf("Failed to get workflow record: %v\n", err)
		return models.Flow{}, err
	}

	var flow models.Flow
	if err := json.Unmarshal([]byte(workflowJson), &flow); err != nil {
		log.Println("Failed to unmarshal workflow record: ", err)
		return models.Flow{}, err
	}

	return flow, nil
}

func (db RedisDatabase) PersistTopic(ctx context.Context, topicRecord models.Topic) error {
	topicJson, err := json.Marshal(topicRecord)
	if err != nil {
		log.Println("Failed to convert topic record to JSON: ", err)
		return err
	}

	// Add the topic to the sorted set for the workspace id
	sortedSetKey := fmt.Sprintf("%s:topics_sorted_set", topicRecord.WorkspaceId)
	err = db.Client.ZAdd(ctx, sortedSetKey, redis.Z{
		Score:  float64(topicRecord.Updated.UnixMilli()),
		Member: topicRecord.Id,
	}).Err()
	if err != nil {
		return fmt.Errorf("failed to add topic to sorted set: %w", err)
	}

	// Persist the topic record itself
	key := fmt.Sprintf("%s:%s", topicRecord.WorkspaceId, topicRecord.Id)
	err = db.Client.Set(ctx, key, topicJson, 0).Err()
	if err != nil {
		log.Println("Failed to persist topic to Redis: ", err)
		return err
	}

	return nil
}

func (db RedisDatabase) TopicExists(ctx context.Context, workspaceId, topicId string) (bool, error) {
	// Check if the topic exists in Redis
	key := fmt.Sprintf("%s:%s", workspaceId, topicId)
	_, err := db.Client.Get(ctx, key).Result()

	if err != nil && err != redis.Nil {
		return false, fmt.Errorf("failed to get topic from Redis: %w", err)
	}

	return err != redis.Nil, nil
}

func (db RedisDatabase) PersistMessage(ctx context.Context, messageRecord models.Message) error {
	// Add the message to the sorted set for the topic id
	sortedSetKey := fmt.Sprintf("%s:%s:messages_sorted_set", messageRecord.WorkspaceId, messageRecord.TopicId)
	err := db.Client.ZAdd(ctx, sortedSetKey, redis.Z{
		Score:  float64(messageRecord.Created.UnixMilli()),
		Member: messageRecord.Id,
	}).Err()
	if err != nil {
		return fmt.Errorf("failed to add message to sorted set: %w", err)
	}

	jsonData, err := json.Marshal(messageRecord)
	if err != nil {
		return fmt.Errorf("failed to convert to json when persisting message: %w", err)
	}
	key := fmt.Sprintf("%s:%s:%s", messageRecord.WorkspaceId, messageRecord.TopicId, messageRecord.Id)
	err = db.Client.Set(ctx, key, jsonData, 0).Err()
	if err != nil {
		return fmt.Errorf("failed to persist message to Redis: %w", err)
	}

	return nil
}

func toMap(something interface{}) (map[string]interface{}, error) {
	// Convert the thing to JSON
	jsonData, err := json.Marshal(something)
	if err != nil {
		return nil, fmt.Errorf("failed to convert something to JSON: %w", err)
	}

	// Convert the JSON data to a map
	var dataMap map[string]interface{}
	err = json.Unmarshal(jsonData, &dataMap)
	if err != nil {
		return nil, fmt.Errorf("failed to convert JSON to map: %w", err)
	}

	return dataMap, nil
}

func (db RedisDatabase) GetTopics(ctx context.Context, workspaceId string) ([]models.Topic, error) {
	sortedSetKey := fmt.Sprintf("%s:topics_sorted_set", workspaceId)
	topicIds, err := db.Client.ZRange(ctx, sortedSetKey, 0, -1).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to get topics from sorted set: %w", err)
	}

	var topics []models.Topic
	for _, topicId := range topicIds {
		key := fmt.Sprintf("%s:%s", workspaceId, topicId)
		topicJson, err := db.Client.Get(ctx, key).Result()
		if err != nil {
			return nil, fmt.Errorf("failed to get topic from Redis: %w", err)
		}

		var topic models.Topic
		err = json.Unmarshal([]byte(topicJson), &topic)
		if err != nil {
			return nil, fmt.Errorf("failed to unmarshal topic JSON: %w", err)
		}

		topics = append(topics, topic)
	}

	return topics, nil
}
func (db RedisDatabase) MGet(ctx context.Context, keys []string) ([]interface{}, error) {
	return db.Client.MGet(ctx, keys...).Result()
}

func (db RedisDatabase) MSet(ctx context.Context, values map[string]interface{}) error {
	return db.Client.MSet(ctx, values).Err()
}

func (db RedisDatabase) DeleteTask(ctx context.Context, workspaceId, taskId string) error {
	task, err := db.GetTask(ctx, workspaceId, taskId)
	if err != nil {
		return err
	}

	key := fmt.Sprintf("%s:%s", workspaceId, taskId)
	err = db.Client.Del(ctx, key).Err()
	if err != nil {
		log.Println("Failed to delete task from main record in Redis: ", err)
		return err
	}

	// Delete task from kanban sets
	kanbanKey := fmt.Sprintf("%s:kanban:%s", workspaceId, task.Status)
	err = db.Client.SRem(ctx, kanbanKey, taskId).Err()
	if err != nil {
		log.Println("Failed to delete task from kanban sets in Redis: ", err)
		return err
	}

	return nil
}

func (db RedisDatabase) PersistTask(ctx context.Context, task models.Task) error {
	taskJson, err := json.Marshal(task)
	if err != nil {
		log.Println("Failed to convert task record to JSON: ", err)
		return err
	}

	// Persist the task record itself
	key := fmt.Sprintf("%s:%s", task.WorkspaceId, task.Id)
	err = db.Client.Set(ctx, key, taskJson, 0).Err()
	if err != nil {
		log.Println("Failed to persist task to Redis: ", err)
		return err
	}

	// Add the task id to the appropriate set based on the task status, and remove from others
	for _, status := range []models.TaskStatus{models.TaskStatusDrafting, models.TaskStatusToDo, models.TaskStatusInProgress, models.TaskStatusComplete, models.TaskStatusBlocked, models.TaskStatusFailed, models.TaskStatusCanceled} {
		statusKey := fmt.Sprintf("%s:kanban:%s", task.WorkspaceId, status)
		if status == task.Status {
			err = db.Client.SAdd(ctx, statusKey, task.Id).Err()
			if err != nil {
				log.Println("Failed to add task id to set: ", err)
				return err
			}
		} else {
			err = db.Client.SRem(ctx, statusKey, task.Id).Err()
			if err != nil {
				log.Println("Failed to remove task id from set: ", err)
				return err
			}
		}
	}

	err = db.AddTaskChange(ctx, task)
	if err != nil {
		log.Println("Failed to add task change: ", err)
		return err
	}

	return nil
}

func (db RedisDatabase) GetTasks(ctx context.Context, workspaceId string, statuses []models.TaskStatus) ([]models.Task, error) {
	var taskIds []string
	for _, status := range statuses {
		statusKey := fmt.Sprintf("%s:kanban:%s", workspaceId, status)
		statusTaskIds, err := db.Client.SMembers(ctx, statusKey).Result()
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
		taskJsons, err = db.Client.MGet(ctx, taskKeys...).Result()
		if err != nil {
			log.Println("Failed to get tasks from Redis: ", err)
			return nil, err
		}
	}

	var tasks []models.Task
	for _, taskJson := range taskJsons {
		var task models.Task
		err = json.Unmarshal([]byte(taskJson.(string)), &task)
		if err != nil {
			log.Println("Failed to unmarshal task: ", err)
			continue
		}
		tasks = append(tasks, task)
	}

	return tasks, nil
}

func (db RedisDatabase) PersistFlowAction(ctx context.Context, flowAction models.FlowAction) error {
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
	exists, err := db.Client.Exists(ctx, key).Result()
	if err != nil {
		log.Println("Failed to check if flow action exists in Redis: ", err)
		return err
	}

	// Persist the flow action record itself
	err = db.Client.Set(ctx, key, flowActionJson, 0).Err()
	if err != nil {
		log.Println("Failed to persist flow action to Redis: ", err)
		return err
	}

	db.AddFlowActionChange(ctx, flowAction)

	// If the flow action is new, append its ID to a list of flow action IDs
	if exists == 0 {
		listKey := fmt.Sprintf("%s:%s:flow_action_ids", flowAction.WorkspaceId, flowAction.FlowId)
		err = db.Client.RPush(ctx, listKey, flowAction.Id).Err()
		if err != nil {
			log.Println("Failed to append flow action ID to Redis stream: ", err)
			return err
		}
	}

	return nil
}

// AddFlowActionChange persists a flow action to the changes stream.
func (db RedisDatabase) AddFlowActionChange(ctx context.Context, flowAction models.FlowAction) error {
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
	err = db.Client.XAdd(ctx, &redis.XAddArgs{
		Stream: streamKey,
		Values: flowActionMap,
	}).Err()
	if err != nil {
		log.Println("Failed to append flow action to changes stream: ", err)
		return err
	}

	return nil
}

func (db RedisDatabase) GetFlowActionChanges(ctx context.Context, workspaceId, flowId, streamMessageStartId string, maxCount int64, blockDuration time.Duration) ([]models.FlowAction, string, error) {
	streamKey := fmt.Sprintf("%s:%s:flow_action_changes", workspaceId, flowId)
	if streamMessageStartId == "" {
		streamMessageStartId = "0"
	}
	if maxCount == 0 {
		maxCount = 100
	}
	streams, err := db.Client.XRead(ctx, &redis.XReadArgs{
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
	var flowActions []models.FlowAction
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
			flowActions = append(flowActions, models.FlowAction{
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

func (db RedisDatabase) GetFlowActions(ctx context.Context, workspaceId, flowId string) ([]models.FlowAction, error) {
	listKey := fmt.Sprintf("%s:%s:flow_action_ids", workspaceId, flowId)
	ids, err := db.Client.LRange(ctx, listKey, 0, -1).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, nil
		}
		log.Println("Failed to get flow action IDs from Redis: ", err)
		return nil, err
	}

	var flowActions []models.FlowAction
	for _, id := range ids {
		key := fmt.Sprintf("%s:%s", workspaceId, id)
		flowActionJson, err := db.Client.Get(ctx, key).Result()
		if err != nil {
			log.Println("Failed to get flow action from Redis: ", err)
			return nil, err
		}

		var flowAction models.FlowAction
		err = json.Unmarshal([]byte(flowActionJson), &flowAction)
		if err != nil {
			log.Println("Failed to unmarshal flow action JSON: ", err)
			return nil, err
		}

		flowActions = append(flowActions, flowAction)
	}

	return flowActions, nil
}

func (db RedisDatabase) GetFlowAction(ctx context.Context, workspaceId, flowActionId string) (models.FlowAction, error) {
	key := fmt.Sprintf("%s:%s", workspaceId, flowActionId)
	val, err := db.Client.Get(ctx, key).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return models.FlowAction{}, ErrNotFound
		}
		return models.FlowAction{}, err
	}

	var flowAction models.FlowAction
	err = json.Unmarshal([]byte(val), &flowAction)
	if err != nil {
		return models.FlowAction{}, err
	}

	return flowAction, nil
}

func (db RedisDatabase) GetTask(ctx context.Context, workspaceId string, taskId string) (models.Task, error) {
	key := fmt.Sprintf("%s:%s", workspaceId, taskId)
	taskRecord, err := db.Client.Get(ctx, key).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return models.Task{}, ErrNotFound
		}
		return models.Task{}, err
	}
	var task models.Task
	err = json.Unmarshal([]byte(taskRecord), &task)
	if err != nil {
		return models.Task{}, err
	}
	return task, nil
}
func (db RedisDatabase) PersistWorkspace(ctx context.Context, workspace models.Workspace) error {
	workspaceJson, err := json.Marshal(workspace)
	if err != nil {
		log.Println("Failed to convert workspace to JSON: ", err)
		return err
	}
	key := fmt.Sprintf("workspace:%s", workspace.Id)
	err = db.Client.Set(ctx, key, workspaceJson, 0).Err()
	if err != nil {
		log.Println("Failed to persist workspace to Redis: ", err)
		return err
	}

	// Add workspace to sorted set by workspace name
	err = db.Client.ZAdd(ctx, "global:workspaces", redis.Z{
		Score:  0,                                   // score is not used, we rely on the lexographical ordering of the member
		Member: workspace.Name + ":" + workspace.Id, // use name as member prefix to sort by name
	}).Err()
	if err != nil {
		log.Println("Failed to add workspace to sorted set: ", err)
		return err
	}

	return nil
}
func (db RedisDatabase) GetWorkspace(ctx context.Context, workspaceId string) (models.Workspace, error) {
	key := fmt.Sprintf("workspace:%s", workspaceId)
	workspaceJson, err := db.Client.Get(ctx, key).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return models.Workspace{}, ErrNotFound
		}
		return models.Workspace{}, fmt.Errorf("failed to get workspace from Redis: %w", err)
	}
	var workspace models.Workspace
	err = json.Unmarshal([]byte(workspaceJson), &workspace)
	if err != nil {
		return models.Workspace{}, fmt.Errorf("failed to unmarshal workspace JSON: %w", err)
	}
	return workspace, nil
}

func (db RedisDatabase) GetAllWorkspaces(ctx context.Context) ([]models.Workspace, error) {
	var workspaces []models.Workspace

	// Retrieve all workspace IDs from the Redis sorted set
	workspaceNameIds, err := db.Client.ZRange(ctx, "global:workspaces", 0, -1).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to get workspace IDs from Redis sorted set: %w", err)
	}

	for _, nameId := range workspaceNameIds {
		id := nameId[strings.LastIndex(nameId, ":")+1:]
		// Fetch workspace details using the ID
		workspaceJson, err := db.Client.Get(ctx, fmt.Sprintf("workspace:%s", id)).Result()
		if err != nil {
			return nil, fmt.Errorf("failed to get workspace data for ID %s: %w", id, err)
		}
		var workspace models.Workspace
		err = json.Unmarshal([]byte(workspaceJson), &workspace)
		if err != nil {
			return nil, fmt.Errorf("failed to unmarshal workspace JSON: %w", err)
		}
		workspaces = append(workspaces, workspace)
	}

	return workspaces, nil
}

// AddTaskChange persists a task to the changes stream.
func (db RedisDatabase) AddTaskChange(ctx context.Context, task models.Task) error {
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

	err = db.Client.XAdd(ctx, &redis.XAddArgs{
		Stream: streamKey,
		Values: taskMap,
	}).Err()
	if err != nil {
		return fmt.Errorf("AddTaskChange - failed to append task to changes stream: %w", err)
	}

	return nil
}

func (db RedisDatabase) GetTaskChanges(ctx context.Context, workspaceId, streamMessageStartId string, maxCount int64, blockDuration time.Duration) ([]models.Task, string, error) {
	streamKey := fmt.Sprintf("%s:task_changes", workspaceId)
	if streamMessageStartId == "" {
		streamMessageStartId = "$"
	}
	if maxCount == 0 {
		maxCount = 100
	}
	streams, err := db.Client.XRead(ctx, &redis.XReadArgs{
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

	var tasks []models.Task
	for _, message := range streams[0].Messages {
		var task models.Task
		utils.Transcode(message.Values, &task)
		tasks = append(tasks, task)
	}

	// Return the last message id value to continue from
	lastMessageId := streams[0].Messages[len(streams[0].Messages)-1].ID

	return tasks, lastMessageId, nil
}
