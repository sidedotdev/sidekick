package llm2

import (
	"fmt"
	"strings"
)

// Id prefixes assigned by each provider to server-side web search tool calls,
// used to detect whether a persisted builtin tool block originated from the
// provider currently being targeted (native same-provider replay) or from a
// different provider (cross-provider conversion).
const (
	anthropicBuiltinToolIdPrefix = "srvtoolu_"
	openaiWebSearchCallIdPrefix = "ws_"
	googleWebSearchCallIdPrefix = "google_ws_"
)

// webSearchToolName is the normalized builtin tool name used across providers.
const webSearchToolName = "web_search"

// builtinToolClientNamePrefix marks client-style tool pairs converted from
// builtin tool blocks, so they can be recognized and reversed back into
// builtin_tool_use/builtin_tool_result blocks.
const builtinToolClientNamePrefix = "builtin_"

func clientNameForBuiltinTool(name string) string {
	if name == "" {
		name = webSearchToolName
	}
	return builtinToolClientNamePrefix + name
}

// BuiltinToolNameFromClientName reverses clientNameForBuiltinTool, reporting
// whether the given client tool name originated from a builtin tool block.
func BuiltinToolNameFromClientName(clientName string) (string, bool) {
	name, ok := strings.CutPrefix(clientName, builtinToolClientNamePrefix)
	if !ok || name == "" {
		return "", false
	}
	return name, true
}

// Design notes (confirmed with the user after studying all three provider APIs):
//   - Web search is configured as a tool-list entry (common.Tool with
//     ToolTypeWebSearch) threaded through Options.Tools, not a separate
//     Options field.
//   - No dedicated usage counter is tracked: web search call counts are
//     derivable from the builtin_tool_use blocks recorded in message content
//     (Anthropic reports searched content as regular input tokens, which the
//     existing token accounting already captures).
//   - Google reports web search grounding out-of-band (response-side
//     groundingMetadata), and the legacy generateContent API has no request
//     representation for it. Google-origin blocks are preserved verbatim in
//     persisted history and converted to client-style tool pairs at
//     request-build time (for Google itself too), keeping prior search
//     results visible to the model. Native google→google replay should be
//     revisited once the Go genai SDK supports the Interactions API, whose
//     stateless step history can echo google_search steps verbatim.

