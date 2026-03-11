package llm

import (
	"reflect"
	"testing"

	"github.com/invopop/jsonschema"
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
