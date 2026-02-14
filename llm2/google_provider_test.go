package llm2

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sidekick/common"
	"sidekick/secret_manager"
	"strings"
	"sync"
	"testing"

	"github.com/invopop/jsonschema"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/stretchr/testify/assert"
	"google.golang.org/genai"
)

func TestGoogleProvider_Unauthorized(t *testing.T) {
	ctx := context.Background()
	mockSecretManager := &secret_manager.MockSecretManager{}
	provider := GoogleProvider{}

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
				Provider: "google",
				Model:    "gemini-2.5-flash",
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
	assert.True(t,
		strings.Contains(errStr, "401") || strings.Contains(errStr, "403") || strings.Contains(errStr, "API_KEY_INVALID"),
		"Expected error to indicate auth failure, got: %s", errStr)
}

func TestGoogleProvider_Integration(t *testing.T) {
	t.Parallel()
	if os.Getenv("SIDE_INTEGRATION_TEST") != "true" {
		t.Skip("Skipping integration test; SIDE_INTEGRATION_TEST not set")
	}

	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr}).Level(zerolog.DebugLevel)
	ctx := context.Background()
	provider := GoogleProvider{}

	fmt.Println("\n=== Google Provider Integration Test ===")

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
					Text: "First say Hi. After that, then look up what the weather is like in New York in celsius.",
				},
			},
		},
	}

	options := Options{
		Params: Params{
			ModelConfig: common.ModelConfig{
				Provider: "google",
				Model:    "gemini-2.5-flash",
			},
			Tools:      []*common.Tool{mockTool},
			ToolChoice: common.ToolChoice{Type: common.ToolChoiceTypeAuto},
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
			fmt.Printf("Event[%d]: type=%s", event.Index, event.Type)
			if event.ContentBlock != nil {
				fmt.Printf(" block_type=%s", event.ContentBlock.Type)
				if event.ContentBlock.ToolUse != nil {
					fmt.Printf(" tool_name=%s args_len=%d", event.ContentBlock.ToolUse.Name, len(event.ContentBlock.ToolUse.Arguments))
				}
			}
			if event.Delta != "" {
				deltaPreview := event.Delta
				if len(deltaPreview) > 50 {
					deltaPreview = deltaPreview[:50] + "..."
				}
				fmt.Printf(" delta=%q", deltaPreview)
			}
			fmt.Println()

			if event.Type == EventBlockStarted && event.ContentBlock != nil && event.ContentBlock.Type == ContentBlockTypeToolUse {
				sawBlockStartedToolUse = true
			}
			if event.Type == EventBlockDone && event.ContentBlock != nil && event.ContentBlock.Type == ContentBlockTypeToolUse {
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
		errStr := err.Error()
		if strings.Contains(errStr, "RESOURCE_EXHAUSTED") || strings.Contains(errStr, "429") {
			t.Skipf("Skipping test due to Google API quota exceeded or rate limited: %v", err)
		}
		t.Fatalf("Stream returned an error: %v", err)
	}

	if response == nil {
		t.Fatal("Stream returned a nil response")
	}

	if len(allEvents) == 0 {
		t.Error("No events received")
	}

	assert.True(t, sawTextDelta, "Expected at least one text_delta event")
	assert.True(t, sawBlockStartedToolUse, "Expected at least one block_started event with tool_use type")

	// Verify non-empty assistant text in the first response
	var firstResponseText string
	for _, block := range response.Output.Content {
		if block.Type == ContentBlockTypeText {
			firstResponseText += block.Text
		}
	}
	assert.NotEmpty(t, firstResponseText, "Expected non-empty assistant text in the first response")

	t.Logf("Response output content blocks: %d", len(response.Output.Content))
	for i, block := range response.Output.Content {
		t.Logf("Block[%d] Type=%s TextLen=%d Signature=%d bytes", i, block.Type, len(block.Text), len(block.Signature))
		if block.Type == ContentBlockTypeReasoning && block.Reasoning != nil {
			t.Logf("  Reasoning: TextLen=%d, Signature=%d bytes", len(block.Reasoning.Text), len(block.Reasoning.Signature))
		}
	}

	var foundToolUse bool
	for _, block := range response.Output.Content {
		if block.Type == ContentBlockTypeToolUse && block.ToolUse != nil {
			foundToolUse = true
			assert.Equal(t, "get_current_weather", block.ToolUse.Name)
			assert.NotEmpty(t, block.ToolUse.Arguments, "Tool use arguments should not be empty")
			t.Logf("Found tool_use block: %+v", block.ToolUse)
		}
	}
	assert.True(t, foundToolUse, "Expected response.Output.Content to include a tool_use block for get_current_weather")

	assert.NotEmpty(t, response.StopReason, "StopReason should not be empty")
	assert.Greater(t, response.Usage.InputTokens, 0, "InputTokens should be greater than 0")
	assert.Greater(t, response.Usage.OutputTokens, 0, "OutputTokens should be greater than 0")

	t.Logf("Usage: InputTokens=%d, OutputTokens=%d", response.Usage.InputTokens, response.Usage.OutputTokens)
	t.Logf("Model: %s, Provider: %s", response.Model, response.Provider)
	t.Logf("StopReason: %s", response.StopReason)

	// Multi-turn: feed tool results back
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
								Name:       block.ToolUse.Name,
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

		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			for event := range eventChan {
				allEvents = append(allEvents, event)
			}
		}()

		options.Params.ChatHistory = newTestChatHistoryWithMessages(messages)
		response, err := provider.Stream(ctx, options, eventChan)
		close(eventChan)
		wg.Wait()

		if err != nil {
			errStr := err.Error()
			if strings.Contains(errStr, "429") || strings.Contains(errStr, "RESOURCE_EXHAUSTED") ||
				strings.Contains(errStr, "quota") || strings.Contains(errStr, "401") ||
				strings.Contains(errStr, "authentication") {
				t.Skipf("Skipping due to API quota/auth issue: %v", err)
			}
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

		var textContent string
		for _, block := range response.Output.Content {
			if block.Type == ContentBlockTypeText && block.Text != "" {
				textContent += block.Text
			}
		}

		assert.NotEmpty(t, textContent, "Expected non-empty text response after providing tool results")
		// The tool result was "25" (degrees), so the response should reference the temperature
		assert.True(t,
			strings.Contains(textContent, "25") || strings.Contains(strings.ToLower(textContent), "degree") || strings.Contains(strings.ToLower(textContent), "celsius"),
			"Expected response to incorporate tool result (temperature 25), got: %s", textContent)

		assert.Greater(t, response.Usage.InputTokens, 0, "InputTokens should be greater than 0 on multi-turn")
		assert.Greater(t, response.Usage.OutputTokens, 0, "OutputTokens should be greater than 0 on multi-turn")
	})

	// Reasoning test: verify reasoning blocks are produced for reasoning models
	t.Run("Reasoning", func(t *testing.T) {
		reasoningMessages := []Message{
			{
				Role: RoleUser,
				Content: []ContentBlock{
					{
						Type: ContentBlockTypeText,
						Text: "What is 127 * 349? Think step by step.",
					},
				},
			},
		}

		reasoningOptions := Options{
			Params: Params{
				ModelConfig: common.ModelConfig{
					Provider:        "google",
					Model:           "gemini-3-flash-preview",
					ReasoningEffort: "low",
				},
			},
			Secrets: options.Secrets,
		}

		reasoningOptions.Params.ChatHistory = newTestChatHistoryWithMessages(reasoningMessages)

		eventChan := make(chan Event, 100)
		var reasoningEvents []Event
		var sawReasoning bool

		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			for event := range eventChan {
				reasoningEvents = append(reasoningEvents, event)
				if event.Type == EventBlockStarted && event.ContentBlock != nil && event.ContentBlock.Type == ContentBlockTypeReasoning {
					sawReasoning = true
				}
			}
		}()

		fmt.Println("\n=== Reasoning Test (gemini-3-flash-preview) ===")
		response, err := provider.Stream(ctx, reasoningOptions, eventChan)
		close(eventChan)
		wg.Wait()

		if err != nil {
			errStr := err.Error()
			if strings.Contains(errStr, "429") || strings.Contains(errStr, "RESOURCE_EXHAUSTED") {
				t.Skipf("Skipping due to quota: %v", err)
			}
			t.Fatalf("Stream returned an error: %v", err)
		}

		assert.NotNil(t, response)
		assert.True(t, sawReasoning, "Expected at least one reasoning block event")

		var foundReasoning bool
		for _, block := range response.Output.Content {
			if block.Type == ContentBlockTypeReasoning {
				foundReasoning = true
				assert.NotEmpty(t, block.Reasoning.Text, "Reasoning text should not be empty")
				fmt.Printf("DEBUG: Reasoning block - Text length: %d, Signature length: %d\n", len(block.Reasoning.Text), len(block.Reasoning.Signature))
				if len(block.Reasoning.Signature) > 0 {
					fmt.Printf("DEBUG: Reasoning Signature (first 100 bytes): %v\n", block.Reasoning.Signature[:min(100, len(block.Reasoning.Signature))])
				}
				t.Logf("Reasoning text length: %d", len(block.Reasoning.Text))
			}
		}
		assert.True(t, foundReasoning, "Expected a reasoning block in the output")

		// Debug: print all content blocks with signatures
		fmt.Printf("\n=== All Content Blocks (total: %d) ===\n", len(response.Output.Content))
		for i, block := range response.Output.Content {
			fmt.Printf("Block[%d] Type=%s\n", i, block.Type)
			if block.Type == ContentBlockTypeText {
				textPreview := block.Text
				if len(textPreview) > 100 {
					textPreview = textPreview[:100] + "..."
				}
				fmt.Printf("  Text=%q\n", textPreview)
				fmt.Printf("  TextLen=<varies> Signature=%d bytes\n", len(block.Signature))
			} else if block.Type == ContentBlockTypeReasoning && block.Reasoning != nil {
				reasoningPreview := block.Reasoning.Text
				if len(reasoningPreview) > 100 {
					reasoningPreview = reasoningPreview[:100] + "..."
				}
				fmt.Printf("  ReasoningText=%q\n", reasoningPreview)
				fmt.Printf("  ReasoningTextLen=%d ReasoningSignature=%d bytes\n", len(block.Reasoning.Text), len(block.Reasoning.Signature))
			} else if block.Type == ContentBlockTypeToolUse && block.ToolUse != nil {
				argsPreview := block.ToolUse.Arguments
				if len(argsPreview) > 100 {
					argsPreview = argsPreview[:100] + "..."
				}
				fmt.Printf("  ToolName=%s Args=%s\n", block.ToolUse.Name, argsPreview)
				fmt.Printf("  Signature=%d bytes\n", len(block.ToolUse.Signature))
			}
		}

		var foundText bool
		for _, block := range response.Output.Content {
			if block.Type == ContentBlockTypeText && block.Text != "" {
				foundText = true
				break
			}
		}
		assert.True(t, foundText, "Expected a text block with the answer")
	})
}

