package llm2

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConvertForeignBuiltinToolBlocks(t *testing.T) {
	t.Parallel()

	anthropicPair := []ContentBlock{
		{Type: ContentBlockTypeText, Text: "let me search"},
		{
			Type: ContentBlockTypeBuiltinToolUse,
			BuiltinToolUse: &BuiltinToolUseBlock{
				Id:        "srvtoolu_123",
				Name:      "web_search",
				Arguments: `{"query":"golang generics"}`,
			},
		},
		{
			Type: ContentBlockTypeBuiltinToolResult,
			BuiltinToolResult: &BuiltinToolResultBlock{
				ToolCallId: "srvtoolu_123",
				Name:       "web_search",
				SearchResults: []WebSearchResult{
					{URL: "https://go.dev", Title: "Go", PageAge: "1 day", EncryptedContent: "secret"},
				},
			},
		},
		{Type: ContentBlockTypeText, Text: "done"},
	}

	t.Run("native blocks are untouched", func(t *testing.T) {
		t.Parallel()
		messages := []Message{{Role: RoleAssistant, Content: anthropicPair}}
		out := convertForeignBuiltinToolBlocks(messages, hasAnthropicBuiltinToolId)
		assert.Equal(t, messages, out)
	})

	t.Run("foreign pair becomes client tool call and result preserving chronology", func(t *testing.T) {
		t.Parallel()
		messages := []Message{
			{Role: RoleAssistant, Content: anthropicPair},
			{Role: RoleUser, Content: TextContentBlocks("next question")},
		}
		out := convertForeignBuiltinToolBlocks(messages, noNativeBuiltinToolBlocks)

		// The answer text following the search result must stay after the
		// tool result, so the assistant message is split around it.
		require.Len(t, out, 4)

		assistant := out[0]
		assert.Equal(t, RoleAssistant, assistant.Role)
		require.Len(t, assistant.Content, 2)
		assert.Equal(t, "let me search", assistant.Content[0].Text)
		require.Equal(t, ContentBlockTypeToolUse, assistant.Content[1].Type)
		require.NotNil(t, assistant.Content[1].ToolUse)
		assert.Equal(t, "srvtoolu_123", assistant.Content[1].ToolUse.Id)
		assert.Equal(t, "builtin_web_search", assistant.Content[1].ToolUse.Name)
		assert.Equal(t, `{"query":"golang generics"}`, assistant.Content[1].ToolUse.Arguments)

		toolResultMsg := out[1]
		assert.Equal(t, RoleUser, toolResultMsg.Role)
		require.Len(t, toolResultMsg.Content, 1)
		require.Equal(t, ContentBlockTypeToolResult, toolResultMsg.Content[0].Type)
		toolResult := toolResultMsg.Content[0].ToolResult
		require.NotNil(t, toolResult)
		assert.Equal(t, "srvtoolu_123", toolResult.ToolCallId)
		assert.Equal(t, "builtin_web_search", toolResult.Name)
		assert.Contains(t, toolResult.TextContent(), "https://go.dev")
		assert.Contains(t, toolResult.TextContent(), "Go")
		assert.NotContains(t, toolResult.TextContent(), "secret")

		answer := out[2]
		assert.Equal(t, RoleAssistant, answer.Role)
		require.Len(t, answer.Content, 1)
		assert.Equal(t, "done", answer.Content[0].Text)

		assert.Equal(t, "next question", out[3].Content[0].Text)
	})

	t.Run("orphan use gets synthesized result before the answer", func(t *testing.T) {
		t.Parallel()
		messages := []Message{{
			Role: RoleAssistant,
			Content: []ContentBlock{
				{
					Type: ContentBlockTypeBuiltinToolUse,
					BuiltinToolUse: &BuiltinToolUseBlock{
						Id:        "ws_abc",
						Name:      "web_search",
						Arguments: `{"type":"search","query":"x"}`,
						Status:    "completed",
					},
				},
				{Type: ContentBlockTypeText, Text: "answer"},
			},
		}}
		out := convertForeignBuiltinToolBlocks(messages, noNativeBuiltinToolBlocks)

		require.Len(t, out, 3)
		require.Len(t, out[0].Content, 1)
		assert.Equal(t, ContentBlockTypeToolUse, out[0].Content[0].Type)
		require.Len(t, out[1].Content, 1)
		require.Equal(t, ContentBlockTypeToolResult, out[1].Content[0].Type)
		assert.Equal(t, "ws_abc", out[1].Content[0].ToolResult.ToolCallId)
		assert.NotEmpty(t, out[1].Content[0].ToolResult.TextContent())
		require.Len(t, out[2].Content, 1)
		assert.Equal(t, "answer", out[2].Content[0].Text)
	})

	t.Run("parallel uses group results together", func(t *testing.T) {
		t.Parallel()
		messages := []Message{{
			Role: RoleAssistant,
			Content: []ContentBlock{
				{Type: ContentBlockTypeBuiltinToolUse, BuiltinToolUse: &BuiltinToolUseBlock{Id: "srvtoolu_a", Name: "web_search"}},
				{Type: ContentBlockTypeBuiltinToolUse, BuiltinToolUse: &BuiltinToolUseBlock{Id: "srvtoolu_b", Name: "web_search"}},
				{Type: ContentBlockTypeBuiltinToolResult, BuiltinToolResult: &BuiltinToolResultBlock{ToolCallId: "srvtoolu_a", Name: "web_search"}},
				{Type: ContentBlockTypeBuiltinToolResult, BuiltinToolResult: &BuiltinToolResultBlock{ToolCallId: "srvtoolu_b", Name: "web_search"}},
				{Type: ContentBlockTypeText, Text: "combined answer"},
			},
		}}
		out := convertForeignBuiltinToolBlocks(messages, noNativeBuiltinToolBlocks)

		require.Len(t, out, 3)
		require.Len(t, out[0].Content, 2)
		assert.Equal(t, ContentBlockTypeToolUse, out[0].Content[0].Type)
		assert.Equal(t, ContentBlockTypeToolUse, out[0].Content[1].Type)
		require.Len(t, out[1].Content, 2)
		assert.Equal(t, "srvtoolu_a", out[1].Content[0].ToolResult.ToolCallId)
		assert.Equal(t, "srvtoolu_b", out[1].Content[1].ToolResult.ToolCallId)
		require.Len(t, out[2].Content, 1)
		assert.Equal(t, "combined answer", out[2].Content[0].Text)
	})

	t.Run("error result is marked as error", func(t *testing.T) {
		t.Parallel()
		messages := []Message{{
			Role: RoleAssistant,
			Content: []ContentBlock{
				{
					Type:           ContentBlockTypeBuiltinToolUse,
					BuiltinToolUse: &BuiltinToolUseBlock{Id: "srvtoolu_err", Name: "web_search"},
				},
				{
					Type: ContentBlockTypeBuiltinToolResult,
					BuiltinToolResult: &BuiltinToolResultBlock{
						ToolCallId: "srvtoolu_err",
						Name:       "web_search",
						IsError:    true,
						Content:    "max_uses_exceeded",
					},
				},
			},
		}}
		out := convertForeignBuiltinToolBlocks(messages, noNativeBuiltinToolBlocks)

		require.Len(t, out, 2)
		require.Len(t, out[1].Content, 1)
		toolResult := out[1].Content[0].ToolResult
		require.NotNil(t, toolResult)
		assert.True(t, toolResult.IsError)
		assert.Contains(t, toolResult.TextContent(), "max_uses_exceeded")
	})

	t.Run("messages without server blocks pass through", func(t *testing.T) {
		t.Parallel()
		messages := []Message{
			{Role: RoleUser, Content: TextContentBlocks("hi")},
			{Role: RoleAssistant, Content: TextContentBlocks("hello")},
		}
		out := convertForeignBuiltinToolBlocks(messages, noNativeBuiltinToolBlocks)
		assert.Equal(t, messages, out)
	})
}

