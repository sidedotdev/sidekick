package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"sidekick/db"
	"sidekick/models"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/segmentio/ksuid"
)

type SubflowTree struct {
	Name        string        `json:"name"`
	Children    []interface{} `json:"children"`
	Description string        `json:"description,omitempty"`
}

func buildSubflowTrees(flowActions []models.FlowAction) []*SubflowTree {
	subflowTrees := []*SubflowTree{}
	ancestors := []*SubflowTree{}
	subflowDescriptions := make(map[string]string)

	for _, action := range flowActions {
		subflows := strings.Split(action.SubflowName, ":|:")

		if action.SubflowDescription != "" {
			lastSubflow := subflows[len(subflows)-1]
			subflowDescriptions[lastSubflow] = action.SubflowDescription
		}

		var parent *SubflowTree
		i := 0

		// Find the longest common prefix with the ancestors
		for i < len(ancestors) && i < len(subflows) && ancestors[i].Name == subflows[i] {
			parent = ancestors[i]
			i++
		}

		// Remove the extra ancestors
		ancestors = ancestors[:i]

		// Add the new nodes
		for ; i < len(subflows); i++ {
			newSubflowTree := &SubflowTree{
				Name:     subflows[i],
				Children: []interface{}{},
			}
			if description, ok := subflowDescriptions[newSubflowTree.Name]; ok && description != "" {
				newSubflowTree.Description = description
			}

			if parent != nil {
				parent.Children = append(parent.Children, newSubflowTree)
			} else {
				subflowTrees = append(subflowTrees, newSubflowTree)
			}
			ancestors = append(ancestors, newSubflowTree)
			parent = newSubflowTree
		}

		// Add the action to the last node
		parent.Children = append(parent.Children, action)
	}

	return subflowTrees
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

	redisDB := &db.RedisDatabase{Client: redisClient}

	if dryRun {
		log.Println("Running in dry-run mode. No changes will be made.")
	}

	summary, err := migrateSubflows(ctx, redisDB, dryRun)
	if err != nil {
		log.Fatalf("Migration failed: %v", err)
	}

	if dryRun {
		log.Println("Dry run completed successfully")
	} else {
		log.Println("Migration completed successfully")
	}

	log.Printf("Migration summary:\n%s", summary)
}

func createSubflowsFromTree(ctx context.Context, tree SubflowTree, workspaceId, flowId string, parentSubflowId string, database *db.RedisDatabase, dryRun bool) (int, error) {
	numCreated := 0
	subflow := models.Subflow{
		WorkspaceId:     workspaceId,
		Id:              fmt.Sprintf("sf_%s", ksuid.New().String()),
		Name:            tree.Name,
		Description:     tree.Description,
		Status:          models.SubflowStatusComplete,
		FlowId:          flowId,
		ParentSubflowId: parentSubflowId,
	}

	if !dryRun {
		err := database.PersistSubflow(ctx, subflow)
		if err != nil {
			return numCreated, fmt.Errorf("failed to persist subflow: %w", err)
		}
		numCreated++
	}

	for _, child := range tree.Children {
		switch v := child.(type) {
		case *SubflowTree:
			moreCreated, err := createSubflowsFromTree(ctx, *v, workspaceId, flowId, subflow.Id, database, dryRun)
			numCreated += moreCreated
			if err != nil {
				return numCreated, err
			}
		case models.FlowAction:
			if !dryRun {
				// NOTE we're not using PersistFlowAction here because that
				// calls AddFlowActionChange, which we don't want to do here
				v.SubflowId = subflow.Id
				flowActionJson, err := json.Marshal(v)
				if err != nil {
					return numCreated, fmt.Errorf("failed to convert flow action record to JSON: %w", err)
				}
				key := fmt.Sprintf("%s:%s", v.WorkspaceId, v.Id)
				err = database.Client.Set(ctx, key, flowActionJson, 0).Err()
				if err != nil {
					return numCreated, fmt.Errorf("failed to persist flow action to Redis: %w", err)
				}
			}
		default:
			return numCreated, fmt.Errorf("unexpected type in subflow tree: %T", v)
		}
	}

	return numCreated, nil
}

