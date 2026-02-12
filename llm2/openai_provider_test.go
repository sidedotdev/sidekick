package llm2

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sidekick/common"
	"sidekick/secret_manager"
	"sidekick/utils"
	"sync"
	"testing"

	"github.com/invopop/jsonschema"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/stretchr/testify/assert"
)

func TestOpenAIProvider_Unauthorized(t *testing.T) {
	ctx := context.Background()
	mockSecretManager := &secret_manager.MockSecretManager{}
	provider := OpenAIProvider{}

	messages := []Message{
		{
			Role: RoleUser,
			Content: []ContentBlock{
				{
					Type: ContentBlockTypeText,
					Text: "Hello",
				},
			},
		},
	}

	options := Options{
		Params: Params{
			ModelConfig: common.ModelConfig{
				Provider: "openai",
				Model:    "gpt-4.1-nano-2025-04-14",
			},
		},
		Secrets: secret_manager.SecretManagerContainer{
			SecretManager: mockSecretManager,
		},
	}

	options.Params.ChatHistory = newTestChatHistoryWithMessages(messages)

	eventChan := make(chan Event, 10)
	defer close(eventChan)

	_, err := provider.Stream(ctx, options, eventChan)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "401")
}

func TestOpenAIProvider_WrapErrorBodyOnly(t *testing.T) {
	t.Parallel()

	responseBody := `{"message":"Tokens per day limit exceeded - too many tokens processed.","type":"too_many_tokens_error","param":"quota","code":"token_quota_exceeded"}`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Retry-After", "86399")
		w.Header().Set("Strict-Transport-Security", "max-age=3600; includeSubDomains")
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(responseBody))
	}))
	defer server.Close()

	ctx := context.Background()
	mockSecretManager := &secret_manager.MockSecretManager{}
	provider := OpenAIProvider{BaseURL: server.URL + "/v1"}

	messages := []Message{
		{
			Role: RoleUser,
			Content: []ContentBlock{
				{Type: ContentBlockTypeText, Text: "Hello"},
			},
		},
	}

	options := Options{
		Params: Params{
			ModelConfig: common.ModelConfig{
				Provider: "openai",
				Model:    "gpt-4.1-nano",
			},
		},
		Secrets: secret_manager.SecretManagerContainer{
			SecretManager: mockSecretManager,
		},
	}
	options.Params.ChatHistory = newTestChatHistoryWithMessages(messages)

	eventChan := make(chan Event, 10)
	defer close(eventChan)

	_, err := provider.Stream(ctx, options, eventChan)
	assert.Error(t, err)
	errStr := err.Error()

	assert.Contains(t, errStr, "429")
	assert.Contains(t, errStr, "response body:")
	assert.Contains(t, errStr, "too_many_tokens_error")
	assert.Contains(t, errStr, responseBody)

	// Headers should NOT be present in the error message
	assert.NotContains(t, errStr, "Content-Type")
	assert.NotContains(t, errStr, "Retry-After")
	assert.NotContains(t, errStr, "Strict-Transport-Security")
	assert.NotContains(t, errStr, "HTTP/")
}

// TestOpenAIProvider_UsageOnChunkWithChoices verifies that usage is captured
// even when the SSE stream includes usage data on a chunk that also contains
// choices (as litellm / OpenAI-compatible proxies commonly do).
func TestOpenAIProvider_UsageOnChunkWithChoices(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		flusher, _ := w.(http.Flusher)

		// First chunk: starts the text content, no usage.
		fmt.Fprint(w, "data: {\"id\":\"chatcmpl-1\",\"object\":\"chat.completion.chunk\",\"model\":\"test-model\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"Hello\"}}]}\n\n")
		flusher.Flush()

		// Second chunk: finishes with finish_reason AND includes usage
		// (this is the litellm pattern: usage on the same chunk as choices).
		// Includes Anthropic-specific cache_creation_input_tokens.
		fmt.Fprint(w, "data: {\"id\":\"chatcmpl-1\",\"object\":\"chat.completion.chunk\",\"model\":\"test-model\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\" world\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":5,\"total_tokens\":15,\"cache_creation_input_tokens\":100}}\n\n")
		flusher.Flush()

		fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer server.Close()

	ctx := context.Background()
	provider := OpenAIProvider{BaseURL: server.URL + "/v1"}

	messages := []Message{
		{
			Role:    RoleUser,
			Content: []ContentBlock{{Type: ContentBlockTypeText, Text: "Hi"}},
		},
	}

	options := Options{
		Params: Params{
			ModelConfig: common.ModelConfig{
				Provider: "test",
				Model:    "test-model",
			},
		},
		Secrets: secret_manager.SecretManagerContainer{
			SecretManager: &secret_manager.MockSecretManager{},
		},
	}
	options.Params.ChatHistory = newTestChatHistoryWithMessages(messages)

	eventChan := make(chan Event, 100)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range eventChan {
		}
	}()

	response, err := provider.Stream(ctx, options, eventChan)
	close(eventChan)
	wg.Wait()

	assert.NoError(t, err)
	assert.NotNil(t, response)
	assert.Equal(t, 10, response.Usage.InputTokens, "InputTokens should be captured from chunk with choices")
	assert.Equal(t, 5, response.Usage.OutputTokens, "OutputTokens should be captured from chunk with choices")
	assert.Equal(t, 100, response.Usage.CacheWriteInputTokens, "CacheWriteInputTokens from cache_creation_input_tokens")
	assert.Equal(t, "test-model", response.Model)
	assert.Equal(t, "stop", response.StopReason)
}

