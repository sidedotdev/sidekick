package llm2

import (
	"encoding/json"
	"sidekick/common"
	"testing"

	"github.com/invopop/jsonschema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func webSearchAndFunctionTools() []*common.Tool {
	return []*common.Tool{
		{
			Name:        "get_weather",
			Description: "Get the weather",
			Parameters:  &jsonschema.Schema{Type: "object"},
		},
		{
			Type: common.ToolTypeWebSearch,
			WebSearch: &common.WebSearchToolConfig{
				MaxUses:        3,
				AllowedDomains: []string{"example.com"},
			},
		},
	}
}

func TestToolsToAnthropicParams_WebSearch(t *testing.T) {
	t.Parallel()

	t.Run("included when enabled", func(t *testing.T) {
		t.Parallel()
		params, err := toolsToAnthropicParams(webSearchAndFunctionTools(), true)
		require.NoError(t, err)
		require.Len(t, params, 2)
		assert.NotNil(t, params[0].OfTool)
		require.NotNil(t, params[1].OfWebSearchTool20250305)
		assert.Equal(t, int64(3), params[1].OfWebSearchTool20250305.MaxUses.Value)
		assert.Equal(t, []string{"example.com"}, params[1].OfWebSearchTool20250305.AllowedDomains)
	})

	t.Run("errors when disabled", func(t *testing.T) {
		t.Parallel()
		_, err := toolsToAnthropicParams(webSearchAndFunctionTools(), false)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not opted into builtin tools")
	})
}

func TestAnthropicProviderWebSearchToolEnabled(t *testing.T) {
	t.Parallel()

	assert.True(t, AnthropicProvider{}.webSearchToolEnabled())
	assert.False(t, AnthropicProvider{AnthropicCompatible: true}.webSearchToolEnabled())
	assert.True(t, AnthropicProvider{
		AnthropicCompatible: true,
		BuiltinTools:         []string{"web_search"},
	}.webSearchToolEnabled())
}

func TestOpenaiResponsesFromTools_WebSearch(t *testing.T) {
	t.Parallel()

	t.Run("included when enabled", func(t *testing.T) {
		t.Parallel()
		params, err := openaiResponsesFromTools(webSearchAndFunctionTools(), true)
		require.NoError(t, err)
		require.Len(t, params, 2)
		assert.NotNil(t, params[0].OfFunction)
		require.NotNil(t, params[1].OfWebSearch)
		assert.Equal(t, []string{"example.com"}, params[1].OfWebSearch.Filters.AllowedDomains)
	})

	t.Run("errors when disabled", func(t *testing.T) {
		t.Parallel()
		_, err := openaiResponsesFromTools(webSearchAndFunctionTools(), false)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not opted into builtin tools")
	})
}

func TestOpenAIResponsesProviderWebSearchToolEnabled(t *testing.T) {
	t.Parallel()

	assert.True(t, OpenAIResponsesProvider{}.webSearchToolEnabled())
	assert.False(t, OpenAIResponsesProvider{BaseURL: "https://proxy.example.com"}.webSearchToolEnabled())
	assert.True(t, OpenAIResponsesProvider{
		BaseURL:     "https://proxy.example.com",
		BuiltinTools: []string{"web_search"},
	}.webSearchToolEnabled())
}

func TestGoogleFromLlm2Tools_WebSearch(t *testing.T) {
	t.Parallel()

	genaiTools, err := googleFromLlm2Tools(webSearchAndFunctionTools())
	require.NoError(t, err)
	require.Len(t, genaiTools, 2)
	require.Len(t, genaiTools[0].FunctionDeclarations, 1)
	assert.Equal(t, "get_weather", genaiTools[0].FunctionDeclarations[0].Name)
	assert.NotNil(t, genaiTools[1].GoogleSearch)

	onlyWebSearch, err := googleFromLlm2Tools([]*common.Tool{{Type: common.ToolTypeWebSearch}})
	require.NoError(t, err)
	require.Len(t, onlyWebSearch, 1)
	assert.NotNil(t, onlyWebSearch[0].GoogleSearch)
}

func TestFilterToolsByName_WithWebSearchTool(t *testing.T) {
	t.Parallel()

	tools := webSearchAndFunctionTools()

	// Forcing a specific function tool intentionally narrows the tool list to
	// just that tool, excluding web search for that turn.
	forced := filterToolsByName(tools, "get_weather")
	require.Len(t, forced, 1)
	assert.Equal(t, "get_weather", forced[0].Name)

	// An unknown forced name leaves the list untouched, keeping web search
	// available.
	unmatched := filterToolsByName(tools, "nonexistent_tool")
	assert.Equal(t, tools, unmatched)
}

