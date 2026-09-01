package llm2

import (
	"context"
	"fmt"
	"os"
	"sidekick/common"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const webSearchIntegrationPrompt = "Use the web search tool to find the latest stable release version of the Go programming language, then answer with the version number. You must perform a web search before answering."

const webSearchFollowUpPrompt = "Thanks. Without searching again, briefly repeat the version number you found."

// runWebSearchStream drains events while streaming and returns the response.
func runWebSearchStream(t *testing.T, provider Provider, request StreamRequest) (*MessageResponse, []Event) {
	t.Helper()
	eventChan := make(chan Event, 1000)
	var allEvents []Event
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for event := range eventChan {
			allEvents = append(allEvents, event)
			if event.Type == EventBlockStarted && event.ContentBlock != nil {
				fmt.Printf("Event[%d]: block_started block_type=%s\n", event.Index, event.ContentBlock.Type)
			}
		}
	}()
	response, err := provider.Stream(context.Background(), request, eventChan)
	close(eventChan)
	wg.Wait()
	if err != nil && strings.Contains(err.Error(), "Model not found") {
		t.Skipf("Skipping: model unavailable to this account: %v", err)
	}
	require.NoError(t, err)
	require.NotNil(t, response)
	return response, allEvents
}

func builtinToolUseBlocks(msg Message) []ContentBlock {
	var blocks []ContentBlock
	for _, block := range msg.Content {
		if block.Type == ContentBlockTypeBuiltinToolUse {
			blocks = append(blocks, block)
		}
	}
	return blocks
}

func builtinToolResultBlocks(msg Message) []ContentBlock {
	var blocks []ContentBlock
	for _, block := range msg.Content {
		if block.Type == ContentBlockTypeBuiltinToolResult {
			blocks = append(blocks, block)
		}
	}
	return blocks
}

// assertWebSearchRoundTrip sends the first response's output back to the same
// provider with a follow-up question, verifying native same-provider replay of
// builtin tool blocks is accepted by the API.
func assertWebSearchRoundTrip(t *testing.T, provider Provider, request StreamRequest, output Message) {
	t.Helper()
	request.Messages = append(request.Messages, output, Message{
		Role:    RoleUser,
		Content: TextContentBlocks(webSearchFollowUpPrompt),
	})
	response, _ := runWebSearchStream(t, provider, request)
	assert.NotEmpty(t, response.Output.Content,
		"expected non-empty output on round-trip request (stop reason: %s)", response.StopReason)
}

func TestAnthropicProvider_WebSearchIntegration(t *testing.T) {
	t.Parallel()
	if os.Getenv("SIDE_INTEGRATION_TEST") != "true" {
		t.Skip("Skipping integration test; SIDE_INTEGRATION_TEST not set")
	}

	provider := AnthropicProvider{AuthType: common.ProviderAuthTypeAPI}
	secretManager := requireIntegrationAPIKey(t, "ANTHROPIC_API_KEY")

	request := StreamRequest{
		Messages: []Message{{
			Role:    RoleUser,
			Content: TextContentBlocks(webSearchIntegrationPrompt),
		}},
		Options: Options{
			ModelConfig: common.ModelConfig{Provider: "anthropic"},
			Tools: []*common.Tool{{
				Type:      common.ToolTypeWebSearch,
				WebSearch: &common.WebSearchToolConfig{MaxUses: 2},
			}},
			ToolChoice: common.ToolChoice{Type: common.ToolChoiceTypeAuto},
		},
		SecretManager: secretManager,
	}

	response, _ := runWebSearchStream(t, provider, request)

	uses := builtinToolUseBlocks(response.Output)
	require.NotEmpty(t, uses, "expected at least one builtin_tool_use block")
	require.NotNil(t, uses[0].BuiltinToolUse)
	assert.Contains(t, uses[0].BuiltinToolUse.Id, anthropicBuiltinToolIdPrefix)
	assert.NotEmpty(t, uses[0].BuiltinToolUse.Arguments)

	results := builtinToolResultBlocks(response.Output)
	require.NotEmpty(t, results, "expected at least one builtin_tool_result block")
	require.NotNil(t, results[0].BuiltinToolResult)
	assert.Equal(t, uses[0].BuiltinToolUse.Id, results[0].BuiltinToolResult.ToolCallId)
	assert.NotEmpty(t, results[0].BuiltinToolResult.SearchResults)

	assertWebSearchRoundTrip(t, provider, request, response.Output)
}

