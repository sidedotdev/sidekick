package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"

	"sidekick/domain"

	"github.com/redis/go-redis/v9"
)

// Connect to Redis
func connectRedis() *redis.Client {
	opts := &redis.Options{
		Addr:     "localhost:6379",
		Password: "", // No password set
		DB:       0,  // Use default DB
	}
	return redis.NewClient(opts)
}

// Migrate existing workspace data to sorted set
func migrateWorkspaces() error {
	ctx := context.Background()
	client := connectRedis()

	workspaceKeys, err := client.Keys(ctx, "workspace:*").Result()
	if err != nil {
		return fmt.Errorf("error fetching workspace keys: %v", err)
	}

	for _, key := range workspaceKeys {
		workspaceJson, err := client.Get(ctx, key).Result()
		if err != nil {
			log.Printf("error getting workspace data for key %s: %v", key, err)
			continue
		}

		var workspace domain.Workspace
		err = json.Unmarshal([]byte(workspaceJson), &workspace)
		if err != nil {
			log.Printf("error unmarshalling workspace JSON for key %s: %v", key, err)
			continue
		}

		// Add to sorted set with workspace name length as score
		err = client.ZAdd(ctx, "global:workspaces", redis.Z{
			Score:  0,                                   // score is not used, we rely on the lexographical ordering of the member
			Member: workspace.Name + ":" + workspace.Id, // use name as member prefix to sort by name
		}).Err()
		if err != nil {
			log.Printf("error adding workspace to sorted set for ID %s: %v", workspace.Id, err)
		} else {
			log.Printf("added workspace %s to sorted set", workspace.Name)
		}
	}

	return nil
}

func main() {
	err := migrateWorkspaces()
	if err != nil {
		log.Fatalf("Migration failed: %v", err)
		os.Exit(1)
	}
	log.Println("Migration completed successfully")
}