// convertForeignBuiltinToolBlocks rewrites builtin tool use/result blocks that
// did not originate from the target provider (per isNative) into client-style
// tool_use/tool_result pairs. Native blocks are left untouched so providers can
// replay them verbatim. Since providers require client tool results in a
// user-role message following the assistant turn, the original assistant
// message is split around each result so the call/result/answer chronology is
// preserved. A foreign builtin tool use without a matching result (e.g. from
// OpenAI, which never reports results) gets a synthesized result so no
// dangling tool call remains.
func convertForeignBuiltinToolBlocks(messages []Message, isNative func(ContentBlock) bool) []Message {
	var out []Message

	for _, msg := range messages {
		hasForeign := false
		for _, b := range msg.Content {
			if (b.Type == ContentBlockTypeBuiltinToolUse || b.Type == ContentBlockTypeBuiltinToolResult) && !isNative(b) {
				hasForeign = true
				break
			}
		}
		if !hasForeign {
			out = append(out, msg)
			continue
		}

		foreignResultIds := make(map[string]bool)
		for _, b := range msg.Content {
			if b.Type == ContentBlockTypeBuiltinToolResult && !isNative(b) && b.BuiltinToolResult != nil {
				foreignResultIds[b.BuiltinToolResult.ToolCallId] = true
			}
		}

		var currentContent []ContentBlock
		var pendingResults []ContentBlock
		flush := func() {
			if len(currentContent) > 0 {
				out = append(out, Message{Role: msg.Role, Content: currentContent})
				currentContent = nil
			}
			if len(pendingResults) > 0 {
				out = append(out, Message{Role: RoleUser, Content: pendingResults})
				pendingResults = nil
			}
		}

		for _, b := range msg.Content {
			switch {
			case b.Type == ContentBlockTypeBuiltinToolUse && !isNative(b):
				if b.BuiltinToolUse == nil {
					continue
				}
				if len(pendingResults) > 0 {
					flush()
				}
				name := clientNameForBuiltinTool(b.BuiltinToolUse.Name)
				currentContent = append(currentContent, ContentBlock{
					Type: ContentBlockTypeToolUse,
					ToolUse: &ToolUseBlock{
						Id:        b.BuiltinToolUse.Id,
						Name:      name,
						Arguments: b.BuiltinToolUse.Arguments,
					},
				})
				if !foreignResultIds[b.BuiltinToolUse.Id] {
					pendingResults = append(pendingResults, ContentBlock{
						Type: ContentBlockTypeToolResult,
						ToolResult: &ToolResultBlock{
							ToolCallId: b.BuiltinToolUse.Id,
							Name:       name,
							Content:    TextContentBlocks("Web search was performed server-side by the originating provider; its results informed the surrounding response."),
						},
					})
				}
			case b.Type == ContentBlockTypeBuiltinToolResult && !isNative(b):
				if b.BuiltinToolResult == nil {
					continue
				}
				name := clientNameForBuiltinTool(b.BuiltinToolResult.Name)
				pendingResults = append(pendingResults, ContentBlock{
					Type: ContentBlockTypeToolResult,
					ToolResult: &ToolResultBlock{
						ToolCallId: b.BuiltinToolResult.ToolCallId,
						Name:       name,
						IsError:    b.BuiltinToolResult.IsError,
						Content:    TextContentBlocks(BuiltinToolResultText(b.BuiltinToolResult)),
					},
				})
			default:
				if len(pendingResults) > 0 {
					flush()
				}
				currentContent = append(currentContent, b)
			}
		}
		flush()
	}

	return out
}

// BuiltinToolResultText renders a builtin tool result as human-readable text for
// cross-provider conversion and streaming display, dropping provider-internal
// fields like Anthropic's encrypted content.
func BuiltinToolResultText(r *BuiltinToolResultBlock) string {
	var sb strings.Builder
	if r.Content != "" {
		sb.WriteString(r.Content)
	}
	for _, res := range r.SearchResults {
		if sb.Len() > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString(res.Title)
		sb.WriteString(" — ")
		sb.WriteString(res.URL)
		if res.PageAge != "" {
			sb.WriteString(fmt.Sprintf(" (%s)", res.PageAge))
		}
	}
	if sb.Len() == 0 {
		return "(no results)"
	}
	return sb.String()
}

func isAnthropicNativeBuiltinToolBlock(block ContentBlock) bool {
	switch block.Type {
	case ContentBlockTypeBuiltinToolUse:
		return block.BuiltinToolUse != nil && strings.HasPrefix(block.BuiltinToolUse.Id, anthropicBuiltinToolIdPrefix)
	case ContentBlockTypeBuiltinToolResult:
		return block.BuiltinToolResult != nil && strings.HasPrefix(block.BuiltinToolResult.ToolCallId, anthropicBuiltinToolIdPrefix)
	}
	return false
}

func isOpenAINativeBuiltinToolBlock(block ContentBlock) bool {
	// OpenAI never reports builtin tool results, so only use blocks can be native.
	return block.Type == ContentBlockTypeBuiltinToolUse &&
		block.BuiltinToolUse != nil &&
		strings.HasPrefix(block.BuiltinToolUse.Id, openaiWebSearchCallIdPrefix)
}

// noNativeBuiltinToolBlocks is for providers with no native replay
// representation for builtin tool blocks (e.g. Google's legacy generateContent
// API, Bedrock).
func noNativeBuiltinToolBlocks(ContentBlock) bool {
	return false
}