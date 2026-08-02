package persisted_ai

import (
	"strings"
	"testing"

	"sidekick/common"
	"sidekick/domain"
	"sidekick/llm2"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// builtinToolPairMessage builds an assistant message holding a server-side web
// search call and its result, as produced by e.g. the Anthropic provider.
func builtinToolPairMessage(resultText string) llm2.Message {
	return llm2.Message{
		Role: llm2.RoleAssistant,
		Content: []llm2.ContentBlock{
			{
				Type: llm2.ContentBlockTypeBuiltinToolUse,
				BuiltinToolUse: &llm2.BuiltinToolUseBlock{
					Id:        "srvtoolu_1",
					Name:      "web_search",
					Arguments: `{"query":"x"}`,
				},
			},
			{
				Type: llm2.ContentBlockTypeBuiltinToolResult,
				BuiltinToolResult: &llm2.BuiltinToolResultBlock{
					ToolCallId: "srvtoolu_1",
					Name:       "web_search",
					SearchResults: []llm2.WebSearchResult{
						{URL: "https://go.dev", Title: resultText},
					},
				},
			},
			{Type: llm2.ContentBlockTypeText, Text: "answer"},
		},
	}
}

func TestCleanLlm2ToolCallsAndResponses_PreservesBuiltinToolPairs(t *testing.T) {
	t.Parallel()

	messages := []llm2.Message{
		{Role: llm2.RoleUser, Content: llm2.TextContentBlocks("question")},
		builtinToolPairMessage("Go"),
		{Role: llm2.RoleUser, Content: llm2.TextContentBlocks("follow-up")},
	}

	cleaned := make([]llm2.Message, len(messages))
	copy(cleaned, messages)
	cleanLlm2ToolCallsAndResponses(&cleaned)

	require.Len(t, cleaned, 3)
	assert.Equal(t, messages[1], cleaned[1])
}

func TestTruncateLargeLlm2ToolResponses_SkipsBuiltinToolResults(t *testing.T) {
	t.Parallel()

	longTitle := strings.Repeat("a", 100000)
	messages := []llm2.Message{builtinToolPairMessage(longTitle)}
	isRetained := []bool{false}

	modelConfig := common.ModelConfig{Provider: "anthropic", Model: "claude-opus-4-5"}
	result, _ := truncateLargeLlm2ToolResponses(messages, isRetained, 10, "anthropic", modelConfig)

	require.Len(t, result, 1)
	require.Len(t, result[0].Content, 3)
	builtinToolResult := result[0].Content[1].BuiltinToolResult
	require.NotNil(t, builtinToolResult)
	require.Len(t, builtinToolResult.SearchResults, 1)
	assert.Equal(t, longTitle, builtinToolResult.SearchResults[0].Title)
}

func TestConvertLlm2EventToFlowEvent_BuiltinToolBlocks(t *testing.T) {
	t.Parallel()

	blocks := make(map[int]llm2.ContentBlock)

	useStarted := convertLlm2EventToFlowEvent(llm2.Event{
		Type:  llm2.EventBlockStarted,
		Index: 0,
		ContentBlock: &llm2.ContentBlock{
			Type: llm2.ContentBlockTypeBuiltinToolUse,
			BuiltinToolUse: &llm2.BuiltinToolUseBlock{
				Id:        "srvtoolu_1",
				Name:      "web_search",
				Arguments: `{"query":`,
			},
		},
	}, "fa_1", blocks)
	require.IsType(t, domain.ChatMessageDeltaEvent{}, useStarted)
	useDelta := useStarted.(domain.ChatMessageDeltaEvent).ChatMessageDelta
	require.Len(t, useDelta.ToolCalls, 1)
	assert.Equal(t, "srvtoolu_1", useDelta.ToolCalls[0].Id)
	assert.Equal(t, "web_search", useDelta.ToolCalls[0].Name)

	argsDelta := convertLlm2EventToFlowEvent(llm2.Event{
		Type:  llm2.EventTextDelta,
		Index: 0,
		Delta: `"golang"}`,
	}, "fa_1", blocks)
	require.IsType(t, domain.ChatMessageDeltaEvent{}, argsDelta)
	argsToolCalls := argsDelta.(domain.ChatMessageDeltaEvent).ChatMessageDelta.ToolCalls
	require.Len(t, argsToolCalls, 1)
	assert.Equal(t, `"golang"}`, argsToolCalls[0].Arguments)

	resultStarted := convertLlm2EventToFlowEvent(llm2.Event{
		Type:  llm2.EventBlockStarted,
		Index: 1,
		ContentBlock: &llm2.ContentBlock{
			Type: llm2.ContentBlockTypeBuiltinToolResult,
			BuiltinToolResult: &llm2.BuiltinToolResultBlock{
				ToolCallId: "srvtoolu_1",
				Name:       "web_search",
				SearchResults: []llm2.WebSearchResult{
					{URL: "https://go.dev", Title: "Go"},
				},
			},
		},
	}, "fa_1", blocks)
	require.IsType(t, domain.ProgressTextEvent{}, resultStarted)
	progress := resultStarted.(domain.ProgressTextEvent)
	assert.Equal(t, "fa_1", progress.ParentId)
	assert.Contains(t, progress.Text, "web_search results:")
	assert.Contains(t, progress.Text, "https://go.dev")

	assert.Nil(t, convertLlm2EventToFlowEvent(llm2.Event{Type: llm2.EventBlockDone, Index: 1}, "fa_1", blocks))
}