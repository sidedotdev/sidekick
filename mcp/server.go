package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"sidekick/client"
	"sidekick/domain"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rs/zerolog/log"
)

// StartTaskParams defines the parameters for the start_task tool
type StartTaskParams struct {
	Description           string `json:"description"`
	FlowType              string `json:"flowType,omitempty"`
	DetermineRequirements *bool  `json:"determineRequirements,omitempty"`
	StartBranch           string `json:"startBranch"`
}

// ListTasksParams defines the parameters for the list_tasks tool
type ListTasksParams struct {
	Statuses []string `json:"statuses,omitempty"`
}

// ViewActionParams defines the parameters for the view_action tool
type ViewActionParams struct {
	ActionId string `json:"actionId"`
	Verbose  *bool  `json:"verbose,omitempty"`
}

// RespondTextParams defines the parameters for the respond_text tool
type RespondTextParams struct {
	ActionId string `json:"actionId"`
	Content  string `json:"content"`
}

// ApproveActionParams defines the parameters for the approve_action tool
type ApproveActionParams struct {
	ActionId string `json:"actionId"`
}

// RejectActionParams defines the parameters for the reject_action tool
type RejectActionParams struct {
	ActionId string `json:"actionId"`
	Message  string `json:"message"`
}

// FlowProgressParams defines the parameters for the flow_progress tool
type FlowProgressParams struct {
	FlowId string `json:"flowId"`
}

// NewWorkspaceServer creates a new MCP server for a specific workspace
func NewWorkspaceServer(c client.Client, workspaceId string, mcpStreamer domain.MCPEventStreamer) *mcpsdk.Server {
	server := mcpsdk.NewServer(&mcpsdk.Implementation{Name: "sidekick"}, &mcpsdk.ServerOptions{HasTools: true})

	server.AddReceivingMiddleware(func(nextHandler mcpsdk.MethodHandler) mcpsdk.MethodHandler {
		return func(ctx context.Context, method string, req mcpsdk.Request) (mcpsdk.Result, error) {
			log.Info().Str("workspaceId", workspaceId).Str("sessionId", req.GetSession().ID()).Str("method", method).Msg("MCP request")
			return nextHandler(ctx, method, req)
		}
	})

	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name:        "start_task",
		Description: "Start a new LLM-driven task in the workspace",
	}, func(ctx context.Context, req *mcpsdk.CallToolRequest, args StartTaskParams) (*mcpsdk.CallToolResult, any, error) {
		return handleStartTaskWithEvents(ctx, c, workspaceId, mcpStreamer, req.Session.ID(), args, req)
	})

	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name:        "list_tasks",
		Description: "List tasks in the workspace with optional status filtering",
	}, func(ctx context.Context, req *mcpsdk.CallToolRequest, args ListTasksParams) (*mcpsdk.CallToolResult, any, error) {
		return handleListTasksWithEvents(ctx, c, workspaceId, mcpStreamer, req.Session.ID(), args, req)
	})

	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name:        "view_action",
		Description: "View details of a specific flow action",
	}, func(ctx context.Context, req *mcpsdk.CallToolRequest, args ViewActionParams) (*mcpsdk.CallToolResult, any, error) {
		return handleViewActionWithEvents(ctx, c, workspaceId, mcpStreamer, req.Session.ID(), args, req)
	})

	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name:        "respond_text",
		Description: "Respond to a flow action with text content",
	}, func(ctx context.Context, req *mcpsdk.CallToolRequest, args RespondTextParams) (*mcpsdk.CallToolResult, any, error) {
		return handleRespondTextWithEvents(ctx, c, workspaceId, mcpStreamer, req.Session.ID(), args, req)
	})

	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name:        "approve_action",
		Description: "Approve a flow action",
	}, func(ctx context.Context, req *mcpsdk.CallToolRequest, args ApproveActionParams) (*mcpsdk.CallToolResult, any, error) {
		return handleApproveActionWithEvents(ctx, c, workspaceId, mcpStreamer, req.Session.ID(), args, req)
	})

	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name:        "reject_action",
		Description: "Reject a flow action with a message",
	}, func(ctx context.Context, req *mcpsdk.CallToolRequest, args RejectActionParams) (*mcpsdk.CallToolResult, any, error) {
		return handleRejectActionWithEvents(ctx, c, workspaceId, mcpStreamer, req.Session.ID(), args, req)
	})

	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name:        "flow_progress",
		Description: "Get progress information for a flow including subflows and recent actions",
	}, func(ctx context.Context, req *mcpsdk.CallToolRequest, args FlowProgressParams) (*mcpsdk.CallToolResult, any, error) {
		return handleFlowProgressWithEvents(ctx, c, workspaceId, mcpStreamer, req.Session.ID(), args, req)
	})

	return server
}