func TestGoogleProvider_ImageIntegration(t *testing.T) {
	t.Parallel()
	if os.Getenv("SIDE_INTEGRATION_TEST") != "true" {
		t.Skip("Skipping integration test; SIDE_INTEGRATION_TEST not set")
	}

	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr}).Level(zerolog.DebugLevel)
	ctx := context.Background()
	provider := GoogleProvider{}

	expectedText, dataURL := GenerateVisionTestImage(6)
	t.Logf("Generated vision test image with text: %q", expectedText)

	messages := []Message{
		{
			Role: RoleUser,
			Content: []ContentBlock{
				{
					Type:  ContentBlockTypeImage,
					Image: &ImageRef{Url: dataURL},
				},
				{
					Type: ContentBlockTypeText,
					Text: "What text is written in this image? The text consists only of uppercase ASCII letters (A-Z, no O or I) and digits (2-9). Reply with ONLY the exact text, nothing else.",
				},
			},
		},
	}

	options := Options{
		Params: Params{
			ModelConfig: common.ModelConfig{
				Provider: "google",
				Model:    "gemini-2.5-flash",
			},
		},
		Secrets: secret_manager.SecretManagerContainer{
			SecretManager: secret_manager.NewCompositeSecretManager([]secret_manager.SecretManager{
				&secret_manager.EnvSecretManager{},
				&secret_manager.KeyringSecretManager{},
				&secret_manager.LocalConfigSecretManager{},
			}),
		},
	}

	options.Params.ChatHistory = newTestChatHistoryWithMessages(messages)

	eventChan := make(chan Event, 100)
	var fullText strings.Builder
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		for event := range eventChan {
			if event.Type == EventTextDelta {
				fullText.WriteString(event.Delta)
			}
		}
	}()

	response, err := provider.Stream(ctx, options, eventChan)
	close(eventChan)
	wg.Wait()

	if err != nil {
		errStr := err.Error()
		if strings.Contains(errStr, "RESOURCE_EXHAUSTED") || strings.Contains(errStr, "429") || strings.Contains(errStr, "quota") {
			t.Skipf("Skipping test due to Google API quota/rate limit: %v", err)
		}
		t.Fatalf("Stream returned an error: %v", err)
	}

	assert.NotNil(t, response)
	responseText := strings.TrimSpace(fullText.String())
	t.Logf("Model response: %q", responseText)
	assert.True(t, VisionTestFuzzyMatch(expectedText, responseText),
		"Expected model to read %q from the image, got %q", expectedText, responseText)
}

