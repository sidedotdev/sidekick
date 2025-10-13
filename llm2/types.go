package llm2

// Role for the v2 message model. Provider-specific synonyms like "developer" should be
// handled in adapters (map to RoleSystem on ingest/emit as needed).
type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleSystem    Role = "system"
)

// Usage is surfaced on final responses (not deltas).
type Usage struct {
	InputTokens  int `json:"inputTokens"`
	OutputTokens int `json:"outputTokens"`
}

// ContentBlockType enumerates standardized content block kinds.
type ContentBlockType string

const (
	ContentBlockTypeText       ContentBlockType = "text"
	ContentBlockTypeImage      ContentBlockType = "image"
	ContentBlockTypeFile       ContentBlockType = "file"
	ContentBlockTypeToolUse    ContentBlockType = "tool_use"
	ContentBlockTypeToolResult ContentBlockType = "tool_result"
	ContentBlockTypeRefusal    ContentBlockType = "refusal"
	ContentBlockTypeReasoning  ContentBlockType = "reasoning"
	ContentBlockTypeMcpCall    ContentBlockType = "mcp_call"
)

// Media references for input blocks.
type ImageRef struct {
	Url string `json:"url,omitempty"`
}

type FileRef struct {
	Url      string `json:"url,omitempty"`
	MimeType string `json:"mimeType,omitempty"`
}

// Structured payloads for certain block types.
type RefusalBlock struct {
	Type   string `json:"type,omitempty"`
	Reason string `json:"reason,omitempty"`
}

type ReasoningBlock struct {
	Text    string `json:"text"`
	Summary string `json:"summary"`
}

type McpCallBlock struct {
	Server    string `json:"server"`
	Tool      string `json:"tool"`
	Arguments string `json:"arguments,omitempty"` // JSON string
}

// Tool invocation emitted by the assistant.
type ToolUseBlock struct {
	Id        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // JSON string
}

// Tool result content provided back to the assistant, modeled within a user-role message.
type ToolResultBlock struct {
	ToolCallId string `json:"toolCallId"`
	Name       string `json:"name,omitempty"`
	IsError    bool   `json:"isError,omitempty"`
	Text       string `json:"text,omitempty"`
}

// A single content block within a message turn.
type ContentBlock struct {
	Type         ContentBlockType `json:"type"`
	Text         string           `json:"text,omitempty"`
	Image        *ImageRef        `json:"image,omitempty"`
	File         *FileRef         `json:"file,omitempty"`
	ToolUse      *ToolUseBlock    `json:"toolUse,omitempty"`
	ToolResult   *ToolResultBlock `json:"toolResult,omitempty"`
	Refusal      *RefusalBlock    `json:"refusal,omitempty"`
	Reasoning    *ReasoningBlock  `json:"reasoning,omitempty"`
	McpCall      *McpCallBlock    `json:"mcpCall,omitempty"`
	CacheControl string           `json:"cacheControl,omitempty"`
}

// A single chat turn (message) consisting of a role and ordered content blocks.
type Message struct {
	Role    Role           `json:"role"`
	Content []ContentBlock `json:"content"`
}

// Provider-agnostic response with metadata and a single synthesized output message.
type MessageResponse struct {
	Id           string  `json:"id"`
	Model        string  `json:"model"`
	Provider     string  `json:"provider"`
	Output       Message `json:"output"`
	StopReason   string  `json:"stopReason"`
	StopSequence string  `json:"stopSequence,omitempty"`
	Usage        Usage   `json:"usage"`
}

// Minimal streaming delta for UX: append text and/or add new blocks over time.
// Role and Usage are intentionally omitted.
type MessageDelta struct {
	TextDelta string         `json:"textDelta,omitempty"`
	NewBlocks []ContentBlock `json:"newBlocks,omitempty"`
}
