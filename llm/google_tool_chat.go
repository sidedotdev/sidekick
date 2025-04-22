package llm

import (
	"context"
	"errors"
	"fmt"
	"sidekick/utils"
	"strings"

	"github.com/invopop/jsonschema"
	"google.golang.org/genai"
)

const (
	GoogleApiKeySecretName = "GOOGLE_API_KEY"
	GoogleDefaultModel     = "gemini-2.5-pro-exp-03-25"
	thinkingStartTag       = "<thinking>"
	thinkingEndTag         = "</thinking>"
)

type GoogleToolChat struct{}

func NewGoogleToolChat() *GoogleToolChat {
	return &GoogleToolChat{}
}

func (g *GoogleToolChat) ChatStream(ctx context.Context, options ToolChatOptions, deltaChan chan<- ChatMessageDelta) (*ChatMessageResponse, error) {
	apiKey, err := options.Secrets.GetSecret(GoogleApiKeySecretName)
	if err != nil {
		return nil, fmt.Errorf("failed to get Google API key: %w", err)
	}

	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:  apiKey,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create Google client: %w", err)
	}

	model := GoogleDefaultModel
	if options.Params.ModelConfig.Model != "" {
		model = options.Params.ModelConfig.Model
	}

	contents := googleFromChatMessages(options.Params.Messages)

	config := &genai.GenerateContentConfig{
		Tools: googleFromTools(options.Params.Tools),
	}
	if options.Params.Temperature != nil {
		config.Temperature = options.Params.Temperature
	}

	stream := client.Models.GenerateContentStream(ctx, model, contents, config)
	var deltas []ChatMessageDelta

	for result, err := range stream {
		if err != nil {
			return nil, fmt.Errorf("failed to iterate on google tool chat stream: %w", err)
		}
		delta := googleToChatMessageDelta(result)
		if delta != nil {
			deltaChan <- *delta
			deltas = append(deltas, *delta)
		}
	}

	message := stitchDeltasToMessage(deltas, true)
	if message.Role == "" {
		return nil, errors.New("chat message role not found")
	}

	return &ChatMessageResponse{
		ChatMessage: message,
		Model:       model,
		Provider:    "google",
	}, nil
}

func googleFromChatMessages(messages []ChatMessage) []*genai.Content {
	var contents []*genai.Content
	var currentRole string
	var currentParts []*genai.Part

	addContent := func() {
		if len(currentParts) > 0 {
			contents = append(contents, &genai.Content{
				Parts: currentParts,
				Role:  currentRole,
			})
		}
	}

	for _, msg := range messages {
		var role string
		switch msg.Role {
		case ChatMessageRoleUser, ChatMessageRoleSystem, ChatMessageRoleTool:
			role = "user"
		case ChatMessageRoleAssistant:
			role = "model"
		default:
			role = "user"
		}

		// If role changes, add the current content and start a new one
		if role != currentRole && currentRole != "" {
			addContent()
			currentParts = nil
		}
		currentRole = role

		content := msg.Content
		// Handle thinking tags in content
		if strings.Contains(content, thinkingStartTag) && strings.Contains(content, thinkingEndTag) {
			// Split content into parts based on thinking tags
			parts := strings.Split(content, thinkingStartTag)
			if parts[0] != "" {
				currentParts = append(currentParts, &genai.Part{
					Text: parts[0],
				})
			}
			for _, part := range parts[1:] {
				thinkingAndRest := strings.Split(part, thinkingEndTag)
				if len(thinkingAndRest) != 2 {
					// Malformed thinking tags, treat as regular text
					currentParts = append(currentParts, &genai.Part{
						Text: content,
					})
					break
				}
				currentParts = append(currentParts, &genai.Part{
					Text:    strings.TrimSpace(thinkingAndRest[0]),
					Thought: true,
				})
				if thinkingAndRest[1] != "" {
					currentParts = append(currentParts, &genai.Part{
						Text: thinkingAndRest[1],
					})
				}
			}
		} else {
			currentParts = append(currentParts, &genai.Part{
				Text: content,
			})
		}
	}

	// Add any remaining content
	addContent()

	return contents
}

func googleFromTools(tools []*Tool) []*genai.Tool {
	if len(tools) == 0 {
		return nil
	}

	geminiTools := make([]*genai.Tool, len(tools))
	for i, tool := range tools {
		geminiTools[i] = &genai.Tool{
			FunctionDeclarations: []*genai.FunctionDeclaration{
				{
					Name:        tool.Name,
					Description: tool.Description,
					Parameters:  googleFromSchema(tool.Parameters),
				},
			},
		}
	}
	return geminiTools
}

func googleFromSchema(schema *jsonschema.Schema) *genai.Schema {
	if schema == nil {
		return nil
	}

	geminiSchema := &genai.Schema{
		Type:        genai.Type(schema.Type),
		Description: schema.Description,
		Required:    schema.Required,
	}

	if schema.Enum != nil {
		geminiSchema.Enum = utils.Map(schema.Enum, func(v any) string {
			return v.(string)
		})
	}

	if schema.Properties != nil {
		geminiSchema.Properties = make(map[string]*genai.Schema)
		for pair := schema.Properties.Oldest(); pair != nil; pair = pair.Next() {
			propSchema := pair.Value
			geminiSchema.Properties[pair.Key] = googleFromSchema(propSchema)
		}
	}

	return geminiSchema
}

func googleToChatMessageDelta(result *genai.GenerateContentResponse) *ChatMessageDelta {
	if result == nil || len(result.Candidates) == 0 {
		return nil
	}

	candidate := result.Candidates[0]
	if candidate.Content == nil || len(candidate.Content.Parts) == 0 {
		return nil
	}

	delta := &ChatMessageDelta{
		Role: ChatMessageRoleAssistant,
	}

	// Handle function calls
	for _, part := range candidate.Content.Parts {
		if part.FunctionCall != nil {
			delta.ToolCalls = []ToolCall{
				{
					Name:      part.FunctionCall.Name,
					Arguments: utils.PanicJSON(part.FunctionCall.Args),
				},
			}
			return delta
		}
	}

	// Handle text content
	var content strings.Builder
	for _, part := range candidate.Content.Parts {
		if part.Text != "" {
			if part.Thought {
				content.WriteString(fmt.Sprintf("%s%s%s", thinkingStartTag, part.Text, thinkingEndTag))
			} else {
				content.WriteString(part.Text)
			}
		}
	}
	if content.Len() > 0 {
		delta.Content = content.String()
	}

	return delta
}
