package main

import (
	"context"
	"fmt"
	"log"
	"time"

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

	// Migrate task changes and flow action changes for each workspace
	for _, workspaceId := range workspaceIds {
		err := migrateTaskChanges(ctx, redisStreamer, js, workspaceId)
		if err != nil {
			log.Printf("Failed to migrate task changes for workspace %s: %v", workspaceId, err)
			continue
		}

		err = migrateFlowActionChanges(ctx, redisStreamer, js, workspaceId)
		if err != nil {
			log.Printf("Failed to migrate flow action changes for workspace %s: %v", workspaceId, err)
			continue
		}
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

func migrateTaskChanges(ctx context.Context, redisStreamer *redis.Streamer, js *jetstreamClient.Streamer, workspaceId string) error {
	log.Printf("Migrating task changes for workspace: %s", workspaceId)

	lastMessageId, err := getLastMigratedMessageId(workspaceId, "task")
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
		}

		err = saveLastMigratedMessageId(workspaceId, "task", nextMessageId)
		if err != nil {
			return fmt.Errorf("failed to save last migrated message ID for workspace %s: %w", workspaceId, err)
		}

		log.Printf("Migrated %d task changes for workspace %s (Total: %d)", len(tasks), workspaceId, totalMigrated)
		lastMessageId = nextMessageId

		// Add a small delay to avoid overwhelming the system
		time.Sleep(10 * time.Millisecond)
	}

	log.Printf("Completed migrating %d task changes for workspace: %s", totalMigrated, workspaceId)
	return nil
}

func migrateFlowActionChanges(ctx context.Context, redisStreamer *redis.Streamer, js *jetstreamClient.Streamer, workspaceId string) error {
	log.Printf("Migrating flow action changes for workspace: %s", workspaceId)

	lastMessageId, err := getLastMigratedMessageId(workspaceId, "flowAction")
	if err != nil {
		return fmt.Errorf("failed to get last migrated message ID: %w", err)
	}

	var totalMigrated int
	for {
		flowActions, nextMessageId, err := redisStreamer.GetFlowActionChanges(ctx, workspaceId, "", lastMessageId, 100, 0)
		if err != nil {
			return fmt.Errorf("failed to get flow action changes from Redis for workspace %s: %w", workspaceId, err)
		}

		if len(flowActions) == 0 {
			break
		}

		for _, flowAction := range flowActions {
			err = js.AddFlowActionChange(ctx, flowAction)
			if err != nil {
				return fmt.Errorf("failed to add flow action change to JetStream for workspace %s, flow action ID %s: %w", workspaceId, flowAction.Id, err)
			}
			totalMigrated++
		}

		err = saveLastMigratedMessageId(workspaceId, "flowAction", nextMessageId)
		if err != nil {
			return fmt.Errorf("failed to save last migrated message ID for workspace %s: %w", workspaceId, err)
		}

		log.Printf("Migrated %d flow action changes for workspace %s (Total: %d)", len(flowActions), workspaceId, totalMigrated)
		lastMessageId = nextMessageId

		// Add a small delay to avoid overwhelming the system
		time.Sleep(10 * time.Millisecond)
	}

	log.Printf("Completed migrating %d flow action changes for workspace: %s", totalMigrated, workspaceId)
	return nil
}

func getLastMigratedMessageId(workspaceId, changeType string) (string, error) {
	// TODO: Implement this function to retrieve the last migrated message ID from a persistent store
	return "", nil
}

func saveLastMigratedMessageId(workspaceId, changeType, messageId string) error {
	// TODO: Implement this function to save the last migrated message ID to a persistent store
	return nil
}