func TestOpenAIResponsesProvider_WebSearchIntegration(t *testing.T) {
	t.Parallel()
	if os.Getenv("SIDE_INTEGRATION_TEST") != "true" {
		t.Skip("Skipping integration test; SIDE_INTEGRATION_TEST not set")
	}

	provider := OpenAIResponsesProvider{AuthType: common.ProviderAuthTypeAPI}
	secretManager := requireIntegrationAPIKey(t, "OPENAI_API_KEY")

	request := StreamRequest{
		Messages: []Message{{
			Role:    RoleUser,
			Content: TextContentBlocks(webSearchIntegrationPrompt),
		}},
		Options: Options{
			ModelConfig: common.ModelConfig{
				Provider: "openai",
				// ChatGPT (OAuth) accounts only support a subset of models on
				// the codex backend; gpt-5.4-mini works with both auth paths.
				Model:           "gpt-5.4-mini",
				ReasoningEffort: "low",
			},
			Tools:      []*common.Tool{{Type: common.ToolTypeWebSearch}},
			ToolChoice: common.ToolChoice{Type: common.ToolChoiceTypeAuto},
		},
		SecretManager: secretManager,
	}

	response, _ := runWebSearchStream(t, provider, request)

	uses := builtinToolUseBlocks(response.Output)
	require.NotEmpty(t, uses, "expected at least one builtin_tool_use block")
	require.NotNil(t, uses[0].BuiltinToolUse)
	assert.Contains(t, uses[0].BuiltinToolUse.Id, openaiWebSearchCallIdPrefix)
	assert.NotEmpty(t, uses[0].BuiltinToolUse.Arguments)
	assert.NotEmpty(t, uses[0].BuiltinToolUse.Status)

	// OpenAI reports no builtin tool results; searches are provider-internal.
	assert.Empty(t, builtinToolResultBlocks(response.Output))

	assertWebSearchRoundTrip(t, provider, request, response.Output)
}

func TestGoogleProvider_WebSearchIntegration(t *testing.T) {
	t.Parallel()
	if os.Getenv("SIDE_INTEGRATION_TEST") != "true" {
		t.Skip("Skipping integration test; SIDE_INTEGRATION_TEST not set")
	}

	provider := GoogleProvider{}
	secretManager := requireIntegrationAPIKey(t, "GOOGLE_API_KEY", "GEMINI_API_KEY")

	request := StreamRequest{
		Messages: []Message{{
			Role:    RoleUser,
			Content: TextContentBlocks(webSearchIntegrationPrompt),
		}},
		Options: Options{
			ModelConfig: common.ModelConfig{
				Provider: "google",
				Model:    "gemini-3-flash-preview",
			},
			Tools:      []*common.Tool{{Type: common.ToolTypeWebSearch}},
			ToolChoice: common.ToolChoice{Type: common.ToolChoiceTypeAuto},
		},
		SecretManager: secretManager,
	}

	response, _ := runWebSearchStream(t, provider, request)

	uses := builtinToolUseBlocks(response.Output)
	require.NotEmpty(t, uses, "expected a synthesized builtin_tool_use block from grounding metadata")
	require.NotNil(t, uses[0].BuiltinToolUse)
	assert.Contains(t, uses[0].BuiltinToolUse.Id, googleWebSearchCallIdPrefix)
	assert.NotEmpty(t, uses[0].BuiltinToolUse.Arguments)

	results := builtinToolResultBlocks(response.Output)
	require.NotEmpty(t, results, "expected a synthesized builtin_tool_result block")
	require.NotNil(t, results[0].BuiltinToolResult)
	assert.Equal(t, uses[0].BuiltinToolUse.Id, results[0].BuiltinToolResult.ToolCallId)

	assertWebSearchRoundTrip(t, provider, request, response.Output)
}

// TestWebSearchCrossProviderIntegration verifies that builtin tool blocks from
// one provider are accepted by another via client-style tool pair conversion.
func TestWebSearchCrossProviderIntegration(t *testing.T) {
	t.Parallel()
	if os.Getenv("SIDE_INTEGRATION_TEST") != "true" {
		t.Skip("Skipping integration test; SIDE_INTEGRATION_TEST not set")
	}

	// Anthropic-origin history (native ids) replayed through OpenAI.
	anthropicHistory := []Message{
		{
			Role:    RoleUser,
			Content: TextContentBlocks(webSearchIntegrationPrompt),
		},
		{
			Role: RoleAssistant,
			Content: []ContentBlock{
				{
					Type: ContentBlockTypeBuiltinToolUse,
					BuiltinToolUse: &BuiltinToolUseBlock{
						Id:        "srvtoolu_cross_test",
						Name:      "web_search",
						Arguments: `{"query":"latest stable Go release version"}`,
					},
				},
				{
					Type: ContentBlockTypeBuiltinToolResult,
					BuiltinToolResult: &BuiltinToolResultBlock{
						ToolCallId: "srvtoolu_cross_test",
						Name:       "web_search",
						SearchResults: []WebSearchResult{
							{URL: "https://go.dev/doc/devel/release", Title: "Go Release History"},
						},
					},
				},
				{Type: ContentBlockTypeText, Text: "The latest stable Go release is listed on the Go release history page."},
			},
		},
		{
			Role:    RoleUser,
			Content: TextContentBlocks("Which URL did you consult? Answer with just the URL."),
		},
	}

	provider := OpenAIResponsesProvider{AuthType: common.ProviderAuthTypeAPI}
	secretManager := requireIntegrationAPIKey(t, "OPENAI_API_KEY")

	request := StreamRequest{
		Messages: anthropicHistory,
		Options: Options{
			ModelConfig: common.ModelConfig{Provider: "openai", Model: "gpt-5.4-mini"},
			Tools:       []*common.Tool{{Type: common.ToolTypeWebSearch}},
			ToolChoice:  common.ToolChoice{Type: common.ToolChoiceTypeAuto},
		},
		SecretManager: secretManager,
	}

	response, _ := runWebSearchStream(t, provider, request)
	assert.Contains(t, response.Output.GetContentString(), "go.dev")
}