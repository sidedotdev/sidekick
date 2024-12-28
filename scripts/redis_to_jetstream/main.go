package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"sidekick/domain"
	"sidekick/nats"
	"sidekick/srv"
	jetstreamClient "sidekick/srv/jetstream"
	"sidekick/srv/redis"
	"sidekick/srv/sqlite"
)

func main() {
	ctx := context.Background()
	storage, err := sqlite.NewStorage()
	if err != nil {
		log.Fatalf("Failed to create SQLite storage: %v", err)
	}
	redisStreamer := redis.NewStreamer()

	// Initialize JetStream client
	nc, err := nats.GetConnection()
	if err != nil {
		log.Fatalf("Failed to connect to NATS: %v", err)
	}
	defer nc.Close()

	js, err := jetstreamClient.NewStreamer(nc)
	if err != nil {
		log.Fatalf("Failed to create JetStream client: %v", err)
	}

	// Get all workspace IDs
	workspaceIds, err := getAllWorkspaceIds(ctx, storage)
	if err != nil {
		log.Fatalf("Failed to get workspace IDs: %v", err)
	}

	// Migrate task changes (including flow action changes) and flow events for each workspace
	for _, workspaceId := range workspaceIds {
		tasks, err := storage.GetTasks(ctx, workspaceId, domain.AllTaskStatuses)
		if err != nil {
			log.Fatalf("Failed to get tasks for workspace %s: %v", workspaceId, err)
		}

		for _, task := range tasks {
			flows, err := storage.GetFlowsForTask(ctx, workspaceId, task.Id)
			if err != nil {
				log.Fatalf("Failed to get flows for task %s in workspace %s: %v", task.Id, workspaceId, err)
			}
			for _, flow := range flows {
				err = migrateFlowActionChanges(ctx, redisStreamer, js, workspaceId, flow.Id)
				if err != nil {
					log.Fatalf("Failed to migrate flow action changes for workspace %s, flow Id %s: %v", workspaceId, flow.Id, err)
				}
			}
		}

		/* We don't actually need task changes or flow events, just flow actions */
		/*
		err := migrateTaskChanges(ctx, redisStreamer, js, workspaceId)
		if err != nil {
			log.Printf("Failed to migrate task changes for workspace %s: %v", workspaceId, err)
			continue
		}

		err = migrateFlowEvents(ctx, redisStreamer, js, workspaceId)
		if err != nil {
			log.Printf("Failed to migrate flow events for workspace %s: %v", workspaceId, err)
			continue
		}
			*/
	}

	log.Println("Migration completed")
}

func getAllWorkspaceIds(ctx context.Context, storage srv.Storage) ([]string, error) {
	workspaces, err := storage.GetAllWorkspaces(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get all workspaces: %w", err)
	}

	var workspaceIds []string
	for _, workspace := range workspaces {
		workspaceIds = append(workspaceIds, workspace.Id)
	}

	return workspaceIds, nil
}

func migrateFlowActionChanges(ctx context.Context, redisStreamer *redis.Streamer, js *jetstreamClient.Streamer, workspaceId, flowId string) error {
	continueMessageId := "0"
	var totalMigrated int
	for {
		flowActions, nextMessageId, err := redisStreamer.GetFlowActionChanges(ctx, workspaceId, flowId, continueMessageId, 100, 100 * time.Millisecond)
		if err != nil {
			return fmt.Errorf("failed to get flow action changes from Redis for workspace %s, flow Id %s: %w", workspaceId, flowId, err)
		}

		if len(flowActions) == 0 {
			break
		}

		for _, flowAction := range flowActions {
			err = js.AddFlowActionChange(ctx, flowAction)
			if err != nil {
				return fmt.Errorf("failed to add flow action change to JetStream for workspace %s, flow type %s, flow action ID %s: %w", workspaceId, flowId, flowAction.Id, err)
			}
			totalMigrated++
		}

		log.Printf("Migrated %d flow action changes for workspace %s, flow type %s (Total: %d)", len(flowActions), workspaceId, flowId, totalMigrated)
		continueMessageId = nextMessageId
	}

	log.Printf("Completed migrating %d flow action changes for workspace: %s, flow type: %s", totalMigrated, workspaceId, flowId)
	return nil
}