func TestBuiltinToolClientNameReversibility(t *testing.T) {
	t.Parallel()

	name, ok := BuiltinToolNameFromClientName(clientNameForBuiltinTool("web_search"))
	assert.True(t, ok)
	assert.Equal(t, "web_search", name)

	name, ok = BuiltinToolNameFromClientName(clientNameForBuiltinTool(""))
	assert.True(t, ok)
	assert.Equal(t, "web_search", name)

	_, ok = BuiltinToolNameFromClientName("web_search")
	assert.False(t, ok)
	_, ok = BuiltinToolNameFromClientName("builtin_")
	assert.False(t, ok)
}

func TestNativeBuiltinToolBlockPredicateTrustsCoreMetadataOverIds(t *testing.T) {
	t.Parallel()

	tagged := func(id, provider, providerType string) ContentBlock {
		return ContentBlock{
			Type:           ContentBlockTypeBuiltinToolUse,
			BuiltinToolUse: &BuiltinToolUseBlock{Id: id},
			Metadata: map[string]any{MetadataNamespaceCore: map[string]any{
				"provider":     provider,
				"providerType": providerType,
			}},
		}
	}
	untagged := func(id string) ContentBlock {
		return ContentBlock{Type: ContentBlockTypeBuiltinToolUse, BuiltinToolUse: &BuiltinToolUseBlock{Id: id}}
	}

	isAnthropicNative := nativeBuiltinToolBlockPredicate("anthropic", "anthropic", hasAnthropicBuiltinToolId)
	isOpenAINative := nativeBuiltinToolBlockPredicate("openai", "openai", hasOpenAIBuiltinToolId)

	tests := []struct {
		name     string
		isNative func(ContentBlock) bool
		block    ContentBlock
		want     bool
	}{
		{"same provider with unrecognized id", isAnthropicNative, tagged("custom_1", "anthropic", "anthropic"), true},
		{"foreign provider with misleading id", isAnthropicNative, tagged("srvtoolu_1", "openai", "openai"), false},
		{"same type under another provider name", isAnthropicNative, tagged("srvtoolu_1", "work-anthropic", "anthropic"), false},
		{"same name with another provider type", isAnthropicNative, tagged("srvtoolu_1", "anthropic", "anthropic_compatible"), false},
		{"openai same provider with unrecognized id", isOpenAINative, tagged("call_1", "openai", "openai"), true},
		{"openai foreign provider with misleading id", isOpenAINative, tagged("ws_1", "google", "google"), false},
		{"untagged falls back to anthropic id", isAnthropicNative, untagged("srvtoolu_1"), true},
		{"untagged falls back to foreign id", isAnthropicNative, untagged("ws_1"), false},
		{"untagged falls back to openai id", isOpenAINative, untagged("ws_1"), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, tt.isNative(tt.block))
		})
	}
}