func TestOpenaiWebSearchActionFromJSON(t *testing.T) {
	t.Parallel()

	t.Run("search with sources", func(t *testing.T) {
		t.Parallel()
		action, err := openaiWebSearchActionFromJSON(`{"type":"search","query":"golang","sources":[{"url":"https://go.dev"}]}`)
		require.NoError(t, err)
		require.NotNil(t, action.OfSearch)
		assert.Equal(t, "golang", action.OfSearch.Query)
		require.Len(t, action.OfSearch.Sources, 1)
		assert.Equal(t, "https://go.dev", action.OfSearch.Sources[0].URL)
	})

	t.Run("open_page", func(t *testing.T) {
		t.Parallel()
		action, err := openaiWebSearchActionFromJSON(`{"type":"open_page","url":"https://go.dev"}`)
		require.NoError(t, err)
		require.NotNil(t, action.OfOpenPage)
		assert.Equal(t, "https://go.dev", action.OfOpenPage.URL)
	})

	t.Run("find", func(t *testing.T) {
		t.Parallel()
		for _, actionType := range []string{"find", "find_in_page"} {
			action, err := openaiWebSearchActionFromJSON(`{"type":"` + actionType + `","url":"https://go.dev","pattern":"generics"}`)
			require.NoError(t, err)
			require.NotNil(t, action.OfFind)
			assert.Equal(t, "generics", action.OfFind.Pattern)

			// The API rejects the SDK's default "find" type on the wire.
			data, err := json.Marshal(action.OfFind)
			require.NoError(t, err)
			assert.Contains(t, string(data), `"type":"find_in_page"`)
		}
	})

	t.Run("invalid json", func(t *testing.T) {
		t.Parallel()
		_, err := openaiWebSearchActionFromJSON("{not json")
		assert.Error(t, err)
	})
}

func TestSplitGoogleFunctionResponseContents(t *testing.T) {
	t.Parallel()

	messages := []Message{
		{Role: RoleAssistant, Content: []ContentBlock{
			{Type: ContentBlockTypeToolUse, ToolUse: &ToolUseBlock{Id: "call_1", Name: "web_search", Arguments: `{"query":"x"}`}},
		}},
		{Role: RoleUser, Content: []ContentBlock{
			{Type: ContentBlockTypeToolResult, ToolResult: &ToolResultBlock{
				ToolCallId: "call_1",
				Name:       "web_search",
				Content:    TextContentBlocks("result text"),
			}},
			{Type: ContentBlockTypeText, Text: "follow-up question"},
		}},
	}

	contents, err := googleFromLlm2Messages(messages, false, "gemini-3-flash-preview")
	require.NoError(t, err)

	// The functionResponse must not share a user turn with the follow-up text,
	// since Gemini returns an empty candidate for such mixed turns.
	require.Len(t, contents, 3)
	require.Len(t, contents[1].Parts, 1)
	assert.NotNil(t, contents[1].Parts[0].FunctionResponse)
	assert.Equal(t, "user", contents[1].Role)
	require.Len(t, contents[2].Parts, 1)
	assert.Equal(t, "follow-up question", contents[2].Parts[0].Text)
	assert.Equal(t, "user", contents[2].Role)
}

func TestGoogleBuiltinToolBlockReplay(t *testing.T) {
	t.Parallel()

	googlePair := []ContentBlock{
		{Type: ContentBlockTypeText, Text: "answer based on search"},
		{
			Type: ContentBlockTypeBuiltinToolUse,
			BuiltinToolUse: &BuiltinToolUseBlock{
				Id:        "google_ws_1",
				Name:      "web_search",
				Arguments: `{"queries":["golang"]}`,
			},
		},
		{
			Type: ContentBlockTypeBuiltinToolResult,
			BuiltinToolResult: &BuiltinToolResultBlock{
				ToolCallId:    "google_ws_1",
				Name:          "web_search",
				SearchResults: []WebSearchResult{{URL: "https://go.dev", Title: "Go"}},
			},
		},
	}
	messages := []Message{
		{Role: RoleUser, Content: TextContentBlocks("question")},
		{Role: RoleAssistant, Content: googlePair},
		{Role: RoleUser, Content: TextContentBlocks("follow-up")},
	}

	// The legacy generateContent API cannot echo grounding natively, so
	// google-origin blocks become a client-style pair on replay, keeping the
	// search results visible to the model.
	converted := convertForeignBuiltinToolBlocks(messages, noNativeBuiltinToolBlocks)
	contents, err := googleFromLlm2Messages(converted, false, "gemini-3-flash-preview")
	require.NoError(t, err)
	require.Len(t, contents, 4)

	assert.Equal(t, "model", contents[1].Role)
	require.Len(t, contents[1].Parts, 2)
	assert.Equal(t, "answer based on search", contents[1].Parts[0].Text)
	require.NotNil(t, contents[1].Parts[1].FunctionCall)
	assert.Equal(t, "google_ws_1", contents[1].Parts[1].FunctionCall.ID)
	assert.Equal(t, "builtin_web_search", contents[1].Parts[1].FunctionCall.Name)

	assert.Equal(t, "user", contents[2].Role)
	require.Len(t, contents[2].Parts, 1)
	functionResponse := contents[2].Parts[0].FunctionResponse
	require.NotNil(t, functionResponse)
	assert.Equal(t, "google_ws_1", functionResponse.ID)
	assert.Contains(t, functionResponse.Response["output"], "https://go.dev")

	assert.Equal(t, "user", contents[3].Role)
	assert.Equal(t, "follow-up", contents[3].Parts[0].Text)
}

