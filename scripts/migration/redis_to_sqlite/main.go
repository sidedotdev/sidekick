package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"path/filepath"

	"sidekick/common"
	redisStorage "sidekick/srv/redis"
	"sidekick/srv/sqlite"
)

func main() {
	ctx := context.Background()

	// Initialize Redis client
	redisClient := redisStorage.NewStorage()

	// Initialize SQLite client
	sidekickDataHome, err := common.GetSidekickDataHome()
	if err != nil {
		log.Fatalf("Failed to get Sidekick data home: %v", err)
	}
	sqliteDbPath := filepath.Join(sidekickDataHome, "sidekick.db")
	sqliteClient, err := initializeSQLiteStorage(sqliteDbPath)
	if err != nil {
		log.Fatalf("Failed to initialize SQLite storage: %v", err)
	}

	// Retrieve all workspace IDs from Redis
	workspaceIDs, err := getAllWorkspaceIDs(ctx, redisClient)
	if err != nil {
		log.Fatalf("Failed to retrieve workspace IDs: %v", err)
	}

	// Initialize counters
	counters := make(map[string]int)

	// Iterate through workspace IDs and migrate data
	for _, workspaceID := range workspaceIDs {
		fmt.Printf("Processing workspace: %s\n", workspaceID)

		err = migrateWorkspace(ctx, redisClient, sqliteClient, workspaceID, &counters)
		if err != nil {
			log.Fatalf("Failed to migrate workspace %s: %v", workspaceID, err)
		}
	}

	// Print migration results
	fmt.Println("\nMigration completed. Results:")
	for dataType, count := range counters {
		fmt.Printf("%s: %d\n", dataType, count)
	}
}

func initializeSQLiteStorage(dbPath string) (*sqlite.Storage, error) {
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open SQLite database: %w", err)
	}

	// Set journal mode to WAL
	_, err = db.Exec("PRAGMA journal_mode=WAL;")
	if err != nil {
		return nil, fmt.Errorf("failed to set WAL journal mode: %w", err)
	}

	kvDb, err := sql.Open("sqlite3", dbPath+".kv")
	if err != nil {
		return nil, fmt.Errorf("failed to open SQLite KV database: %w", err)
	}

	return sqlite.NewStorage(db, kvDb), nil
}

func getAllWorkspaceIDs(ctx context.Context, redisClient *redisStorage.Storage) ([]string, error) {
	workspaces, err := redisClient.GetAllWorkspaces(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get all workspaces: %w", err)
	}

	workspaceIDs := make([]string, len(workspaces))
	for i, workspace := range workspaces {
		workspaceIDs[i] = workspace.Id
	}

	return workspaceIDs, nil
}

func migrateWorkspace(ctx context.Context, redisClient *redisStorage.Storage, sqliteClient *sqlite.Storage, workspaceID string, counters *map[string]int) error {
	// Migrate workspace
	workspace, err := redisClient.GetWorkspace(ctx, workspaceID)
	if err != nil {
		return fmt.Errorf("failed to get workspace from Redis: %w", err)
	}

	err = sqliteClient.PersistWorkspace(ctx, workspace)
	if err != nil {
		return fmt.Errorf("failed to persist workspace to SQLite: %w", err)
	}

	(*counters)["workspaces"]++

	// Migrate workspace config
	config, err := redisClient.GetWorkspaceConfig(ctx, workspaceID)
	if err != nil {
		return fmt.Errorf("failed to get workspace config from Redis: %w", err)
	}

	err = sqliteClient.PersistWorkspaceConfig(ctx, workspaceID, config)
	if err != nil {
		return fmt.Errorf("failed to persist workspace config to SQLite: %w", err)
	}

	(*counters)["workspace_configs"]++

	// Migrate Tasks, Flows, Subflows, and FlowActions
	err = migrateTasksAndFlows(ctx, redisClient, sqliteClient, workspaceID, counters)
	if err != nil {
		return fmt.Errorf("failed to migrate tasks and flows: %w", err)
	}

	return nil
}

func migrateTasksAndFlows(ctx context.Context, redisClient *redisStorage.Storage, sqliteClient *sqlite.Storage, workspaceID string, counters *map[string]int) error {
	// TODO: Implement migration for Tasks, Flows, Subflows, and FlowActions
	// This function should:
	// 1. Retrieve all tasks for the workspace from Redis
	// 2. For each task, migrate its data and associated flows
	// 3. For each flow, migrate its actions and subflows
	// 4. Update the counters for each migrated item type
	return fmt.Errorf("migrateTasksAndFlows not implemented")
}
