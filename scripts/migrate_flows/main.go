package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"sidekick/domain"
	"sidekick/srv"
	"sidekick/srv/redis"
)

func migrateFlows(ctx context.Context, redisDB *redis.Storage, dryRun bool) error {
	workspaces, err := redisDB.GetAllWorkspaces(ctx)
	if err != nil {
		return fmt.Errorf("failed to get workspaces: %w", err)
	}

	var (
		totalWorkspaces   = len(workspaces)
		totalTasks        = 0
		totalFlows        = 0
		totalUpdatedFlows = 0
	)

	for _, workspace := range workspaces {
		log.Printf("Processing workspace: %s", workspace.Id)

		tasks, err := redisDB.GetTasks(ctx, workspace.Id, domain.AllTaskStatuses)
		if err != nil {
			log.Printf("Error getting tasks for workspace %s: %v", workspace.Id, err)
			continue
		}

		totalTasks += len(tasks)

		for _, task := range tasks {
			flowsKey := fmt.Sprintf("%s:%s:flows", workspace.Id, task.Id)
			flowIds, err := redisDB.Client.SMembers(ctx, flowsKey).Result()
			if err != nil {
				log.Printf("Error getting child flows for task %s: %v", task.Id, err)
				continue
			}

			totalFlows += len(flowIds)

			for _, flowId := range flowIds {
				// Verify if the flow is accessible using the current key format
				_, err := redisDB.GetFlow(ctx, workspace.Id, flowId)
				if err != nil {
					if err == srv.ErrNotFound {
						// If not found, update the key
						if !dryRun {
							err = updateFlowKey(ctx, redisDB, workspace.Id, flowId)
							if err != nil {
								log.Printf("Error updating flow key for flow %s: %v", flowId, err)
							} else {
								totalUpdatedFlows++
								//log.Printf("Updated key for flow %s in workspace %s", flowId, workspace.Id)
							}
						} else {
							log.Printf("Dry run: Would update key for flow %s in workspace %s", flowId, workspace.Id)
							totalUpdatedFlows++
						}
					} else {
						log.Printf("Error verifying flow %s: %v", flowId, err)
					}
				}
			}
		}
	}

	log.Printf("Migration summary:")
	log.Printf("Total workspaces processed: %d", totalWorkspaces)
	log.Printf("Total tasks checked: %d", totalTasks)
	log.Printf("Total flows verified: %d", totalFlows)
	if dryRun {
		log.Printf("Total flows that would be updated: %d", totalUpdatedFlows)
	} else {
		log.Printf("Total flows updated: %d", totalUpdatedFlows)
	}

	return nil
}

func updateFlowKey(ctx context.Context, redisDB *redis.Storage, workspaceId string, flowId string) error {
	// Get the flow data using the old key format
	oldKey := flowId // no workspaceId prefix
	flowJson, err := redisDB.Client.Get(ctx, oldKey).Result()
	if err != nil {
		return fmt.Errorf("failed to get flow data: %w", err)
	}

	// Set the flow data using the new key format
	newKey := fmt.Sprintf("%s:%s", workspaceId, flowId)
	err = redisDB.Client.Set(ctx, newKey, flowJson, 0).Err()
	if err != nil {
		return fmt.Errorf("failed to set flow data with new key: %w", err)
	}

	// Delete the old key if it's different from the new key
	if oldKey != newKey {
		err = redisDB.Client.Del(ctx, oldKey).Err()
		if err != nil {
			return fmt.Errorf("failed to delete old flow key: %w", err)
		}
	}

	return nil
}

func main() {
	ctx := context.Background()

	redisAddress := os.Getenv("REDIS_ADDRESS")
	if redisAddress == "" {
		redisAddress = "localhost:6379"
	}

	dryRun := os.Getenv("DRY_RUN") == "true"

	redisClient := redis.NewClient(&redis.Options{
		Addr: redisAddress,
	})
	defer redisClient.Close()

	redisDB := &redis.Storage{Client: redisClient}

	if dryRun {
		log.Println("Running in dry-run mode. No changes will be made.")
	}

	err := migrateFlows(ctx, redisDB, dryRun)
	if err != nil {
		log.Fatalf("Migration failed: %v", err)
	}

	if dryRun {
		log.Println("Dry run completed successfully")
	} else {
		log.Println("Migration completed successfully")
	}
}
