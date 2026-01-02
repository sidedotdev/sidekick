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

func (s Storage) PersistTask(ctx context.Context, task domain.Task) error {
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

	// Handle archived tasks
	archivedKey := fmt.Sprintf("%s:archived_tasks", task.WorkspaceId)
	if task.Archived != nil {
		// Remove from all kanban sets and add to archived sorted set
		for _, status := range []domain.TaskStatus{domain.TaskStatusDrafting, domain.TaskStatusToDo, domain.TaskStatusInProgress, domain.TaskStatusInReview, domain.TaskStatusComplete, domain.TaskStatusBlocked, domain.TaskStatusFailed, domain.TaskStatusCanceled} {
			statusKey := fmt.Sprintf("%s:kanban:%s", task.WorkspaceId, status)
			err = s.Client.SRem(ctx, statusKey, task.Id).Err()
			if err != nil {
				log.Println("Failed to remove task id from kanban set: ", err)
				return err
			}
		}
		score := float64(task.Archived.Unix())
		err = s.Client.ZAdd(ctx, archivedKey, redis.Z{Score: score, Member: task.Id}).Err()
		if err != nil {
			log.Println("Failed to add task id to archived sorted set: ", err)
			return err
		}
	} else {
		// Remove from archived sorted set if it exists there
		err = s.Client.ZRem(ctx, archivedKey, task.Id).Err()
		if err != nil {
			log.Println("Failed to remove task id from archived sorted set: ", err)
			return err
		}

		// Add the task id to the appropriate kanban set based on the task status, and remove from others
		for _, status := range []domain.TaskStatus{domain.TaskStatusDrafting, domain.TaskStatusToDo, domain.TaskStatusInProgress, domain.TaskStatusInReview, domain.TaskStatusComplete, domain.TaskStatusBlocked, domain.TaskStatusFailed, domain.TaskStatusCanceled} {
			statusKey := fmt.Sprintf("%s:kanban:%s", task.WorkspaceId, status)
			if status == task.Status {
				err = s.Client.SAdd(ctx, statusKey, task.Id).Err()
				if err != nil {
					log.Println("Failed to add task id to kanban set: ", err)
					return err
				}
			} else {
				err = s.Client.SRem(ctx, statusKey, task.Id).Err()
				if err != nil {
					log.Println("Failed to remove task id from kanban set: ", err)
					return err
				}
			}
		}
	}

	if err != nil {
		log.Println("Failed to add task change: ", err)
		return err
	}

	return nil
}

// TODO /gen add tests for DeleteTask
func (s Storage) DeleteTask(ctx context.Context, workspaceId, taskId string) error {
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

	// Delete task from archived/kanban set
	if task.Archived != nil {
		archiveKey := fmt.Sprintf("%s:archived_tasks", workspaceId)
		err = s.Client.ZRem(ctx, archiveKey, taskId).Err()
		if err != nil {
			log.Println("Failed to delete task from archived sets in Redis: ", err)
			return err
		}
	} else {
		kanbanKey := fmt.Sprintf("%s:kanban:%s", workspaceId, task.Status)
		err = s.Client.SRem(ctx, kanbanKey, taskId).Err()
		if err != nil {
			log.Println("Failed to delete task from kanban sets in Redis: ", err)
			return err
		}
	}

	return nil
}

func (s Storage) GetTasks(ctx context.Context, workspaceId string, statuses []domain.TaskStatus) ([]domain.Task, error) {
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

func (s Storage) GetTask(ctx context.Context, workspaceId string, taskId string) (domain.Task, error) {
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

func (s Storage) GetArchivedTasks(ctx context.Context, workspaceId string, page, pageSize int64) ([]domain.Task, int64, error) {
	key := fmt.Sprintf("%s:archived_tasks", workspaceId)

	// Get the total count of archived tasks
	totalCount, err := s.Client.ZCard(ctx, key).Result()
	if err != nil {
		return nil, 0, fmt.Errorf("failed to get total count of archived tasks: %w", err)
	}

	// Calculate offset
	offset := (page - 1) * pageSize

	// Get the tasks from the sorted set
	taskIds, err := s.Client.ZRevRange(ctx, key, offset, offset+pageSize-1).Result()
	if err != nil {
		return nil, 0, fmt.Errorf("failed to get archived tasks: %w", err)
	}

	var tasks []domain.Task
	for _, taskId := range taskIds {
		task, err := s.GetTask(ctx, workspaceId, taskId)
		if err != nil {
			return nil, 0, fmt.Errorf("failed to get task %s: %w", taskId, err)
		}
		tasks = append(tasks, task)
	}

	return tasks, totalCount, nil
}

// AddTaskChange persists a task to the changes stream.
func (s Streamer) AddTaskChange(ctx context.Context, task domain.Task) error {
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

func (s *Streamer) StreamTaskChanges(ctx context.Context, workspaceId, streamMessageStartId string) (<-chan domain.Task, <-chan error) {
	taskChan := make(chan domain.Task)
	errChan := make(chan error, 1)

	go func() {
		defer close(taskChan)
		defer close(errChan)

		continueMessageId := streamMessageStartId
		for {
			select {
			case <-ctx.Done():
				return
			default:
				// we tried a blockDuration of 0, but it turns out to be uncancellable
				blockDuration := 250 * time.Millisecond
				tasks, latestContinueMessageId, err := s.getTaskChanges(ctx, workspaceId, continueMessageId, 100, blockDuration)
				if err != nil {
					errChan <- err
					return
				}

				for _, task := range tasks {
					select {
					case <-ctx.Done():
						return
					case taskChan <- task:
					}
				}

				continueMessageId = latestContinueMessageId
			}
		}
	}()

	return taskChan, errChan
}

func (s Streamer) getTaskChanges(ctx context.Context, workspaceId, streamMessageStartId string, maxCount int64, blockDuration time.Duration) ([]domain.Task, string, error) {
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
		if errors.Is(err, redis.Nil) {
			return nil, streamMessageStartId, nil
		}
		return nil, "", err
	}
	if len(streams) == 0 {
		return nil, "", fmt.Errorf("no streams returned for stream key %s", streamKey)
	}

	var tasks []domain.Task
	for _, message := range streams[0].Messages {
		var task domain.Task
		utils.Transcode(message.Values, &task)
		task.Created = task.Created.UTC()
		task.Updated = task.Updated.UTC()
		if task.Archived != nil {
			utc := task.Archived.UTC()
			task.Archived = &utc
		}
		tasks = append(tasks, task)
	}

	// Return the last message id value to continue from
	continueMessageId := streams[0].Messages[len(streams[0].Messages)-1].ID

	return tasks, continueMessageId, nil
}