// TestOpenAIProvider_UsageOnSeparateFinalChunk verifies usage is captured from
// a final chunk with no choices (the standard OpenAI behavior).
func TestOpenAIProvider_UsageOnSeparateFinalChunk(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		flusher, _ := w.(http.Flusher)

		// Content chunk
		fmt.Fprint(w, "data: {\"id\":\"chatcmpl-2\",\"object\":\"chat.completion.chunk\",\"model\":\"gpt-4\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"Hi\"}}]}\n\n")
		flusher.Flush()

		// Finish chunk
		fmt.Fprint(w, "data: {\"id\":\"chatcmpl-2\",\"object\":\"chat.completion.chunk\",\"model\":\"gpt-4\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
		flusher.Flush()

		// Separate usage-only chunk (no choices array or empty choices)
		fmt.Fprint(w, "data: {\"id\":\"chatcmpl-2\",\"object\":\"chat.completion.chunk\",\"model\":\"gpt-4\",\"choices\":[],\"usage\":{\"prompt_tokens\":20,\"completion_tokens\":8,\"total_tokens\":28,\"prompt_tokens_details\":{\"cached_tokens\":5}}}\n\n")
		flusher.Flush()

		fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer server.Close()

	ctx := context.Background()
	provider := OpenAIProvider{BaseURL: server.URL + "/v1"}

	messages := []Message{
		{
			Role:    RoleUser,
			Content: []ContentBlock{{Type: ContentBlockTypeText, Text: "Hi"}},
		},
	}

	options := Options{
		Params: Params{
			ModelConfig: common.ModelConfig{
				Provider: "test",
				Model:    "gpt-4",
			},
		},
		Secrets: secret_manager.SecretManagerContainer{
			SecretManager: &secret_manager.MockSecretManager{},
		},
	}
	options.Params.ChatHistory = newTestChatHistoryWithMessages(messages)

	eventChan := make(chan Event, 100)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range eventChan {
		}
	}()

	response, err := provider.Stream(ctx, options, eventChan)
	close(eventChan)
	wg.Wait()

	assert.NoError(t, err)
	assert.NotNil(t, response)
	assert.Equal(t, 20, response.Usage.InputTokens, "InputTokens from usage-only chunk")
	assert.Equal(t, 8, response.Usage.OutputTokens, "OutputTokens from usage-only chunk")
	assert.Equal(t, 5, response.Usage.CacheReadInputTokens, "CacheReadInputTokens from cached_tokens")
}