// truncateString truncates a string to the specified length
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// summarizeFlowAction creates a summary of a flow action based on its type.
// This function provides specialized summaries for user_request.* and tool_call.*
// action types with 100-character truncation, and includes result details for
// completed user requests when parseable. Used by flow_progress tool.
func summarizeFlowAction(action domain.FlowAction) map[string]interface{} {
	summary := map[string]interface{}{
		"type":        "flow_action",
		"id":          action.Id,
		"actionType":  action.ActionType,
		"status":      action.ActionStatus,
		"subflowId":   action.SubflowId,
		"subflowName": action.SubflowName,
	}

	// Handle user_request.* actions
	if strings.HasPrefix(action.ActionType, "user_request.") {
		if requestKind, exists := action.ActionParams["requestKind"]; exists {
			summary["requestKind"] = requestKind
		}
		if requestContent, exists := action.ActionParams["requestContent"]; exists {
			if contentStr, ok := requestContent.(string); ok {
				summary["requestContent"] = truncateString(contentStr, 100)
			}
		}

		// If complete and ActionResult is parseable JSON with user response
		if action.ActionStatus == domain.ActionStatusComplete && action.ActionResult != "" {
			var result map[string]interface{}
			if err := json.Unmarshal([]byte(action.ActionResult), &result); err == nil {
				if approved, exists := result["Approved"]; exists {
					summary["Approved"] = approved
				}
				if choice, exists := result["Choice"]; exists {
					summary["Choice"] = choice
				}
				if content, exists := result["Content"]; exists {
					if contentStr, ok := content.(string); ok {
						summary["Content"] = truncateString(contentStr, 100)
					}
				}
			}
		}
	} else if strings.HasPrefix(action.ActionType, "tool_call.") {
		// Handle tool_call.* actions
		toolName := strings.TrimPrefix(action.ActionType, "tool_call.")
		summary["toolName"] = toolName

		// Truncate params
		if action.ActionParams != nil {
			paramsJSON, _ := json.Marshal(action.ActionParams)
			summary["params"] = truncateString(string(paramsJSON), 100)
		}

		// Truncate result
		if action.ActionResult != "" {
			summary["result"] = truncateString(action.ActionResult, 100)
		}
	} else {
		// Generic fallback for unknown action types
		if action.ActionParams != nil {
			paramsJSON, _ := json.Marshal(action.ActionParams)
			summary["actionParams"] = truncateString(string(paramsJSON), 100)
		}
		if action.ActionResult != "" {
			summary["actionResult"] = truncateString(action.ActionResult, 100)
		}
	}

	return summary
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

	// Validate startBranch
	startBranch := strings.TrimSpace(params.StartBranch)
	if startBranch == "" {
		return &mcpsdk.CallToolResult{
			IsError: true,
			Content: []mcpsdk.Content{
				&mcpsdk.TextContent{Text: "startBranch parameter is required and cannot be empty"},
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
			"startBranch":           startBranch,
			"envType":               "local_git_worktree",
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

// emitMCPEvent emits an MCP tool call event if sessionId is not empty
func emitMCPEvent(ctx context.Context, mcpStreamer domain.MCPEventStreamer, workspaceId, sessionId string, event domain.MCPToolCallEvent) {
	if sessionId != "" {
		mcpStreamer.AddMCPToolCallEvent(ctx, workspaceId, sessionId, event)
	}
}

func handleStartTaskWithEvents(ctx context.Context, c client.Client, workspaceId string, mcpStreamer domain.MCPEventStreamer, sessionId string, params StartTaskParams, req *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, any, error) {
	// Emit pending event
	argsJSON, _ := json.Marshal(params)
	emitMCPEvent(ctx, mcpStreamer, workspaceId, sessionId, domain.MCPToolCallEvent{
		ToolName: "start_task",
		Status:   domain.MCPToolCallStatusPending,
		ArgsJSON: string(argsJSON),
	})

	// Execute the tool
	result, structuredContent, err := handleStartTask(ctx, c, workspaceId, params)

	// Emit completion event
	if err != nil {
		emitMCPEvent(ctx, mcpStreamer, workspaceId, sessionId, domain.MCPToolCallEvent{
			ToolName: "start_task",
			Status:   domain.MCPToolCallStatusFailed,
			Error:    err.Error(),
		})
	} else {
		resultJSON, _ := json.Marshal(structuredContent)
		emitMCPEvent(ctx, mcpStreamer, workspaceId, sessionId, domain.MCPToolCallEvent{
			ToolName:   "start_task",
			Status:     domain.MCPToolCallStatusComplete,
			ResultJSON: string(resultJSON),
		})
	}

	return result, structuredContent, err
}

func handleListTasksWithEvents(ctx context.Context, c client.Client, workspaceId string, mcpStreamer domain.MCPEventStreamer, sessionId string, params ListTasksParams, req *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, any, error) {
	// Emit pending event
	argsJSON, _ := json.Marshal(params)
	emitMCPEvent(ctx, mcpStreamer, workspaceId, sessionId, domain.MCPToolCallEvent{
		ToolName: "list_tasks",
		Status:   domain.MCPToolCallStatusPending,
		ArgsJSON: string(argsJSON),
	})

	// Execute the tool
	result, structuredContent, err := handleListTasks(ctx, c, workspaceId, params)

	// Emit completion event
	if err != nil {
		emitMCPEvent(ctx, mcpStreamer, workspaceId, sessionId, domain.MCPToolCallEvent{
			ToolName: "list_tasks",
			Status:   domain.MCPToolCallStatusFailed,
			Error:    err.Error(),
		})
	} else {
		resultJSON, _ := json.Marshal(structuredContent)
		emitMCPEvent(ctx, mcpStreamer, workspaceId, sessionId, domain.MCPToolCallEvent{
			ToolName:   "list_tasks",
			Status:     domain.MCPToolCallStatusComplete,
			ResultJSON: string(resultJSON),
		})
	}

	return result, structuredContent, err
}

func handleViewActionWithEvents(ctx context.Context, c client.Client, workspaceId string, mcpStreamer domain.MCPEventStreamer, sessionId string, params ViewActionParams, req *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, any, error) {
	// Emit pending event
	argsJSON, _ := json.Marshal(params)
	emitMCPEvent(ctx, mcpStreamer, workspaceId, sessionId, domain.MCPToolCallEvent{
		ToolName: "view_action",
		Status:   domain.MCPToolCallStatusPending,
		ArgsJSON: string(argsJSON),
	})

	// Execute the tool
	result, structuredContent, err := handleViewAction(ctx, c, workspaceId, params)

	// Emit completion event
	if err != nil {
		emitMCPEvent(ctx, mcpStreamer, workspaceId, sessionId, domain.MCPToolCallEvent{
			ToolName: "view_action",
			Status:   domain.MCPToolCallStatusFailed,
			Error:    err.Error(),
		})
	} else {
		resultJSON, _ := json.Marshal(structuredContent)
		emitMCPEvent(ctx, mcpStreamer, workspaceId, sessionId, domain.MCPToolCallEvent{
			ToolName:   "view_action",
			Status:     domain.MCPToolCallStatusComplete,
			ResultJSON: string(resultJSON),
		})
	}

	return result, structuredContent, err
}

func handleRespondTextWithEvents(ctx context.Context, c client.Client, workspaceId string, mcpStreamer domain.MCPEventStreamer, sessionId string, params RespondTextParams, req *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, any, error) {
	// Emit pending event
	argsJSON, _ := json.Marshal(params)
	emitMCPEvent(ctx, mcpStreamer, workspaceId, sessionId, domain.MCPToolCallEvent{
		ToolName: "respond_text",
		Status:   domain.MCPToolCallStatusPending,
		ArgsJSON: string(argsJSON),
	})

	// Execute the tool
	result, structuredContent, err := handleRespondText(ctx, c, workspaceId, params)

	// Emit completion event
	if err != nil {
		emitMCPEvent(ctx, mcpStreamer, workspaceId, sessionId, domain.MCPToolCallEvent{
			ToolName: "respond_text",
			Status:   domain.MCPToolCallStatusFailed,
			Error:    err.Error(),
		})
	} else {
		resultJSON, _ := json.Marshal(structuredContent)
		emitMCPEvent(ctx, mcpStreamer, workspaceId, sessionId, domain.MCPToolCallEvent{
			ToolName:   "respond_text",
			Status:     domain.MCPToolCallStatusComplete,
			ResultJSON: string(resultJSON),
		})
	}

	return result, structuredContent, err
}

func handleApproveActionWithEvents(ctx context.Context, c client.Client, workspaceId string, mcpStreamer domain.MCPEventStreamer, sessionId string, params ApproveActionParams, req *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, any, error) {
	// Emit pending event
	argsJSON, _ := json.Marshal(params)
	emitMCPEvent(ctx, mcpStreamer, workspaceId, sessionId, domain.MCPToolCallEvent{
		ToolName: "approve_action",
		Status:   domain.MCPToolCallStatusPending,
		ArgsJSON: string(argsJSON),
	})

	// Execute the tool
	result, structuredContent, err := handleApproveAction(ctx, c, workspaceId, params)

	// Emit completion event
	if err != nil {
		emitMCPEvent(ctx, mcpStreamer, workspaceId, sessionId, domain.MCPToolCallEvent{
			ToolName: "approve_action",
			Status:   domain.MCPToolCallStatusFailed,
			Error:    err.Error(),
		})
	} else {
		resultJSON, _ := json.Marshal(structuredContent)
		emitMCPEvent(ctx, mcpStreamer, workspaceId, sessionId, domain.MCPToolCallEvent{
			ToolName:   "approve_action",
			Status:     domain.MCPToolCallStatusComplete,
			ResultJSON: string(resultJSON),
		})
	}

	return result, structuredContent, err
}

func handleRejectActionWithEvents(ctx context.Context, c client.Client, workspaceId string, mcpStreamer domain.MCPEventStreamer, sessionId string, params RejectActionParams, req *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, any, error) {
	// Emit pending event
	argsJSON, _ := json.Marshal(params)
	emitMCPEvent(ctx, mcpStreamer, workspaceId, sessionId, domain.MCPToolCallEvent{
		ToolName: "reject_action",
		Status:   domain.MCPToolCallStatusPending,
		ArgsJSON: string(argsJSON),
	})

	// Execute the tool
	result, structuredContent, err := handleRejectAction(ctx, c, workspaceId, params)

	// Emit completion event
	if err != nil {
		emitMCPEvent(ctx, mcpStreamer, workspaceId, sessionId, domain.MCPToolCallEvent{
			ToolName: "reject_action",
			Status:   domain.MCPToolCallStatusFailed,
			Error:    err.Error(),
		})
	} else {
		resultJSON, _ := json.Marshal(structuredContent)
		emitMCPEvent(ctx, mcpStreamer, workspaceId, sessionId, domain.MCPToolCallEvent{
			ToolName:   "reject_action",
			Status:     domain.MCPToolCallStatusComplete,
			ResultJSON: string(resultJSON),
		})
	}

	return result, structuredContent, err
}

func handleListTasks(ctx context.Context, c client.Client, workspaceId string, params ListTasksParams) (*mcpsdk.CallToolResult, any, error) {
	// Set default statuses if not provided
	statuses := params.Statuses
	if len(statuses) == 0 {
		statuses = []string{"to_do", "drafting", "blocked", "in_progress", "complete", "failed", "canceled"}
	}

	tasks, err := c.GetTasks(workspaceId, statuses)
	if err != nil {
		return &mcpsdk.CallToolResult{
			IsError: true,
			Content: []mcpsdk.Content{
				&mcpsdk.TextContent{Text: fmt.Sprintf("failed to get tasks: %v", err)},
			},
		}, nil, nil
	}

	// Marshal tasks to compact JSON
	tasksJSON, err := json.Marshal(tasks)
	if err != nil {
		return &mcpsdk.CallToolResult{
			IsError: true,
			Content: []mcpsdk.Content{
				&mcpsdk.TextContent{Text: fmt.Sprintf("failed to marshal tasks: %v", err)},
			},
		}, nil, nil
	}

	return &mcpsdk.CallToolResult{
		Content: []mcpsdk.Content{
			&mcpsdk.TextContent{Text: string(tasksJSON)},
		},
		StructuredContent: tasks,
	}, tasks, nil
}

func handleViewAction(ctx context.Context, c client.Client, workspaceId string, params ViewActionParams) (*mcpsdk.CallToolResult, any, error) {
	if params.ActionId == "" {
		return &mcpsdk.CallToolResult{
			IsError: true,
			Content: []mcpsdk.Content{
				&mcpsdk.TextContent{Text: "actionId parameter is required and cannot be empty"},
			},
		}, nil, nil
	}

	action, err := c.GetFlowAction(workspaceId, params.ActionId)
	if err != nil {
		return &mcpsdk.CallToolResult{
			IsError: true,
			Content: []mcpsdk.Content{
				&mcpsdk.TextContent{Text: fmt.Sprintf("failed to get flow action: %v", err)},
			},
		}, nil, nil
	}

	var structuredContent interface{}
	var textContent string

	// Check if verbose mode is requested
	verbose := params.Verbose != nil && *params.Verbose
	if verbose {
		// Return full flow action JSON
		structuredContent = action
		actionJSON, err := json.Marshal(action)
		if err != nil {
			return &mcpsdk.CallToolResult{
				IsError: true,
				Content: []mcpsdk.Content{
					&mcpsdk.TextContent{Text: fmt.Sprintf("failed to marshal flow action: %v", err)},
				},
			}, nil, nil
		}
		textContent = string(actionJSON)
	} else {
		// Return summarized view
		summary := summarizeFlowAction(action)
		structuredContent = summary
		summaryJSON, err := json.Marshal(summary)
		if err != nil {
			return &mcpsdk.CallToolResult{
				IsError: true,
				Content: []mcpsdk.Content{
					&mcpsdk.TextContent{Text: fmt.Sprintf("failed to marshal action summary: %v", err)},
				},
			}, nil, nil
		}
		textContent = string(summaryJSON)
	}

	return &mcpsdk.CallToolResult{
		Content: []mcpsdk.Content{
			&mcpsdk.TextContent{Text: textContent},
		},
		StructuredContent: structuredContent,
	}, nil, nil
}

func handleRespondText(ctx context.Context, c client.Client, workspaceId string, params RespondTextParams) (*mcpsdk.CallToolResult, any, error) {
	if params.ActionId == "" {
		return &mcpsdk.CallToolResult{
			IsError: true,
			Content: []mcpsdk.Content{
				&mcpsdk.TextContent{Text: "actionId parameter is required and cannot be empty"},
			},
		}, nil, nil
	}

	content := strings.TrimSpace(params.Content)
	if content == "" {
		return &mcpsdk.CallToolResult{
			IsError: true,
			Content: []mcpsdk.Content{
				&mcpsdk.TextContent{Text: "content parameter is required and cannot be empty"},
			},
		}, nil, nil
	}

	req := &client.CompleteFlowActionRequest{
		UserResponse: map[string]interface{}{
			"content": content,
		},
	}

	action, err := c.CompleteFlowAction(workspaceId, params.ActionId, req)
	if err != nil {
		return &mcpsdk.CallToolResult{
			IsError: true,
			Content: []mcpsdk.Content{
				&mcpsdk.TextContent{Text: fmt.Sprintf("failed to complete flow action: %v", err)},
			},
		}, nil, nil
	}

	actionJSON, err := json.Marshal(action)
	if err != nil {
		return &mcpsdk.CallToolResult{
			IsError: true,
			Content: []mcpsdk.Content{
				&mcpsdk.TextContent{Text: fmt.Sprintf("failed to marshal flow action: %v", err)},
			},
		}, nil, nil
	}

	return &mcpsdk.CallToolResult{
		Content: []mcpsdk.Content{
			&mcpsdk.TextContent{Text: string(actionJSON)},
		},
		StructuredContent: action,
	}, nil, nil
}

func handleApproveAction(ctx context.Context, c client.Client, workspaceId string, params ApproveActionParams) (*mcpsdk.CallToolResult, any, error) {
	if params.ActionId == "" {
		return &mcpsdk.CallToolResult{
			IsError: true,
			Content: []mcpsdk.Content{
				&mcpsdk.TextContent{Text: "actionId parameter is required and cannot be empty"},
			},
		}, nil, nil
	}

	req := &client.CompleteFlowActionRequest{
		UserResponse: map[string]interface{}{
			"approved": true,
		},
	}

	action, err := c.CompleteFlowAction(workspaceId, params.ActionId, req)
	if err != nil {
		return &mcpsdk.CallToolResult{
			IsError: true,
			Content: []mcpsdk.Content{
				&mcpsdk.TextContent{Text: fmt.Sprintf("failed to complete flow action: %v", err)},
			},
		}, nil, nil
	}

	actionJSON, err := json.Marshal(action)
	if err != nil {
		return &mcpsdk.CallToolResult{
			IsError: true,
			Content: []mcpsdk.Content{
				&mcpsdk.TextContent{Text: fmt.Sprintf("failed to marshal flow action: %v", err)},
			},
		}, nil, nil
	}

	return &mcpsdk.CallToolResult{
		Content: []mcpsdk.Content{
			&mcpsdk.TextContent{Text: string(actionJSON)},
		},
		StructuredContent: action,
	}, nil, nil
}

func handleRejectAction(ctx context.Context, c client.Client, workspaceId string, params RejectActionParams) (*mcpsdk.CallToolResult, any, error) {
	if params.ActionId == "" {
		return &mcpsdk.CallToolResult{
			IsError: true,
			Content: []mcpsdk.Content{
				&mcpsdk.TextContent{Text: "actionId parameter is required and cannot be empty"},
			},
		}, nil, nil
	}

	message := strings.TrimSpace(params.Message)
	if message == "" {
		return &mcpsdk.CallToolResult{
			IsError: true,
			Content: []mcpsdk.Content{
				&mcpsdk.TextContent{Text: "message parameter is required and cannot be empty"},
			},
		}, nil, nil
	}

	req := &client.CompleteFlowActionRequest{
		UserResponse: map[string]interface{}{
			"approved": false,
			"content":  message,
		},
	}

	action, err := c.CompleteFlowAction(workspaceId, params.ActionId, req)
	if err != nil {
		return &mcpsdk.CallToolResult{
			IsError: true,
			Content: []mcpsdk.Content{
				&mcpsdk.TextContent{Text: fmt.Sprintf("failed to complete flow action: %v", err)},
			},
		}, nil, nil
	}

	actionJSON, err := json.Marshal(action)
	if err != nil {
		return &mcpsdk.CallToolResult{
			IsError: true,
			Content: []mcpsdk.Content{
				&mcpsdk.TextContent{Text: fmt.Sprintf("failed to marshal flow action: %v", err)},
			},
		}, nil, nil
	}

	return &mcpsdk.CallToolResult{
		Content: []mcpsdk.Content{
			&mcpsdk.TextContent{Text: string(actionJSON)},
		},
		StructuredContent: action,
	}, nil, nil
}

func handleFlowProgressWithEvents(ctx context.Context, c client.Client, workspaceId string, mcpStreamer domain.MCPEventStreamer, sessionId string, params FlowProgressParams, req *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, any, error) {
	// Emit pending event
	argsJSON, _ := json.Marshal(params)
	emitMCPEvent(ctx, mcpStreamer, workspaceId, sessionId, domain.MCPToolCallEvent{
		ToolName: "flow_progress",
		Status:   domain.MCPToolCallStatusPending,
		ArgsJSON: string(argsJSON),
	})

	// Execute the tool
	result, structuredContent, err := handleFlowProgress(ctx, c, workspaceId, params)

	// Emit completion event
	if err != nil {
		emitMCPEvent(ctx, mcpStreamer, workspaceId, sessionId, domain.MCPToolCallEvent{
			ToolName: "flow_progress",
			Status:   domain.MCPToolCallStatusFailed,
			Error:    err.Error(),
		})
	} else {
		resultJSON, _ := json.Marshal(structuredContent)
		emitMCPEvent(ctx, mcpStreamer, workspaceId, sessionId, domain.MCPToolCallEvent{
			ToolName:   "flow_progress",
			Status:     domain.MCPToolCallStatusComplete,
			ResultJSON: string(resultJSON),
		})
	}

	return result, structuredContent, err
}

func handleFlowProgress(ctx context.Context, c client.Client, workspaceId string, params FlowProgressParams) (*mcpsdk.CallToolResult, any, error) {
	// Validate flowId
	if params.FlowId == "" {
		return &mcpsdk.CallToolResult{
			IsError: true,
			Content: []mcpsdk.Content{
				&mcpsdk.TextContent{Text: "flowId parameter is required and cannot be empty"},
			},
		}, nil, nil
	}

	// Get the flow
	flow, err := c.GetFlow(workspaceId, params.FlowId)
	if err != nil {
		return &mcpsdk.CallToolResult{
			IsError: true,
			Content: []mcpsdk.Content{
				&mcpsdk.TextContent{Text: fmt.Sprintf("failed to get flow: %v", err)},
			},
		}, nil, nil
	}

	// Get the parent task for parentObject
	task, err := c.GetTask(workspaceId, flow.ParentId)
	if err != nil {
		return &mcpsdk.CallToolResult{
			IsError: true,
			Content: []mcpsdk.Content{
				&mcpsdk.TextContent{Text: fmt.Sprintf("failed to get parent task: %v", err)},
			},
		}, nil, nil
	}

	// Get latest 10 flow actions
	flowActions, err := c.GetFlowActions(workspaceId, params.FlowId, "", 10)
	if err != nil {
		return &mcpsdk.CallToolResult{
			IsError: true,
			Content: []mcpsdk.Content{
				&mcpsdk.TextContent{Text: fmt.Sprintf("failed to get flow actions: %v", err)},
			},
		}, nil, nil
	}

	// Get all subflows
	subflows, err := c.GetSubflows(workspaceId, params.FlowId)
	if err != nil {
		return &mcpsdk.CallToolResult{
			IsError: true,
			Content: []mcpsdk.Content{
				&mcpsdk.TextContent{Text: fmt.Sprintf("failed to get subflows: %v", err)},
			},
		}, nil, nil
	}

	// Build the actions summary tree
	actionsSummary := buildActionsSummary(subflows, flowActions)

	// Build the response
	response := map[string]interface{}{
		"flowStatus": flow.Status,
		"parentObject": map[string]interface{}{
			"id":     task.Id,
			"status": task.Status,
		},
		"actions_summary": actionsSummary,
	}

	// Marshal to compact JSON
	responseJSON, err := json.Marshal(response)
	if err != nil {
		return &mcpsdk.CallToolResult{
			IsError: true,
			Content: []mcpsdk.Content{
				&mcpsdk.TextContent{Text: fmt.Sprintf("failed to marshal response: %v", err)},
			},
		}, nil, nil
	}

	return &mcpsdk.CallToolResult{
		Content: []mcpsdk.Content{
			&mcpsdk.TextContent{Text: string(responseJSON)},
		},
		StructuredContent: response,
	}, nil, nil
}

// buildActionsSummary builds a tree structure combining subflows and flow actions
func buildActionsSummary(subflows []domain.Subflow, flowActions []domain.FlowAction) []map[string]interface{} {
	// Create a map of subflows by ID for quick lookup
	subflowMap := make(map[string]domain.Subflow)
	for _, subflow := range subflows {
		subflowMap[subflow.Id] = subflow
	}

	// Create a map to track which subflows have been processed
	processedSubflows := make(map[string]bool)

	// Build subflow nodes with their children
	var buildSubflowNode func(subflow domain.Subflow) map[string]interface{}
	buildSubflowNode = func(subflow domain.Subflow) map[string]interface{} {
		node := map[string]interface{}{
			"type":   "subflow",
			"id":     subflow.Id,
			"status": subflow.Status,
		}

		if subflow.Name != "" {
			node["name"] = subflow.Name
		}
		if subflow.Description != "" {
			node["description"] = subflow.Description
		}
		if subflow.Result != "" {
			node["result"] = subflow.Result
		}
		if subflow.ParentSubflowId != "" {
			node["parentSubflowId"] = subflow.ParentSubflowId
		}

		// Find child subflows
		var children []map[string]interface{}
		for _, childSubflow := range subflows {
			if childSubflow.ParentSubflowId == subflow.Id && !processedSubflows[childSubflow.Id] {
				processedSubflows[childSubflow.Id] = true
				children = append(children, buildSubflowNode(childSubflow))
			}
		}

		// Add flow actions that belong to this subflow
		for _, action := range flowActions {
			if action.SubflowId == subflow.Id {
				children = append(children, summarizeFlowAction(action))
			}
		}

		if len(children) > 0 {
			node["children"] = children
		}

		return node
	}

	// Start with top-level nodes
	var result []map[string]interface{}

	// Add completed subflows at top level (when parentSubflowId is empty)
	for _, subflow := range subflows {
		if subflow.ParentSubflowId == "" && subflow.Status == "complete" && !processedSubflows[subflow.Id] {
			processedSubflows[subflow.Id] = true
			result = append(result, buildSubflowNode(subflow))
		}
	}

	// Add active subflows (multiple nested active subflows supported)
	for _, subflow := range subflows {
		if subflow.ParentSubflowId == "" && subflow.Status != "complete" && !processedSubflows[subflow.Id] {
			processedSubflows[subflow.Id] = true
			result = append(result, buildSubflowNode(subflow))
		}
	}

	// Add flow actions that don't belong to any subflow (top-level)
	for _, action := range flowActions {
		if action.SubflowId == "" {
			result = append(result, summarizeFlowAction(action))
		}
	}

	return result
}