func TestGoogleFromLlm2Messages(t *testing.T) {
	t.Parallel()

	t.Run("basic text messages", func(t *testing.T) {
		t.Parallel()
		messages := []Message{
			{
				Role: RoleUser,
				Content: []ContentBlock{
					{Type: ContentBlockTypeText, Text: "Hello"},
				},
			},
			{
				Role: RoleAssistant,
				Content: []ContentBlock{
					{Type: ContentBlockTypeText, Text: "Hi there"},
				},
			},
		}

		contents, err := googleFromLlm2Messages(messages, false)
		assert.NoError(t, err)
		assert.Len(t, contents, 2)
		assert.Equal(t, "user", contents[0].Role)
		assert.Equal(t, "model", contents[1].Role)
		assert.Equal(t, "Hello", contents[0].Parts[0].Text)
		assert.Equal(t, "Hi there", contents[1].Parts[0].Text)
	})

	t.Run("tool result messages", func(t *testing.T) {
		t.Parallel()
		messages := []Message{
			{
				Role: RoleUser,
				Content: []ContentBlock{
					{
						Type: ContentBlockTypeToolResult,
						ToolResult: &ToolResultBlock{
							ToolCallId: "call-123",
							Name:       "get_weather",
							Text:       "25 degrees",
							IsError:    false,
						},
					},
				},
			},
		}

		contents, err := googleFromLlm2Messages(messages, false)
		assert.NoError(t, err)
		assert.Len(t, contents, 1)
		assert.Equal(t, "user", contents[0].Role)
		assert.NotNil(t, contents[0].Parts[0].FunctionResponse)
		assert.Equal(t, "call-123", contents[0].Parts[0].FunctionResponse.ID)
		assert.Equal(t, "get_weather", contents[0].Parts[0].FunctionResponse.Name)
	})

	t.Run("tool use messages", func(t *testing.T) {
		t.Parallel()
		messages := []Message{
			{
				Role: RoleAssistant,
				Content: []ContentBlock{
					{
						Type: ContentBlockTypeToolUse,
						ToolUse: &ToolUseBlock{
							Id:        "call-456",
							Name:      "get_weather",
							Arguments: `{"location":"NYC"}`,
						},
					},
				},
			},
		}

		contents, err := googleFromLlm2Messages(messages, false)
		assert.NoError(t, err)
		assert.Len(t, contents, 1)
		assert.Equal(t, "model", contents[0].Role)
		assert.NotNil(t, contents[0].Parts[0].FunctionCall)
		assert.Equal(t, "call-456", contents[0].Parts[0].FunctionCall.ID)
		assert.Equal(t, "get_weather", contents[0].Parts[0].FunctionCall.Name)
		assert.Equal(t, "NYC", contents[0].Parts[0].FunctionCall.Args["location"])
	})

	t.Run("reasoning blocks", func(t *testing.T) {
		t.Parallel()
		messages := []Message{
			{
				Role: RoleAssistant,
				Content: []ContentBlock{
					{
						Type: ContentBlockTypeReasoning,
						Reasoning: &ReasoningBlock{
							Text: "Let me think about this...",
						},
					},
					{
						Type: ContentBlockTypeText,
						Text: "The answer is 42.",
					},
				},
			},
		}

		contents, err := googleFromLlm2Messages(messages, false)
		assert.NoError(t, err)
		assert.Len(t, contents, 1)
		assert.Len(t, contents[0].Parts, 2)
		assert.True(t, contents[0].Parts[0].Thought)
		assert.Equal(t, "Let me think about this...", contents[0].Parts[0].Text)
		assert.False(t, contents[0].Parts[1].Thought)
		assert.Equal(t, "The answer is 42.", contents[0].Parts[1].Text)
	})

	t.Run("tool use with reasoning model adds thought signature", func(t *testing.T) {
		t.Parallel()
		messages := []Message{
			{
				Role: RoleAssistant,
				Content: []ContentBlock{
					{
						Type: ContentBlockTypeToolUse,
						ToolUse: &ToolUseBlock{
							Id:        "call-789",
							Name:      "test_tool",
							Arguments: `{}`,
						},
					},
				},
			},
		}

		contents, err := googleFromLlm2Messages(messages, true)
		assert.NoError(t, err)
		assert.Len(t, contents, 1)
		assert.Equal(t, []byte("skip_thought_signature_validator"), contents[0].Parts[0].ThoughtSignature)
	})

	t.Run("error tool result", func(t *testing.T) {
		t.Parallel()
		messages := []Message{
			{
				Role: RoleUser,
				Content: []ContentBlock{
					{
						Type: ContentBlockTypeToolResult,
						ToolResult: &ToolResultBlock{
							ToolCallId: "call-err",
							Name:       "failing_tool",
							Text:       "something went wrong",
							IsError:    true,
						},
					},
				},
			},
		}

		contents, err := googleFromLlm2Messages(messages, false)
		assert.NoError(t, err)
		assert.Len(t, contents, 1)
		resp := contents[0].Parts[0].FunctionResponse.Response
		assert.Equal(t, "something went wrong", resp["error"])
	})

	t.Run("reasoning blocks with signature preserved", func(t *testing.T) {
		t.Parallel()
		messages := []Message{
			{
				Role: RoleAssistant,
				Content: []ContentBlock{
					{
						Type: ContentBlockTypeReasoning,
						Reasoning: &ReasoningBlock{
							Text:      "First thought",
							Signature: []byte("sig-abc"),
						},
					},
					{
						Type: ContentBlockTypeReasoning,
						Reasoning: &ReasoningBlock{
							Text: "Second thought without signature",
						},
					},
				},
			},
		}

		contents, err := googleFromLlm2Messages(messages, false)
		assert.NoError(t, err)
		assert.Len(t, contents, 1)
		assert.Len(t, contents[0].Parts, 2)
		assert.True(t, contents[0].Parts[0].Thought)
		assert.Equal(t, "First thought", contents[0].Parts[0].Text)
		assert.Equal(t, []byte("sig-abc"), contents[0].Parts[0].ThoughtSignature)
		assert.True(t, contents[0].Parts[1].Thought)
		assert.Equal(t, "Second thought without signature", contents[0].Parts[1].Text)
		assert.Nil(t, contents[0].Parts[1].ThoughtSignature)
	})

	t.Run("text blocks with signature preserved", func(t *testing.T) {
		t.Parallel()
		messages := []Message{
			{
				Role: RoleAssistant,
				Content: []ContentBlock{
					{
						Type:      ContentBlockTypeText,
						Text:      "Text with signature",
						Signature: []byte("text-sig-123"),
					},
					{
						Type: ContentBlockTypeText,
						Text: "Text without signature",
					},
				},
			},
		}

		contents, err := googleFromLlm2Messages(messages, false)
		assert.NoError(t, err)
		assert.Len(t, contents, 1)
		assert.Len(t, contents[0].Parts, 2)
		assert.False(t, contents[0].Parts[0].Thought)
		assert.Equal(t, "Text with signature", contents[0].Parts[0].Text)
		assert.Equal(t, []byte("text-sig-123"), contents[0].Parts[0].ThoughtSignature)
		assert.False(t, contents[0].Parts[1].Thought)
		assert.Equal(t, "Text without signature", contents[0].Parts[1].Text)
		assert.Nil(t, contents[0].Parts[1].ThoughtSignature)
	})
}

