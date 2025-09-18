package domain

import "context"

type MCPToolCallStatus = string

const (
	MCPToolCallStatusPending  MCPToolCallStatus = "pending"
	MCPToolCallStatusComplete MCPToolCallStatus = "complete"
	MCPToolCallStatusFailed   MCPToolCallStatus = "failed"
)

type MCPToolCallEvent struct {
	ToolName   string            `json:"toolName"`
	Status     MCPToolCallStatus `json:"status"`
	ArgsJSON   string            `json:"argsJson,omitempty"`
	ResultJSON string            `json:"resultJson,omitempty"`
	Error      string            `json:"error,omitempty"`
}

type MCPEventStreamer interface {
	AddMCPToolCallEvent(ctx context.Context, workspaceId, sessionId string, event MCPToolCallEvent) error
}