func TestReorderSyntheticGroundingBlocks(t *testing.T) {
	t.Parallel()

	serverUse := ContentBlock{
		Type:          ContentBlockTypeBuiltinToolUse,
		BuiltinToolUse: &BuiltinToolUseBlock{Id: "google_ws_1", Name: "web_search"},
	}
	serverResult := ContentBlock{
		Type:             ContentBlockTypeBuiltinToolResult,
		BuiltinToolResult: &BuiltinToolResultBlock{ToolCallId: "google_ws_1", Name: "web_search"},
	}
	reasoning := ContentBlock{Type: ContentBlockTypeReasoning, Reasoning: &ReasoningBlock{Text: "thinking"}}
	answer := ContentBlock{Type: ContentBlockTypeText, Text: "grounded answer"}

	t.Run("pair moves before answer text, after reasoning", func(t *testing.T) {
		t.Parallel()
		msg := reorderSyntheticGroundingBlocks(Message{
			Role:    RoleAssistant,
			Content: []ContentBlock{reasoning, answer, serverUse, serverResult},
		})
		require.Len(t, msg.Content, 4)
		assert.Equal(t, ContentBlockTypeReasoning, msg.Content[0].Type)
		assert.Equal(t, ContentBlockTypeBuiltinToolUse, msg.Content[1].Type)
		assert.Equal(t, ContentBlockTypeBuiltinToolResult, msg.Content[2].Type)
		assert.Equal(t, ContentBlockTypeText, msg.Content[3].Type)
	})

	t.Run("no server blocks leaves message unchanged", func(t *testing.T) {
		t.Parallel()
		original := Message{Role: RoleAssistant, Content: []ContentBlock{reasoning, answer}}
		assert.Equal(t, original, reorderSyntheticGroundingBlocks(original))
	})

	t.Run("no text keeps pair at end", func(t *testing.T) {
		t.Parallel()
		msg := reorderSyntheticGroundingBlocks(Message{
			Role:    RoleAssistant,
			Content: []ContentBlock{reasoning, serverUse, serverResult},
		})
		require.Len(t, msg.Content, 3)
		assert.Equal(t, ContentBlockTypeReasoning, msg.Content[0].Type)
		assert.Equal(t, ContentBlockTypeBuiltinToolUse, msg.Content[1].Type)
		assert.Equal(t, ContentBlockTypeBuiltinToolResult, msg.Content[2].Type)
	})
}

func TestBuiltinToolBlocksJSONRoundTrip(t *testing.T) {
	t.Parallel()

	msg := Message{
		Role: RoleAssistant,
		Content: []ContentBlock{
			{
				Type: ContentBlockTypeBuiltinToolUse,
				BuiltinToolUse: &BuiltinToolUseBlock{
					Id:        "srvtoolu_1",
					Name:      "web_search",
					Arguments: `{"query":"x"}`,
					Status:    "completed",
				},
			},
			{
				Type: ContentBlockTypeBuiltinToolResult,
				BuiltinToolResult: &BuiltinToolResultBlock{
					ToolCallId: "srvtoolu_1",
					Name:       "web_search",
					SearchResults: []WebSearchResult{
						{URL: "https://go.dev", Title: "Go", PageAge: "1 day", EncryptedContent: "enc"},
					},
				},
			},
		},
	}

	data, err := json.Marshal(msg)
	require.NoError(t, err)
	var decoded Message
	require.NoError(t, json.Unmarshal(data, &decoded))
	assert.Equal(t, msg, decoded)
}