func TestGoogleFromLlm2ToolChoice(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		toolChoice    common.ToolChoice
		expectedMode  string
		expectedNames []string
		expectError   bool
	}{
		{
			name:         "auto",
			toolChoice:   common.ToolChoice{Type: common.ToolChoiceTypeAuto},
			expectedMode: "AUTO",
		},
		{
			name:         "unspecified defaults to auto",
			toolChoice:   common.ToolChoice{Type: common.ToolChoiceTypeUnspecified},
			expectedMode: "AUTO",
		},
		{
			name:         "required",
			toolChoice:   common.ToolChoice{Type: common.ToolChoiceTypeRequired},
			expectedMode: "ANY",
		},
		{
			name:          "specific tool",
			toolChoice:    common.ToolChoice{Type: common.ToolChoiceTypeTool, Name: "my_tool"},
			expectedMode:  "ANY",
			expectedNames: []string{"my_tool"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			result, err := googleFromLlm2ToolChoice(tc.toolChoice)
			if tc.expectError {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, genai.FunctionCallingConfigMode(tc.expectedMode), result.FunctionCallingConfig.Mode)
			if tc.expectedNames != nil {
				assert.Equal(t, tc.expectedNames, result.FunctionCallingConfig.AllowedFunctionNames)
			}
		})
	}
}

