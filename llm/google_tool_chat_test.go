package llm

import (
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
			got := googleFromChatMessages(tt.messages)
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
		name   string
		result *genai.GenerateContentResponse
		want   *ChatMessageDelta
	}{
		{
			name:   "nil response",
			result: nil,
			want:   nil,
		},
		{
			name: "text content",
			result: &genai.GenerateContentResponse{
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
			want: &ChatMessageDelta{
				Content: "Hello world",
			},
		},
		{
			name: "thinking content",
			result: &genai.GenerateContentResponse{
				Candidates: []*genai.Candidate{
					{
						Content: &genai.Content{
							Parts: []*genai.Part{
								{Text: "processing", Thought: true},
							},
						},
					},
				},
			},
			want: &ChatMessageDelta{
				Content: "<thinking>processing</thinking>",
			},
		},
		{
			name: "thinking in middle",
			result: &genai.GenerateContentResponse{
				Candidates: []*genai.Candidate{
					{
						Content: &genai.Content{
							Parts: []*genai.Part{
								{Text: "before"},
								{Text: "processing", Thought: true},
								{Text: "after"},
							},
						},
					},
				},
			},
			want: &ChatMessageDelta{
				Content: "before<thinking>processing</thinking>after",
			},
		},
		{
			name: "function call",
			result: &genai.GenerateContentResponse{
				Candidates: []*genai.Candidate{
					{
						Content: &genai.Content{
							Parts: []*genai.Part{
								{
									FunctionCall: &genai.FunctionCall{
										Name: "test_function",
										Args: map[string]any{
											"param1": "value1",
											"param2": 42,
										},
									},
								},
							},
						},
					},
				},
			},
			want: &ChatMessageDelta{
				ToolCalls: []ToolCall{
					{
						Name:      "test_function",
						Arguments: `{"param1":"value1","param2":42}`,
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := googleToChatMessageDelta(tt.result)
			assert.Equal(t, tt.want, got)
		})
	}
}