func TestConvertForeignBuiltinToolBlocksUsesCoreMetadata(t *testing.T) {
	t.Parallel()

	nativeBlocks := anthropicWebSearchBlocks("custom_native", "org")
	foreignBlocks := anthropicWebSearchBlocks("srvtoolu_foreign", "org")
	for i := range foreignBlocks {
		foreignBlocks[i].Metadata = map[string]any{MetadataNamespaceCore: map[string]any{
			"provider":     "openai",
			"providerType": "openai",
		}}
	}
	messages := []Message{{Role: RoleAssistant, Content: append(nativeBlocks, foreignBlocks...)}}

	out := convertForeignBuiltinToolBlocks(messages, nativeBuiltinToolBlockPredicate("anthropic", "anthropic", hasAnthropicBuiltinToolId))

	typesById := make(map[string][]ContentBlockType)
	for _, msg := range out {
		for _, block := range msg.Content {
			switch {
			case block.BuiltinToolUse != nil:
				typesById[block.BuiltinToolUse.Id] = append(typesById[block.BuiltinToolUse.Id], block.Type)
			case block.BuiltinToolResult != nil:
				typesById[block.BuiltinToolResult.ToolCallId] = append(typesById[block.BuiltinToolResult.ToolCallId], block.Type)
			case block.ToolUse != nil:
				typesById[block.ToolUse.Id] = append(typesById[block.ToolUse.Id], block.Type)
			case block.ToolResult != nil:
				typesById[block.ToolResult.ToolCallId] = append(typesById[block.ToolResult.ToolCallId], block.Type)
			}
		}
	}
	assert.Equal(t, map[string][]ContentBlockType{
		"custom_native":    {ContentBlockTypeBuiltinToolUse, ContentBlockTypeBuiltinToolResult},
		"srvtoolu_foreign": {ContentBlockTypeToolUse, ContentBlockTypeToolResult},
	}, typesById)
}

func TestIsNativeBuiltinToolBlockPredicates(t *testing.T) {
	t.Parallel()

	anthropicUse := ContentBlock{
		Type:           ContentBlockTypeBuiltinToolUse,
		BuiltinToolUse: &BuiltinToolUseBlock{Id: "srvtoolu_1"},
	}
	openaiUse := ContentBlock{
		Type:           ContentBlockTypeBuiltinToolUse,
		BuiltinToolUse: &BuiltinToolUseBlock{Id: "ws_1"},
	}
	googleUse := ContentBlock{
		Type:           ContentBlockTypeBuiltinToolUse,
		BuiltinToolUse: &BuiltinToolUseBlock{Id: "google_ws_1"},
	}
	anthropicResult := ContentBlock{
		Type:              ContentBlockTypeBuiltinToolResult,
		BuiltinToolResult: &BuiltinToolResultBlock{ToolCallId: "srvtoolu_1"},
	}

	assert.True(t, hasAnthropicBuiltinToolId(anthropicUse))
	assert.True(t, hasAnthropicBuiltinToolId(anthropicResult))
	assert.False(t, hasAnthropicBuiltinToolId(openaiUse))
	assert.False(t, hasAnthropicBuiltinToolId(googleUse))

	assert.True(t, hasOpenAIBuiltinToolId(openaiUse))
	assert.False(t, hasOpenAIBuiltinToolId(anthropicUse))
	assert.False(t, hasOpenAIBuiltinToolId(anthropicResult))

	assert.False(t, noNativeBuiltinToolBlocks(anthropicUse))
	assert.False(t, noNativeBuiltinToolBlocks(openaiUse))
	assert.False(t, noNativeBuiltinToolBlocks(googleUse))
}
