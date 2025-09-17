package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"sidekick/client"
	"sidekick/domain"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// StartTaskParams defines the parameters for the start_task tool
type StartTaskParams struct {
	Description           string `json:"description"`
	FlowType              string `json:"flowType,omitempty"`
	DetermineRequirements *bool  `json:"determineRequirements,omitempty"`
}

// NewWorkspaceServer creates a new MCP server for a specific workspace
func NewWorkspaceServer(c client.Client, workspaceId string) *mcpsdk.Server {
	server := mcpsdk.NewServer(&mcpsdk.Implementation{Name: "sidekick"}, nil)

	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name:        "start_task",
		Description: "Start a new LLM-driven task in the workspace",
	}, func(ctx context.Context, req *mcpsdk.CallToolRequest, args StartTaskParams) (*mcpsdk.CallToolResult, any, error) {
		return handleStartTask(ctx, c, workspaceId, args)
	})

	return server
}

func handleStartTask(ctx context.Context, c client.Client, workspaceId string, params StartTaskParams) (*mcpsdk.CallToolResult, any, error) {
	// Validate description
	if params.Description == "" {
		return &mcpsdk.CallToolResult{
			IsError: true,
			Content: []mcpsdk.Content{
				&mcpsdk.TextContent{Text: "description parameter is required and cannot be empty"},
			},
		}, nil, nil
	}

	// Set default flowType if not provided
	flowType := params.FlowType
	if flowType == "" {
		flowType = "basic_dev"
	}

	// Validate flowType
	_, err := domain.StringToFlowType(flowType)
	if err != nil {
		return &mcpsdk.CallToolResult{
			IsError: true,
			Content: []mcpsdk.Content{
				&mcpsdk.TextContent{Text: fmt.Sprintf("invalid flowType: %s. Allowed values: basic_dev, planned_dev", flowType)},
			},
		}, nil, nil
	}

	// Set default determineRequirements if not provided
	determineRequirements := true
	if params.DetermineRequirements != nil {
		determineRequirements = *params.DetermineRequirements
	}

	// Create the task
	createReq := &client.CreateTaskRequest{
		Description: params.Description,
		FlowType:    flowType,
		FlowOptions: map[string]interface{}{
			"determineRequirements": determineRequirements,
		},
	}

	task, err := c.CreateTask(workspaceId, createReq)
	if err != nil {
		return &mcpsdk.CallToolResult{
			IsError: true,
			Content: []mcpsdk.Content{
				&mcpsdk.TextContent{Text: fmt.Sprintf("failed to create task: %v", err)},
			},
		}, nil, nil
	}

	// Wait for the task to start
	monitor := client.NewTaskMonitor(c, workspaceId, task.Id)
	statusChan, _ := monitor.Start(ctx)

	// Wait for task to reach started state (in_progress) or finished state
	for {
		select {
		case <-ctx.Done():
			monitor.Stop()
			return &mcpsdk.CallToolResult{
				IsError: true,
				Content: []mcpsdk.Content{
					&mcpsdk.TextContent{Text: "task creation was cancelled"},
				},
			}, nil, nil

		case status, ok := <-statusChan:
			if !ok {
				return &mcpsdk.CallToolResult{
					IsError: true,
					Content: []mcpsdk.Content{
						&mcpsdk.TextContent{Text: "task monitoring ended unexpectedly"},
					},
				}, nil, nil
			}

			// Check if task has started (in_progress) or reached a finished state
			if status.Task.Status == "in_progress" || status.Task.Status == "complete" || status.Task.Status == "failed" || status.Task.Status == "canceled" {
				monitor.Stop()

				// Marshal task to compact JSON
				taskJSON, err := json.Marshal(status.Task)
				if err != nil {
					return &mcpsdk.CallToolResult{
						IsError: true,
						Content: []mcpsdk.Content{
							&mcpsdk.TextContent{Text: fmt.Sprintf("failed to marshal task: %v", err)},
						},
					}, nil, nil
				}

				return &mcpsdk.CallToolResult{
					Content: []mcpsdk.Content{
						&mcpsdk.TextContent{Text: string(taskJSON)},
					},
					StructuredContent: status.Task,
				}, nil, nil
			}

		case <-time.After(30 * time.Second):
			monitor.Stop()
			return &mcpsdk.CallToolResult{
				IsError: true,
				Content: []mcpsdk.Content{
					&mcpsdk.TextContent{Text: "timeout waiting for task to start"},
				},
			}, nil, nil
		}
	}
}