func migrateSubflows(ctx context.Context, database *db.RedisDatabase, dryRun bool) (string, error) {
	workspaces, err := database.GetAllWorkspaces(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get all workspaces: %w", err)
	}

	var totalWorkspaces, totalTasks, totalFlows, totalFlowActions, totalSubflowsCreated int

	for _, workspace := range workspaces {
		// TODO: remove this check
		if workspace.Id != "ws_2h7TkUIDypLPvB5QXLIF0cwlqi8" {
			continue
		}

		log.Printf("Processing workspace: %s", workspace.Id)
		totalWorkspaces++

		tasks, err := database.GetTasks(ctx, workspace.Id, models.AllTaskStatuses)
		if err != nil {
			return "", fmt.Errorf("Failed to get tasks for workspace %s: %v", workspace.Id, err)
		}

		for _, task := range tasks {
			//log.Printf("Processing task: %s", task.Id)

			totalTasks++

			flows, err := database.GetFlowsForTask(ctx, workspace.Id, task.Id)
			if err != nil {
				return "", fmt.Errorf("Failed to get flows for task %s: %v", task.Id, err)
			}

			for _, flow := range flows {
				log.Printf("Processing flow: %s", flow.Id)
				totalFlows++

				flowActions, err := database.GetFlowActions(ctx, workspace.Id, flow.Id)
				if err != nil {
					return "", fmt.Errorf("Failed to get flow actions for flow %s: %v", flow.Id, err)
				}

				subflowTrees := buildSubflowTrees(flowActions)
				for _, tree := range subflowTrees {
					// NOTE: createSubflowsFromTree also updates the flow actions to point to the new subflow ids from the subflow tree
					numCreated, err := createSubflowsFromTree(ctx, *tree, workspace.Id, flow.Id, "", database, dryRun)
					totalSubflowsCreated += numCreated
					if err != nil {
						return "", fmt.Errorf("Failed to create subflows for flow %s: %v", flow.Id, err)
					}
				}
				totalFlowActions += len(flowActions)

				// we rely on the existing subflow id being persisted in the the
				// create subflows form tree step
				updatedFlowActions, err := database.GetFlowActions(ctx, workspace.Id, flow.Id)
				if err != nil {
					return "", fmt.Errorf("Failed to get flow actions for flow %s: %v", flow.Id, err)
				}
				subflowIdByFlowActionId := make(map[string]string)
				for _, updatedFlowAction := range updatedFlowActions {
					if updatedFlowAction.SubflowId == "" {
						return "", fmt.Errorf("Subflow id not set for flow action %s", updatedFlowAction.Id)
					}
					subflowIdByFlowActionId[updatedFlowAction.Id] = updatedFlowAction.SubflowId
				}

				// duplicate flow action changes stream
				flowActionChanges, _, err := database.GetFlowActionChanges(ctx, workspace.Id, flow.Id, "0", 100000, 1*time.Second)
				if err != nil {
					return "", fmt.Errorf("Failed to get flow action changes for flow %s: %v", flow.Id, err)
				}
				for _, flowActionChange := range flowActionChanges {
					//fmt.Printf("Processing flow action change: %s\n", flowActionChange.Id)
					if !dryRun {
						if subflowId, ok := subflowIdByFlowActionId[flowActionChange.Id]; ok {
							flowActionChange.SubflowId = subflowId
						} else {
							return "", fmt.Errorf("Subflow id not found for flow action id %s", flowActionChange.Id)
						}

						err = addFlowActionChangeV2(ctx, flowActionChange, database)
						if err != nil {
							return "", err
						}

						// NOTE: we were originally going to delete old flow
						// action change via XDEL, but we'll overwrite the
						// entire old stream instead via RENAME, which is better
						/*
							oldStreamKey := fmt.Sprintf("%s:%s:flow_action_changes", workspace.Id, flow.Id)
							err = database.Client.XDel(ctx, oldStreamKey, flowAction.Id).Err()
							if err != nil {
								return "", fmt.Errorf("Failed to delete old flow action change: %v", err)
							}
						*/
					}
				}

				// overwrite v1 stream with v2 stream after fully replicating,
				// so we can remove the v2 keys
				if !dryRun && len(flowActionChanges) > 0 {
					v1StreamKey := fmt.Sprintf("%s:%s:flow_action_changes", workspace.Id, flow.Id)
					v2StreamKey := fmt.Sprintf("%s:%s:flow_action_changes_v2", workspace.Id, flow.Id)
					err = database.Client.Rename(ctx, v2StreamKey, v1StreamKey).Err()
					if err != nil {
						return "", fmt.Errorf("Failed to rename stream %s to %s: %v", v2StreamKey, v1StreamKey, err)
					}
				}
			}
		}
	}

	summary := fmt.Sprintf("Workspaces processed: %d\nTasks processed: %d\nFlows processed: %d\nFlow actions processed: %d\nSubflows created: %d",
		totalWorkspaces, totalTasks, totalFlows, totalFlowActions, totalSubflowsCreated)

	return summary, nil
}

func addFlowActionChangeV2(ctx context.Context, flowAction models.FlowAction, database *db.RedisDatabase) error {
	// v2 streamkey created temporarily, until we rename
	streamKey := fmt.Sprintf("%s:%s:flow_action_changes_v2", flowAction.WorkspaceId, flowAction.FlowId)
	actionParams, err := json.Marshal(flowAction.ActionParams)
	if err != nil {
		return fmt.Errorf("Failed to marshal action params: %w", err)

	}
	flowActionMap, err := toMap(flowAction)
	if err != nil {
		return fmt.Errorf("Failed to append flow action to changes stream: %w", err)
	}

	flowActionMap["actionParams"] = string(actionParams)
	err = database.Client.XAdd(ctx, &redis.XAddArgs{
		Stream: streamKey,
		Values: flowActionMap,
	}).Err()
	if err != nil {
		return fmt.Errorf("Failed to append flow action to changes stream: %w", err)
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
