package llm

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"sidekick/common"
	"sidekick/secret_manager"
	"sidekick/utils"
	"strings"
	"testing"

	"github.com/invopop/jsonschema"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/stretchr/testify/assert"
	orderedmap "github.com/wk8/go-ordered-map/v2"
	"google.golang.org/genai"
)

func TestGoogleFromChatMessages(t *testing.T) {
	tests := []struct {
		name     string
		messages []ChatMessage
		want     []*genai.Content
	}{
		{
			name: "regular message",
			messages: []ChatMessage{
				{Content: "Hello", Role: "user"},
				{Content: "world", Role: "assistant"},
			},
			want: []*genai.Content{
				{
					Role: "user",
					Parts: []*genai.Part{
						{Text: "Hello"},
					},
				},
				{
					Role: "model",
					Parts: []*genai.Part{
						{Text: "world"},
					},
				},
			},
		},
		{
			name: "thinking message",
			messages: []ChatMessage{
				{Content: "<thinking>processing request</thinking>", Role: "assistant"},
			},
			want: []*genai.Content{
				{
					Role: "model",
					Parts: []*genai.Part{
						{Text: "processing request", Thought: true},
					},
				},
			},
		},
		{
			name: "thinking in middle",
			messages: []ChatMessage{
				{Content: "Before<thinking>processing request</thinking>After", Role: "assistant"},
			},
			want: []*genai.Content{
				{
					Role: "model",
					Parts: []*genai.Part{
						{Text: "Before"},
						{Text: "processing request", Thought: true},
						{Text: "After"},
					},
				},
			},
		},
		{
			name: "mixed consecutive messages from assistant",
			messages: []ChatMessage{
				{Content: "Hello", Role: "assistant"},
				{Content: "<thinking>thinking about response</thinking>", Role: "assistant"},
				{Content: "World", Role: "assistant"},
			},
			want: []*genai.Content{
				{
					Role: "model",
					Parts: []*genai.Part{
						{Text: "Hello"},
						{Text: "thinking about response", Thought: true},
						{Text: "World"},
					},
				},
			},
		},
		{
			name: "mixed consecutive messages from multiple roles",
			messages: []ChatMessage{
				{Content: "Hello", Role: "user"},
				{Content: "<thinking>thinking about response</thinking>", Role: "assistant"},
				{Content: "World", Role: "assistant"},
			},
			want: []*genai.Content{
				{
					Role: "user",
					Parts: []*genai.Part{
						{Text: "Hello"},
					},
				},
				{
					Role: "model",
					Parts: []*genai.Part{
						{Text: "thinking about response", Thought: true},
						{Text: "World"},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := googleFromChatMessages(tt.messages, false)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestGoogleFromChatMessagesReasoningModel(t *testing.T) {
	tests := []struct {
		name             string
		messages         []ChatMessage
		isReasoningModel bool
		want             []*genai.Content
	}{
		{
			name: "tool call with empty signature on reasoning model",
			messages: []ChatMessage{
				{
					Role: "assistant",
					ToolCalls: []ToolCall{
						{Id: "call1", Name: "test_tool", Arguments: `{"arg": "value"}`},
					},
				},
			},
			isReasoningModel: true,
			want: []*genai.Content{
				{
					Role: "model",
					Parts: []*genai.Part{
						{
							FunctionCall: &genai.FunctionCall{
								ID:   "call1",
								Name: "test_tool",
								Args: map[string]any{"arg": "value"},
							},
							ThoughtSignature: []byte("skip_thought_signature_validator"),
						},
					},
				},
			},
		},
		{
			name: "tool call with existing signature on reasoning model",
			messages: []ChatMessage{
				{
					Role: "assistant",
					ToolCalls: []ToolCall{
						{Id: "call1", Name: "test_tool", Arguments: `{"arg": "value"}`, Signature: []byte("existing_signature")},
					},
				},
			},
			isReasoningModel: true,
			want: []*genai.Content{
				{
					Role: "model",
					Parts: []*genai.Part{
						{
							FunctionCall: &genai.FunctionCall{
								ID:   "call1",
								Name: "test_tool",
								Args: map[string]any{"arg": "value"},
							},
							ThoughtSignature: []byte("existing_signature"),
						},
					},
				},
			},
		},
		{
			name: "tool call with empty signature on non-reasoning model",
			messages: []ChatMessage{
				{
					Role: "assistant",
					ToolCalls: []ToolCall{
						{Id: "call1", Name: "test_tool", Arguments: `{"arg": "value"}`},
					},
				},
			},
			isReasoningModel: false,
			want: []*genai.Content{
				{
					Role: "model",
					Parts: []*genai.Part{
						{
							FunctionCall: &genai.FunctionCall{
								ID:   "call1",
								Name: "test_tool",
								Args: map[string]any{"arg": "value"},
							},
							ThoughtSignature: nil,
						},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := googleFromChatMessages(tt.messages, tt.isReasoningModel)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestGoogleFromTools(t *testing.T) {
	tests := []struct {
		name  string
		tools []*Tool
		want  []*genai.Tool
	}{
		{
			name:  "nil tools",
			tools: nil,
			want:  nil,
		},
		{
			name: "simple tool",
			tools: []*Tool{
				{
					Name:        "test",
					Description: "test function",
					Parameters: &jsonschema.Schema{
						Type:     "object",
						Required: []string{"field1"},
						Properties: orderedmap.New[string, *jsonschema.Schema](
							orderedmap.WithInitialData(orderedmap.Pair[string, *jsonschema.Schema]{
								Key: "field1",
								Value: &jsonschema.Schema{
									Type:        "string",
									Description: "description of field1",
								},
							}),
						),
					},
				},
			},
			want: []*genai.Tool{
				{
					FunctionDeclarations: []*genai.FunctionDeclaration{
						{
							Name:        "test",
							Description: "test function",
							Parameters: &genai.Schema{
								Type:     "object",
								Required: []string{"field1"},
								Properties: map[string]*genai.Schema{
									"field1": &genai.Schema{
										Type:        "string",
										Description: "description of field1",
									},
								},
							},
						},
					},
				},
			},
		},
		{
			name: "enum field",
			tools: []*Tool{
				{
					Name:        "test",
					Description: "test function",
					Parameters: &jsonschema.Schema{
						Type:     "object",
						Required: []string{"field1"},
						Properties: orderedmap.New[string, *jsonschema.Schema](
							orderedmap.WithInitialData(orderedmap.Pair[string, *jsonschema.Schema]{
								Key: "field1",
								Value: &jsonschema.Schema{
									Type: "string",
									Enum: []any{"value1", "value2"},
								},
							}),
						),
					},
				},
			},
			want: []*genai.Tool{
				{
					FunctionDeclarations: []*genai.FunctionDeclaration{
						{
							Name:        "test",
							Description: "test function",
							Parameters: &genai.Schema{
								Type:     "object",
								Required: []string{"field1"},
								Properties: map[string]*genai.Schema{
									"field1": &genai.Schema{
										Type: "string",
										Enum: []string{"value1", "value2"},
									},
								},
							},
						},
					},
				},
			},
		},
		{
			name: "array of strings",
			tools: []*Tool{
				{
					Name:        "test",
					Description: "test function",
					Parameters: &jsonschema.Schema{
						Type:     "object",
						Required: []string{"field1"},
						Properties: orderedmap.New[string, *jsonschema.Schema](
							orderedmap.WithInitialData(orderedmap.Pair[string, *jsonschema.Schema]{
								Key: "field1",
								Value: &jsonschema.Schema{
									Type:        "array",
									Description: "an array of strings",
									Items: &jsonschema.Schema{
										Type: "string",
									},
								},
							}),
						),
					},
				},
			},
			want: []*genai.Tool{
				{
					FunctionDeclarations: []*genai.FunctionDeclaration{
						{
							Name:        "test",
							Description: "test function",
							Parameters: &genai.Schema{
								Type:     "object",
								Required: []string{"field1"},
								Properties: map[string]*genai.Schema{
									"field1": &genai.Schema{
										Type:        "array",
										Description: "an array of strings",
										Items: &genai.Schema{
											Type: "string",
										},
									},
								},
							},
						},
					},
				},
			},
		},
		{
			name: "array of objects",
			tools: []*Tool{
				{
					Name:        "test",
					Description: "test function",
					Parameters: &jsonschema.Schema{
						Type:     "object",
						Required: []string{"field1"},
						Properties: orderedmap.New[string, *jsonschema.Schema](
							orderedmap.WithInitialData(orderedmap.Pair[string, *jsonschema.Schema]{
								Key: "field1",
								Value: &jsonschema.Schema{
									Type:        "array",
									Description: "an array of objects",
									Items: &jsonschema.Schema{
										Type: "object",
										Properties: orderedmap.New[string, *jsonschema.Schema](
											orderedmap.WithInitialData(orderedmap.Pair[string, *jsonschema.Schema]{
												Key: "subfield",
												Value: &jsonschema.Schema{
													Type:        "string",
													Description: "a string field in the object",
												},
											}),
										),
									},
								},
							}),
						),
					},
				},
			},
			want: []*genai.Tool{
				{
					FunctionDeclarations: []*genai.FunctionDeclaration{
						{
							Name:        "test",
							Description: "test function",
							Parameters: &genai.Schema{
								Type:     "object",
								Required: []string{"field1"},
								Properties: map[string]*genai.Schema{
									"field1": &genai.Schema{
										Type:        "array",
										Description: "an array of objects",
										Items: &genai.Schema{
											Type: "object",
											Properties: map[string]*genai.Schema{
												"subfield": &genai.Schema{
													Type:        "string",
													Description: "a string field in the object",
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := googleFromTools(tt.tools)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestGoogleToChatMessageDelta(t *testing.T) {
	tests := []struct {
		name         string
		response     *genai.GenerateContentResponse
		wantDelta    *ChatMessageDelta
		wantProgress *ProgressInfo
	}{
		{
			name:         "nil response",
			response:     nil,
			wantDelta:    nil,
			wantProgress: nil,
		},
		{
			name: "empty candidates",
			response: &genai.GenerateContentResponse{
				Candidates: []*genai.Candidate{},
			},
			wantDelta:    nil,
			wantProgress: nil,
		},
		{
			name: "regular text content",
			response: &genai.GenerateContentResponse{
				Candidates: []*genai.Candidate{
					{
						Content: &genai.Content{
							Parts: []*genai.Part{
								{Text: "Hello world"},
							},
						},
					},
				},
			},
			wantDelta: &ChatMessageDelta{
				Role:    ChatMessageRoleAssistant,
				Content: "Hello world",
			},
			wantProgress: nil,
		},
		{
			name: "thinking content with ** markers",
			response: &genai.GenerateContentResponse{
				Candidates: []*genai.Candidate{
					{
						Content: &genai.Content{
							Parts: []*genai.Part{
								{
									Text:    "**Analyzing code**\nLooking at the implementation\nChecking patterns",
									Thought: true,
								},
							},
						},
					},
				},
			},
			wantDelta: &ChatMessageDelta{
				Role: ChatMessageRoleAssistant,
			},
			wantProgress: &ProgressInfo{
				Title:   "Analyzing code",
				Details: "Looking at the implementation\nChecking patterns",
			},
		},
		{
			name: "thinking content without markers",
			response: &genai.GenerateContentResponse{
				Candidates: []*genai.Candidate{
					{
						Content: &genai.Content{
							Parts: []*genai.Part{
								{
									Text:    "Simple title\nWith details",
									Thought: true,
								},
							},
						},
					},
				},
			},
			wantDelta: &ChatMessageDelta{
				Role: ChatMessageRoleAssistant,
			},
			wantProgress: &ProgressInfo{
				Title:   "Simple title",
				Details: "With details",
			},
		},
		{
			name: "long title truncation",
			response: &genai.GenerateContentResponse{
				Candidates: []*genai.Candidate{
					{
						Content: &genai.Content{
							Parts: []*genai.Part{
								{
									Text:    "This is a very long title that should be truncated because it exceeds the maximum length of 120 characters and we want to ensure it looks good\nWith details",
									Thought: true,
								},
							},
						},
					},
				},
			},
			wantDelta: &ChatMessageDelta{
				Role: ChatMessageRoleAssistant,
			},
			wantProgress: &ProgressInfo{
				Title:   "This is a very long title that should be truncated because it exceeds the maximum length of 120 characters and we want...",
				Details: "With details",
			},
		},
		{
			name: "function call",
			response: &genai.GenerateContentResponse{
				Candidates: []*genai.Candidate{
					{
						Content: &genai.Content{
							Parts: []*genai.Part{
								{
									FunctionCall: &genai.FunctionCall{
										ID:   "123",
										Name: "test",
										Args: map[string]interface{}{"foo": "bar"},
									},
								},
							},
						},
					},
				},
			},
			wantDelta: &ChatMessageDelta{
				Role: ChatMessageRoleAssistant,
				ToolCalls: []ToolCall{
					{
						Id:        "123",
						Name:      "test",
						Arguments: `{"foo":"bar"}`,
					},
				},
			},
			wantProgress: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			delta, progress := googleToChatMessageDelta(tt.response)
			if !reflect.DeepEqual(delta, tt.wantDelta) {
				t.Errorf("googleToChatMessageDelta() delta = %v, want %v", delta, tt.wantDelta)
			}
			if !reflect.DeepEqual(progress, tt.wantProgress) {
				t.Errorf("googleToChatMessageDelta() progress = %v, want %v", progress, tt.wantProgress)
			}
		})
	}
}

func TestGoogleToolChatIntegration(t *testing.T) {
	t.Parallel()
	if os.Getenv("SIDE_INTEGRATION_TEST") != "true" {
		t.Skip("Skipping integration test; SIDE_INTEGRATION_TEST not set")
	}

	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr}).Level(zerolog.DebugLevel)
	ctx := context.Background()
	chat := GoogleToolChat{}

	// Mock tool for testing
	mockTool := &Tool{
		Name:        "get_current_weather",
		Description: "Get the current weather in a given location",
		Parameters:  (&jsonschema.Reflector{DoNotReference: true}).Reflect(&getCurrentWeather{}),
	}

	options := ToolChatOptions{
		Params: ToolChatParams{
			ModelConfig: common.ModelConfig{
				Provider: "google",
				Model:    "gemini-2.5-flash", // need a cheap yet reliable enough model for testing
			},
			Temperature: utils.Ptr(float32(0)),
			Messages: []ChatMessage{
				{Role: ChatMessageRoleUser, Content: "First say hi. After that, then look up what the weather is like in New York in celcius. Let me know, then check London too for me."},
			},
			Tools:      []*Tool{mockTool},
			ToolChoice: common.ToolChoice{Type: common.ToolChoiceTypeAuto},
		},
		Secrets: secret_manager.SecretManagerContainer{
			SecretManager: secret_manager.NewCompositeSecretManager([]secret_manager.SecretManager{
				secret_manager.EnvSecretManager{},
				secret_manager.KeyringSecretManager{},
				secret_manager.LocalConfigSecretManager{},
			}),
		},
	}

	deltaChan := make(chan ChatMessageDelta)
	var allDeltas []ChatMessageDelta

	go func() {
		for delta := range deltaChan {
			allDeltas = append(allDeltas, delta)
		}
	}()

	progressChan := make(chan ProgressInfo, 10)
	defer close(progressChan)

	response, err := chat.ChatStream(ctx, options, deltaChan, progressChan)
	close(deltaChan)

	if err != nil {
		errStr := err.Error()
		if strings.Contains(errStr, "429") || strings.Contains(errStr, "RESOURCE_EXHAUSTED") ||
			strings.Contains(errStr, "quota") || strings.Contains(errStr, "401") ||
			strings.Contains(errStr, "authentication") {
			t.Skipf("Skipping due to API quota/auth issue: %v", err)
		}
		t.Fatalf("ChatStream returned an error: %v", err)
	}

	if response == nil {
		t.Fatal("ChatStream returned a nil response")
	}

	// Check that we received deltas
	if len(allDeltas) == 0 {
		t.Error("No deltas received")
	}

	// Check that the response contains content
	t.Logf("Response content: %s", response.Content)
	if response.Content == "" {
		t.Error("Response content is empty")
	}

	// Check that the response includes a tool call
	if len(response.ToolCalls) == 0 {
		t.Fatal("No tool calls in the response")
	}

	// Verify tool call
	toolCall := response.ToolCalls[0]
	if toolCall.Name != "get_current_weather" {
		t.Errorf("Expected tool call to 'get_current_weather', got '%s'", toolCall.Name)
	}

	t.Logf("Tool call: %+v", toolCall)
	t.Logf("Usage: InputTokens=%d, OutputTokens=%d", response.Usage.InputTokens, response.Usage.OutputTokens)

	// Parse tool call arguments
	var args map[string]string
	err = json.Unmarshal([]byte(toolCall.Arguments), &args)
	if err != nil {
		t.Fatalf("Failed to parse tool call arguments: %v", err)
	}

	// Check tool call arguments
	if !strings.Contains(strings.ToLower(args["location"]), "new york") {
		t.Errorf("Expected location to contain 'New York', got '%s'", args["location"])
	}
	if args["unit"] != "celsius" && args["unit"] != "c" {
		t.Errorf("Expected unit 'celsius' or 'c', got '%s'", args["unit"])
	}

	// Check usage
	assert.NotNil(t, response.Usage, "Usage field should not be nil")
	// Depending on the model and exact prompt, token counts can vary slightly,
	// but they should be positive for a successful interaction.
	assert.Greater(t, response.Usage.InputTokens, 0, "InputTokens should be greater than 0")
	assert.Greater(t, response.Usage.OutputTokens, 0, "OutputTokens should be greater than 0")

	// check multi-turn works
	t.Run("MultiTurn", func(t *testing.T) {
		options.Params.Messages = append(options.Params.Messages, response.ChatMessage)
		options.Params.Messages = append(options.Params.Messages, ChatMessage{
			Role:       ChatMessageRoleTool,
			Content:    "25",
			ToolCallId: toolCall.Id,
			Name:       toolCall.Name,
			IsError:    false,
		})

		deltaChan := make(chan ChatMessageDelta)
		var allDeltas []ChatMessageDelta

		go func() {
			for delta := range deltaChan {
				allDeltas = append(allDeltas, delta)
			}
		}()

		progressChan := make(chan ProgressInfo, 10)
		defer close(progressChan)

		response, err := chat.ChatStream(ctx, options, deltaChan, progressChan)
		close(deltaChan)

		if err != nil {
			errStr := err.Error()
			if strings.Contains(errStr, "429") || strings.Contains(errStr, "RESOURCE_EXHAUSTED") ||
				strings.Contains(errStr, "quota") || strings.Contains(errStr, "401") ||
				strings.Contains(errStr, "authentication") {
				t.Skipf("Skipping due to API quota/auth issue: %v", err)
			}
			t.Fatalf("ChatStream returned an error: %v", err)
		}

		if response == nil {
			t.Fatal("ChatStream returned a nil response")
		}

		// Check that we received deltas
		if len(allDeltas) == 0 {
			t.Error("No deltas received")
		}

		// Check that the response contains content
		// Note: not needed, and the test model doesn't provide content typically for this turn
		//if response.Content == "" {
		//	t.Error("Response content is empty")
		//}
		t.Logf("Response content: %s", response.Content)
		t.Logf("Usage (multi-turn): InputTokens=%d, OutputTokens=%d", response.Usage.InputTokens, response.Usage.OutputTokens)

		// Check that the response includes a tool call
		if len(response.ToolCalls) == 0 {
			t.Fatal("No tool calls in the response")
		}

		// Verify tool call
		toolCall := response.ToolCalls[0]
		if toolCall.Name != "get_current_weather" {
			t.Errorf("Expected tool call to 'get_current_weather', got '%s'", toolCall.Name)
		}

		t.Logf("Tool call: %+v", toolCall)

		// Parse tool call arguments
		var args map[string]string
		err = json.Unmarshal([]byte(toolCall.Arguments), &args)
		if err != nil {
			t.Fatalf("Failed to parse tool call arguments: %v", err)
		}

		// Check tool call arguments
		if !strings.Contains(strings.ToLower(args["location"]), "london") {
			t.Errorf("Expected location to contain 'london', got '%s'", args["location"])
		}
		if args["unit"] != "celsius" && args["unit"] != "c" {
			t.Errorf("Expected unit 'celsius' or 'c', got '%s'", args["unit"])
		}

		// Check usage for multi-turn
		assert.NotNil(t, response.Usage, "Usage field should not be nil in multi-turn")
		assert.Greater(t, response.Usage.InputTokens, 0, "InputTokens should be greater than 0 in multi-turn")
		assert.Greater(t, response.Usage.OutputTokens, 0, "OutputTokens should be greater than 0 in multi-turn")
	})
}