/*
func getLastMigratedMessageId(workspaceId, changeType, flowType string) (string, error) {
	// TODO: Implement this function to retrieve the last migrated message ID from a persistent store
	// Use flowType as part of the key for flow action changes
	return "", nil
}

func saveLastMigratedMessageId(workspaceId, changeType, flowId, messageId string) error {
	// TODO: Implement this function to save the last migrated message ID to a persistent store
	// Use flowId as part of the key for flow action changes
	return nil
}

func migrateFlowEvents(ctx context.Context, redisStreamer *redis.Streamer, js *jetstreamClient.Streamer, workspaceId string) error {
	log.Printf("Migrating flow events for workspace: %s", workspaceId)

	lastMessageId, err := getLastMigratedMessageId(workspaceId, "flowEvent", "")
	if err != nil {
		return fmt.Errorf("failed to get last migrated message ID: %w", err)
	}

	streamKeys := map[string]string{
		fmt.Sprintf("%s:flow_events:stream", workspaceId): lastMessageId,
	}

	var totalMigrated int
	for {
		events, newStreamKeys, err := redisStreamer.GetFlowEvents(ctx, workspaceId, streamKeys, 100, 0)
		if err != nil {
			return fmt.Errorf("failed to get flow events from Redis for workspace %s: %w", workspaceId, err)
		}

		if len(events) == 0 {
			break
		}

		for _, event := range events {
			err = js.AddFlowEvent(ctx, workspaceId, event.GetParentId(), event)
			if err != nil {
				return fmt.Errorf("failed to add flow event to JetStream for workspace %s, parent ID %s: %w", workspaceId, event.GetParentId(), err)
			}
			totalMigrated++
		}

		streamKeys = newStreamKeys
		lastMessageId = newStreamKeys[fmt.Sprintf("%s:flow_events:stream", workspaceId)]
		err = saveLastMigratedMessageId(workspaceId, "flowEvent", "", lastMessageId)
		if err != nil {
			return fmt.Errorf("failed to save last migrated message ID for workspace %s: %w", workspaceId, err)
		}

		log.Printf("Migrated %d flow events for workspace %s (Total: %d)", len(events), workspaceId, totalMigrated)

		// Add a small delay to avoid overwhelming the system
		time.Sleep(10 * time.Millisecond)
	}

	log.Printf("Completed migrating %d flow events for workspace: %s", totalMigrated, workspaceId)
	return nil
}

func migrateTaskChanges(ctx context.Context, redisStreamer *redis.Streamer, js *jetstreamClient.Streamer, workspaceId string) error {
	log.Printf("Migrating task changes for workspace: %s", workspaceId)

	lastMessageId, err := getLastMigratedMessageId(workspaceId, "task", "")
	if err != nil {
		return fmt.Errorf("failed to get last migrated message ID: %w", err)
	}

	var totalMigrated int
	for {
		tasks, nextMessageId, err := redisStreamer.GetTaskChanges(ctx, workspaceId, lastMessageId, 100, 0)
		if err != nil {
			return fmt.Errorf("failed to get task changes from Redis for workspace %s: %w", workspaceId, err)
		}

		if len(tasks) == 0 {
			break
		}

		for _, task := range tasks {
			err = js.AddTaskChange(ctx, task)
			if err != nil {
				return fmt.Errorf("failed to add task change to JetStream for workspace %s, task ID %s: %w", workspaceId, task.Id, err)
			}
			totalMigrated++

			// Migrate flow action changes for this task's flow
			if task.FlowType != "" {
				err = migrateFlowActionChanges(ctx, redisStreamer, js, workspaceId, string(task.FlowType))
				if err != nil {
					log.Printf("Failed to migrate flow action changes for workspace %s, flow type %s: %v", workspaceId, task.FlowType, err)
					// Continue with other tasks even if one flow's action changes fail
				}
			}
		}

		log.Printf("Migrated %d task changes for workspace %s (Total: %d)", len(tasks), workspaceId, totalMigrated)
		lastMessageId = nextMessageId

		// Add a small delay to avoid overwhelming the system
		time.Sleep(10 * time.Millisecond)
	}

	log.Printf("Completed migrating %d task changes for workspace: %s", totalMigrated, workspaceId)
	return nil
}


*/