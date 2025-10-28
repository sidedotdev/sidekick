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
	Text             string `json:"text"`
	Summary          string `json:"summary"`
	EncryptedContent string `json:"encryptedContent,omitempty"`
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
	Id           string           `json:"id"`
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

// EventType enumerates provider-agnostic streaming event kinds for content blocks.
// This is a small whitelist we can reliably map from OpenAI/Anthropic, etc.
// No message-level start/stop or usage events are included by design.
type EventType string

const (
	// A new content block starts. Append ContentBlock to the assistant Message.Content.
	EventBlockStarted EventType = "block_started"

	// No further deltas will target this block (also used to mark the end of a
	// tool_use block's arguments streaming).
	EventBlockDone EventType = "block_done"

	// A text fragment for the targeted block.
	// - For text or reasoning blocks: append to ContentBlock.Text
	// - For refusal blocks: append to ContentBlock.Refusal.Reason
	// - For tool_use blocks: append to ContentBlock.ToolUse.Arguments (JSON string)
	EventTextDelta EventType = "text_delta"

	// Provider-signaled "summary/output" channel distinct from other text.
	// Typically targets a text block (final answer channel), separate from
	// reasoning or other auxiliary text.
	EventSummaryTextDelta EventType = "summary_text_delta"

	// Provider-encrypted reasoning content fragment (e.g., OpenAI Responses
	// reasoning.encrypted_content). Accumulated into Reasoning.EncryptedContent
	// to enable stateless multi-turn reasoning continuity.
	EventSignatureDelta EventType = "signature_delta"
)

// Event represents a single provider-agnostic streaming event at the
// content-block level. Consumers apply events sequentially to reconstruct
// the assistant Message.Content.
//
// Targeting:
//   - Index is REQUIRED for all events and is the 0-based index of the
//     target block within the assistant Message.Content after applying
//     all prior events.
//
// Semantics:
//   - EventBlockStarted: append ContentBlock at Index (minimally populated, e.g.,
//     type=text with empty Text; type=tool_use with ToolUse.Id/Name when known,
//     empty ToolUse.Arguments; type=refusal with empty Refusal.Reason; etc.).
//   - EventBlockDone: close the block at Index; no further deltas should target it.
//     For tool_use blocks, this marks the end of arguments streaming.
//   - EventTextDelta: append Delta to the appropriate field depending on
//     ContentBlock.Type (see EventType const docs above). For tool_use, append
//     to ToolUse.Arguments (JSON string).
//   - EventSummaryTextDelta: same payload as EventTextDelta, but indicates the
//     provider's final answer channel distinct from other text.
//   - EventSignatureDelta: append Delta to a provider-specific accumulator for
//     the block (e.g., reasoning signature/encrypted content). Not stored on
//     ContentBlock.
//
// Provider mapping (intended; adapters to be implemented later):
//   - Anthropic:
//   - content_block_start -> block_started
//   - content_block_delta {type=text_delta} on text/reasoning -> text_delta
//   - content_block_delta {type=thinking_delta} on reasoning -> text_delta
//   - content_block_delta {type=signature_delta} on reasoning -> signature_delta
//   - content_block_delta {type=input_json_delta} for server tool use -> text_delta targeting tool_use
//   - content_block_stop -> block_done
//   - OpenAI Responses:
//   - response.output_item.added -> block_started when the item corresponds 1:1 to a single content block
//     (e.g., a reasoning or tool_use item). If the item is a container (e.g., a message with multiple parts),
//     defer to content_part.* events to start blocks.
//   - response.output_item.done -> block_done for the corresponding block when 1:1 mapping was used
//   - response.content_part.added -> block_started (map part.type to ContentBlock.Type)
//   - response.content_part.done -> block_done
//   - response.output_text.delta -> text_delta on a reasoning block
//   - response.reasoning_text.delta -> text_delta on a reasoning block
//   - response.reasoning_summary_text.delta -> summary_text_delta on a text block
//   - response.refusal.delta -> text_delta targeting a refusal block
//   - response.function_call_arguments.delta -> text_delta targeting tool_use
//   - response.function_call_arguments.done -> block_done for that tool_use block
//
// Note: We do NOT expose provider item/part IDs on Event; Index and ordering
// are the canonical targeting mechanism.
type Event struct {
	Type         EventType     `json:"type"`
	Index        int           `json:"index"`                  // 0-based block index
	ContentBlock *ContentBlock `json:"contentBlock,omitempty"` // present for block_started; MAY be included for other events in future
	Delta        string        `json:"delta,omitempty"`        // for *_delta events (text_delta, summary_text_delta, signature_delta)
}