func TestGoogleFromLlm2Tools(t *testing.T) {
	t.Parallel()

	tools := []*common.Tool{
		{
			Name:        "get_weather",
			Description: "Get weather for a location",
			Parameters:  (&jsonschema.Reflector{DoNotReference: true}).Reflect(&getCurrentWeather{}),
		},
	}

	result := googleFromLlm2Tools(tools)
	assert.Len(t, result, 1)
	assert.Len(t, result[0].FunctionDeclarations, 1)
	assert.Equal(t, "get_weather", result[0].FunctionDeclarations[0].Name)
	assert.Equal(t, "Get weather for a location", result[0].FunctionDeclarations[0].Description)
}

func TestGoogleResultToEvents(t *testing.T) {
	t.Parallel()

	t.Run("text part", func(t *testing.T) {
		t.Parallel()
		state := &googleStreamState{}
		result := &genai.GenerateContentResponse{
			Candidates: []*genai.Candidate{
				{
					Content: &genai.Content{
						Parts: []*genai.Part{
							{Text: "Hello world"},
						},
					},
				},
			},
		}

		events := googleResultToEvents(result, state)
		// Text parts emit: block_started + text_delta (block_done comes from finalize)
		assert.Len(t, events, 2)
		assert.Equal(t, EventBlockStarted, events[0].Type)
		assert.Equal(t, ContentBlockTypeText, events[0].ContentBlock.Type)
		assert.Equal(t, EventTextDelta, events[1].Type)
		assert.Equal(t, "Hello world", events[1].Delta)
		assert.Equal(t, 1, state.nextBlockIndex)
		assert.True(t, state.blockStarted)

		// Finalize should emit block_done
		finalEvents := googleFinalizeStream(state)
		assert.Len(t, finalEvents, 1)
		assert.Equal(t, EventBlockDone, finalEvents[0].Type)
	})

	t.Run("thought part", func(t *testing.T) {
		t.Parallel()
		state := &googleStreamState{}
		result := &genai.GenerateContentResponse{
			Candidates: []*genai.Candidate{
				{
					Content: &genai.Content{
						Parts: []*genai.Part{
							{Text: "Let me think...", Thought: true},
						},
					},
				},
			},
		}

		events := googleResultToEvents(result, state)
		// Thought parts emit: block_started + text_delta (block_done comes from finalize)
		assert.Len(t, events, 2)
		assert.Equal(t, EventBlockStarted, events[0].Type)
		assert.Equal(t, ContentBlockTypeReasoning, events[0].ContentBlock.Type)
		assert.NotNil(t, events[0].ContentBlock.Reasoning)
		assert.Equal(t, EventTextDelta, events[1].Type)
		assert.Equal(t, "Let me think...", events[1].Delta)

		// Finalize should emit block_done
		finalEvents := googleFinalizeStream(state)
		assert.Len(t, finalEvents, 1)
		assert.Equal(t, EventBlockDone, finalEvents[0].Type)
	})

	t.Run("function call part", func(t *testing.T) {
		t.Parallel()
		state := &googleStreamState{}
		result := &genai.GenerateContentResponse{
			Candidates: []*genai.Candidate{
				{
					Content: &genai.Content{
						Parts: []*genai.Part{
							{
								FunctionCall: &genai.FunctionCall{
									ID:   "call-1",
									Name: "get_weather",
									Args: map[string]any{"location": "NYC"},
								},
							},
						},
					},
				},
			},
		}

		events := googleResultToEvents(result, state)
		// Function calls emit only block_done with full content (no start/delta)
		assert.Len(t, events, 1)
		assert.Equal(t, EventBlockDone, events[0].Type)
		assert.Equal(t, ContentBlockTypeToolUse, events[0].ContentBlock.Type)
		assert.Equal(t, "call-1", events[0].ContentBlock.ToolUse.Id)
		assert.Equal(t, "get_weather", events[0].ContentBlock.ToolUse.Name)

		var args map[string]any
		err := json.Unmarshal([]byte(events[0].ContentBlock.ToolUse.Arguments), &args)
		assert.NoError(t, err)
		assert.Equal(t, "NYC", args["location"])
	})

	t.Run("multiple text deltas coalesce", func(t *testing.T) {
		t.Parallel()
		state := &googleStreamState{}

		// First chunk
		result1 := &genai.GenerateContentResponse{
			Candidates: []*genai.Candidate{
				{Content: &genai.Content{Parts: []*genai.Part{{Text: "Hello "}}}},
			},
		}
		events1 := googleResultToEvents(result1, state)
		assert.Len(t, events1, 2) // block_started + text_delta

		// Second chunk (same type, should continue same block)
		result2 := &genai.GenerateContentResponse{
			Candidates: []*genai.Candidate{
				{Content: &genai.Content{Parts: []*genai.Part{{Text: "world"}}}},
			},
		}
		events2 := googleResultToEvents(result2, state)
		assert.Len(t, events2, 1) // only text_delta, no new block_started
		assert.Equal(t, EventTextDelta, events2[0].Type)
		assert.Equal(t, "world", events2[0].Delta)
		assert.Equal(t, 0, events2[0].Index) // same block index

		// Only one block was created
		assert.Equal(t, 1, state.nextBlockIndex)
	})

	t.Run("nil result", func(t *testing.T) {
		t.Parallel()
		state := &googleStreamState{}
		events := googleResultToEvents(nil, state)
		assert.Nil(t, events)
	})

	t.Run("empty candidates", func(t *testing.T) {
		t.Parallel()
		state := &googleStreamState{}
		result := &genai.GenerateContentResponse{
			Candidates: []*genai.Candidate{},
		}
		events := googleResultToEvents(result, state)
		assert.Nil(t, events)
	})

	t.Run("parts with signatures are not merged", func(t *testing.T) {
		t.Parallel()
		state := &googleStreamState{}

		// First thought part without signature
		result1 := &genai.GenerateContentResponse{
			Candidates: []*genai.Candidate{
				{Content: &genai.Content{Parts: []*genai.Part{
					{Text: "Thinking part 1", Thought: true},
				}}},
			},
		}
		events1 := googleResultToEvents(result1, state)
		assert.Len(t, events1, 2) // block_started + text_delta

		// Second thought part WITH signature - should NOT merge
		result2 := &genai.GenerateContentResponse{
			Candidates: []*genai.Candidate{
				{Content: &genai.Content{Parts: []*genai.Part{
					{Text: "Thinking part 2", Thought: true, ThoughtSignature: []byte("sig123")},
				}}},
			},
		}
		events2 := googleResultToEvents(result2, state)
		// Should have: block_done (for prev block) + block_started + text_delta
		assert.Len(t, events2, 3)
		assert.Equal(t, EventBlockDone, events2[0].Type)
		assert.Equal(t, 0, events2[0].Index)
		assert.Equal(t, EventBlockStarted, events2[1].Type)
		assert.Equal(t, 1, events2[1].Index)
		assert.Equal(t, []byte("sig123"), events2[1].ContentBlock.Reasoning.Signature)
		assert.Equal(t, EventTextDelta, events2[2].Type)

		// Third thought part without signature - should NOT merge with signed block
		result3 := &genai.GenerateContentResponse{
			Candidates: []*genai.Candidate{
				{Content: &genai.Content{Parts: []*genai.Part{
					{Text: "Thinking part 3", Thought: true},
				}}},
			},
		}
		events3 := googleResultToEvents(result3, state)
		// Should have: block_done (for signed block) + block_started + text_delta
		assert.Len(t, events3, 3)
		assert.Equal(t, EventBlockDone, events3[0].Type)
		assert.Equal(t, 1, events3[0].Index)
		assert.Equal(t, EventBlockStarted, events3[1].Type)
		assert.Equal(t, 2, events3[1].Index)

		// Three separate blocks were created
		assert.Equal(t, 3, state.nextBlockIndex)
	})

	t.Run("text with signature is preserved in accumulated message", func(t *testing.T) {
		t.Parallel()
		state := &googleStreamState{}

		// Text part with signature
		result := &genai.GenerateContentResponse{
			Candidates: []*genai.Candidate{
				{Content: &genai.Content{Parts: []*genai.Part{
					{Text: "Hello with sig", ThoughtSignature: []byte("text-sig-123")},
				}}},
			},
		}
		events := googleResultToEvents(result, state)
		assert.Len(t, events, 2) // block_started + text_delta
		assert.Equal(t, EventBlockStarted, events[0].Type)
		assert.Equal(t, ContentBlockTypeText, events[0].ContentBlock.Type)
		assert.Equal(t, []byte("text-sig-123"), events[0].ContentBlock.Signature)

		// Finalize
		finalEvents := googleFinalizeStream(state)
		allEvents := append(events, finalEvents...)

		// Accumulate and verify signature is preserved
		msg := accumulateGoogleEventsToMessage(allEvents)
		assert.Len(t, msg.Content, 1)
		assert.Equal(t, ContentBlockTypeText, msg.Content[0].Type)
		assert.Equal(t, "Hello with sig", msg.Content[0].Text)
		assert.Equal(t, []byte("text-sig-123"), msg.Content[0].Signature)
	})

	t.Run("reasoning with signature is preserved in accumulated message", func(t *testing.T) {
		t.Parallel()
		state := &googleStreamState{}

		// Reasoning part with signature
		result := &genai.GenerateContentResponse{
			Candidates: []*genai.Candidate{
				{Content: &genai.Content{Parts: []*genai.Part{
					{Text: "Thinking...", Thought: true, ThoughtSignature: []byte("thought-sig-456")},
				}}},
			},
		}
		events := googleResultToEvents(result, state)
		assert.Len(t, events, 2) // block_started + text_delta
		assert.Equal(t, EventBlockStarted, events[0].Type)
		assert.Equal(t, ContentBlockTypeReasoning, events[0].ContentBlock.Type)
		assert.Equal(t, []byte("thought-sig-456"), events[0].ContentBlock.Reasoning.Signature)

		// Finalize
		finalEvents := googleFinalizeStream(state)
		allEvents := append(events, finalEvents...)

		// Accumulate and verify signature is preserved
		msg := accumulateGoogleEventsToMessage(allEvents)
		assert.Len(t, msg.Content, 1)
		assert.Equal(t, ContentBlockTypeReasoning, msg.Content[0].Type)
		assert.Equal(t, "Thinking...", msg.Content[0].Reasoning.Text)
		assert.Equal(t, []byte("thought-sig-456"), msg.Content[0].Reasoning.Signature)
	})
}
