package main

import (
	"context"
	"fmt"
	"log"

	"sidekick/domain"
	"sidekick/srv/redis"
	"sidekick/srv/sqlite"
)

func main() {
	ctx := context.Background()

	// Initialize Redis client
	redisStorage := redis.NewStorage()

	// Initialize SQLite client
	sqliteStorage, err := sqlite.NewStorage()
	if err != nil {
		log.Fatalf("Failed to initialize SQLite storage: %v", err)
	}
	sqliteStorage.MigrateUp("sidekick")

	// Retrieve all workspace IDs from Redis
	workspaceIds, err := getAllWorkspaceIds(ctx, redisStorage)
	if err != nil {
		log.Fatalf("Failed to retrieve workspace IDs: %v", err)
	}

	// Initialize counters
	counters := make(map[string]int)

	// Iterate through workspace IDs and migrate data
	for _, workspaceId := range workspaceIds {
		fmt.Printf("Processing workspace: %s\n", workspaceId)

		err = migrateWorkspace(ctx, redisStorage, sqliteStorage, workspaceId, &counters)
		if err != nil {
			log.Fatalf("Failed to migrate workspace %s: %v", workspaceId, err)
		}
	}

	// Print migration results
	fmt.Println("\nMigration completed. Results:")
	for dataType, count := range counters {
		fmt.Printf("%s: %d\n", dataType, count)
	}
}

func getAllWorkspaceIds(ctx context.Context, redisClient *redis.Storage) ([]string, error) {
	workspaces, err := redisClient.GetAllWorkspaces(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get all workspaces: %w", err)
	}

	workspaceIds := make([]string, len(workspaces))
	for i, workspace := range workspaces {
		workspaceIds[i] = workspace.Id
	}

	return workspaceIds, nil
}

func migrateWorkspace(ctx context.Context, redisClient *redis.Storage, sqliteClient *sqlite.Storage, workspaceID string, counters *map[string]int) error {
	// Migrate workspace
	fmt.Printf("Migrating workspace: %s\n", workspaceID)
	workspace, err := redisClient.GetWorkspace(ctx, workspaceID)
	if err != nil {
		return fmt.Errorf("failed to get workspace %s from Redis: %w", workspaceID, err)
	}

	err = sqliteClient.PersistWorkspace(ctx, workspace)
	if err != nil {
		return fmt.Errorf("failed to persist workspace %s to SQLite: %w", workspaceID, err)
	}

	(*counters)["workspaces"]++

	// Migrate workspace config
	config, err := redisClient.GetWorkspaceConfig(ctx, workspaceID)
	if err != nil {
		return fmt.Errorf("failed to get workspace config for %s from Redis: %w", workspaceID, err)
	}

	err = sqliteClient.PersistWorkspaceConfig(ctx, workspaceID, config)
	if err != nil {
		return fmt.Errorf("failed to persist workspace config for %s to SQLite: %w", workspaceID, err)
	}

	(*counters)["workspace_configs"]++

	// Migrate Tasks, Flows, Subflows, and FlowActions
	err = migrateTasksAndFlows(ctx, redisClient, sqliteClient, workspaceID, counters)
	if err != nil {
		return fmt.Errorf("failed to migrate tasks and flows for workspace %s: %w", workspaceID, err)
	}

	return nil
}

func migrateTasksAndFlows(ctx context.Context, redisClient *redis.Storage, sqliteClient *sqlite.Storage, workspaceID string, counters *map[string]int) error {
	// Retrieve all tasks for the workspace from Redis
	tasks, err := redisClient.GetTasks(ctx, workspaceID, domain.AllTaskStatuses)
	if err != nil {
		return fmt.Errorf("failed to get tasks for workspace %s from Redis: %w", workspaceID, err)
	}

	for _, task := range tasks {
		err := migrateTask(ctx, redisClient, sqliteClient, workspaceID, task, counters)
		if err != nil {
			return err
		}
	}

	return nil
}

func migrateTask(ctx context.Context, redisClient *redis.Storage, sqliteClient *sqlite.Storage, workspaceID string, task domain.Task, counters *map[string]int) error {
	// Migrate task
	err := sqliteClient.PersistTask(ctx, task)
	if err != nil {
		return fmt.Errorf("failed to persist task %s to SQLite: %w", task.Id, err)
	}
	(*counters)["tasks"]++

	// Migrate associated flows
	err = migrateFlowsForTask(ctx, redisClient, sqliteClient, workspaceID, task.Id, counters)
	if err != nil {
		return err
	}

	return nil
}

func migrateFlowsForTask(ctx context.Context, redisClient *redis.Storage, sqliteClient *sqlite.Storage, workspaceID, taskID string, counters *map[string]int) error {
	flows, err := redisClient.GetFlowsForTask(ctx, workspaceID, taskID)
	if err != nil {
		return fmt.Errorf("failed to get flows for task %s from Redis: %w", taskID, err)
	}

	for _, flow := range flows {
		err = sqliteClient.PersistFlow(ctx, flow)
		if err != nil {
			return fmt.Errorf("failed to persist flow %s for task %s to SQLite: %w", flow.Id, taskID, err)
		}
		(*counters)["flows"]++
		fmt.Printf("Migrated flow %s for task: %s\n", flow.Id, taskID)

		err = migrateFlowActions(ctx, redisClient, sqliteClient, workspaceID, flow.Id, counters)
		if err != nil {
			return err
		}

		err = migrateSubflows(ctx, redisClient, sqliteClient, workspaceID, flow.Id, counters)
		if err != nil {
			return err
		}
	}

	return nil
}

func migrateFlowActions(ctx context.Context, redisClient *redis.Storage, sqliteClient *sqlite.Storage, workspaceID, flowID string, counters *map[string]int) error {
	actions, err := redisClient.GetFlowActions(ctx, workspaceID, flowID)
	if err != nil {
		return fmt.Errorf("failed to get flow actions for flow %s from Redis: %w", flowID, err)
	}

	for _, action := range actions {
		err = sqliteClient.PersistFlowAction(ctx, action)
		if err != nil {
			return fmt.Errorf("failed to persist flow action %s to SQLite: %w", action.Id, err)
		}
		(*counters)["flow_actions"]++
	}

	return nil
}

func migrateSubflows(ctx context.Context, redisClient *redis.Storage, sqliteClient *sqlite.Storage, workspaceID, flowID string, counters *map[string]int) error {
	subflows, err := redisClient.GetSubflows(ctx, workspaceID, flowID)
	if err != nil {
		return fmt.Errorf("failed to get subflows for flow %s from Redis: %w", flowID, err)
	}

	for _, subflow := range subflows {
		err = sqliteClient.PersistSubflow(ctx, subflow)
		if err != nil {
			return fmt.Errorf("failed to persist subflow %s to SQLite: %w", subflow.Id, err)
		}
		(*counters)["subflows"]++
	}

	return nil
}