func TestOpenAIProvider_Integration(t *testing.T) {
	t.Parallel()
	if os.Getenv("SIDE_INTEGRATION_TEST") != "true" {
		t.Skip("Skipping integration test; SIDE_INTEGRATION_TEST not set")
	}

	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr}).Level(zerolog.DebugLevel)
	ctx := context.Background()
	provider := OpenAIProvider{}

	mockTool := &common.Tool{
		Name:        "get_current_weather",
		Description: "Get the current weather in a given location",
		Parameters:  (&jsonschema.Reflector{DoNotReference: true}).Reflect(&getCurrentWeather{}),
	}

	messages := []Message{
		{
			Role: RoleUser,
			Content: []ContentBlock{
				{
					Type: ContentBlockTypeText,
					Text: "First say hi. After that, then look up what the weather is like in New York in celsius, then describe it in words.",
				},
			},
		},
	}

	options := Options{
		Params: Params{
			ModelConfig: common.ModelConfig{
				Provider: "openai",
				Model:    "gpt-4.1-nano-2025-04-14",
			},
			Temperature: utils.Ptr(float32(0)),
			Tools:       []*common.Tool{mockTool},
			ToolChoice:  common.ToolChoice{Type: common.ToolChoiceTypeAuto},
		},
		Secrets: secret_manager.SecretManagerContainer{
			SecretManager: secret_manager.NewCompositeSecretManager([]secret_manager.SecretManager{
				&secret_manager.EnvSecretManager{},
				&secret_manager.KeyringSecretManager{},
				&secret_manager.LocalConfigSecretManager{},
			}),
		},
	}

	eventChan := make(chan Event, 100)
	var allEvents []Event
	var sawBlockStartedToolUse bool
	var sawTextDelta bool
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		for event := range eventChan {
			allEvents = append(allEvents, event)
			if event.Type == EventBlockStarted && event.ContentBlock != nil && event.ContentBlock.Type == ContentBlockTypeToolUse {
				sawBlockStartedToolUse = true
			}
			if event.Type == EventTextDelta {
				sawTextDelta = true
			}
		}
	}()

	options.Params.ChatHistory = newTestChatHistoryWithMessages(messages)

	response, err := provider.Stream(ctx, options, eventChan)
	close(eventChan)
	wg.Wait()

	if err != nil {
		t.Fatalf("Stream returned an error: %v", err)
	}

	if response == nil {
		t.Fatal("Stream returned a nil response")
	}

	if len(allEvents) == 0 {
		t.Error("No events received")
	}

	if !sawBlockStartedToolUse {
		t.Error("Expected to see at least one block_started event with tool_use")
	}

	if !sawTextDelta {
		t.Error("Expected to see at least one text_delta event")
	}

	t.Logf("Response output content blocks: %d", len(response.Output.Content))

	var foundToolUse bool
	for _, block := range response.Output.Content {
		if block.Type == ContentBlockTypeToolUse {
			foundToolUse = true
			if block.ToolUse.Name == "get_current_weather" {
				t.Logf("Found tool_use block: %+v", block.ToolUse)
				break
			}
		}
	}

	if !foundToolUse {
		t.Error("Expected response.Output.Content to include a tool_use block with Name 'get_current_weather'")
	}

	assert.NotNil(t, response.Usage, "Usage field should not be nil")
	assert.Greater(t, response.Usage.InputTokens, 0, "InputTokens should be greater than 0")
	assert.Greater(t, response.Usage.OutputTokens, 0, "OutputTokens should be greater than 0")

	t.Logf("Usage: InputTokens=%d, OutputTokens=%d", response.Usage.InputTokens, response.Usage.OutputTokens)
	t.Logf("Model: %s, Provider: %s", response.Model, response.Provider)
	t.Logf("StopReason: %s", response.StopReason)

	t.Run("MultiTurn", func(t *testing.T) {
		messages = append(messages, response.Output)

		for _, block := range response.Output.Content {
			if block.Type == ContentBlockTypeToolUse && block.ToolUse != nil {
				messages = append(messages, Message{
					Role: RoleUser,
					Content: []ContentBlock{
						{
							Type: ContentBlockTypeToolResult,
							ToolResult: &ToolResultBlock{
								ToolCallId: block.ToolUse.Id,
								Text:       "25",
								IsError:    false,
							},
						},
					},
				})
			}
		}

		eventChan := make(chan Event, 100)
		var allEvents []Event
		var wg2 sync.WaitGroup

		wg2.Add(1)
		go func() {
			defer wg2.Done()
			for event := range eventChan {
				allEvents = append(allEvents, event)
			}
		}()

		options.Params.ChatHistory = newTestChatHistoryWithMessages(messages)
		response, err := provider.Stream(ctx, options, eventChan)
		close(eventChan)
		wg2.Wait()

		if err != nil {
			t.Fatalf("Stream returned an error: %v", err)
		}

		if response == nil {
			t.Fatal("Stream returned a nil response")
		}

		if len(allEvents) == 0 {
			t.Error("No events received")
		}

		t.Logf("Response output content blocks (multi-turn): %d", len(response.Output.Content))
		t.Logf("Usage (multi-turn): InputTokens=%d, OutputTokens=%d", response.Usage.InputTokens, response.Usage.OutputTokens)

		var hasTextContent bool
		for _, block := range response.Output.Content {
			if block.Type == ContentBlockTypeText && block.Text != "" {
				hasTextContent = true
				break
			} else {
				t.Logf("Output Block: %s", utils.PanicJSON(block))
			}
		}

		if !hasTextContent {
			t.Error("Response content is empty after providing tool results")
		}

		assert.NotNil(t, response.Usage, "Usage field should not be nil on multi-turn")
		assert.Greater(t, response.Usage.InputTokens, 0, "InputTokens should be greater than 0 on multi-turn")
		assert.Greater(t, response.Usage.OutputTokens, 0, "OutputTokens should be greater than 0 on multi-turn")
	})
}
