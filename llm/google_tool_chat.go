package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sidekick/utils"
	"strings"
	"time"

	"github.com/invopop/jsonschema"
	"github.com/rs/zerolog/log"
	"go.temporal.io/sdk/activity"
	"google.golang.org/genai"
)

type ProgressInfo struct {
	Title   string
	Details string
}

const (
	GoogleApiKeySecretName = "GOOGLE_API_KEY"
	// FIXME /gen/req/plan we need exp for the free-tier, but preview for the
	// paid tier! we should ideally detect which and support both automagically,
	// and if that's not possible, guide the user in how to adjust their
	// settings after manually enabling billing.
	//GoogleDefaultModel     = "gemini-2.5-pro-exp-03-25"
	GoogleDefaultModel = "gemini-2.5-pro-preview-03-25"
	thinkingStartTag   = "<thinking>"
	thinkingEndTag     = "</thinking>"
)

type GoogleToolChat struct{}

func NewGoogleToolChat() *GoogleToolChat {
	return &GoogleToolChat{}
}

func (g GoogleToolChat) ChatStream(ctx context.Context, options ToolChatOptions, deltaChan chan<- ChatMessageDelta, progressChan chan<- ProgressInfo) (*ChatMessageResponse, error) {
	// additional heartbeat until ChatStream ends: we can't rely on the delta
	// heartbeat because thinking tokens are being hidden in the API currently,
	// resulting in a very long time between deltas and thus heartbeat timeouts.
	heartbeatCtx, cancelHeartbeat := context.WithCancel(context.Background())
	defer cancelHeartbeat()
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-heartbeatCtx.Done():
			case <-ctx.Done():
				return
			case <-ticker.C:
				{
					if activity.IsActivity(ctx) {
						activity.RecordHeartbeat(ctx, map[string]bool{"fake": true})
					}
					continue
				}
			}
		}
	}()

	providerName := options.Params.ModelConfig.Provider
	providerNameNormalized := options.Params.ModelConfig.NormalizedProviderName()
	apiKey, err := options.Secrets.SecretManager.GetSecret(fmt.Sprintf("%s_API_KEY", providerNameNormalized))
	if err != nil {
		return nil, fmt.Errorf("failed to get %s API key: %w", providerName, err)
	}

	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:  apiKey,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create %s client: %w", providerName, err)
	}

	model := GoogleDefaultModel
	if options.Params.ModelConfig.Model != "" {
		model = options.Params.ModelConfig.Model
	}

	contents := googleFromChatMessages(options.Params.Messages)

	toolConfig, err := googleFromToolChoice(options.Params.ToolChoice)
	if err != nil {
		return nil, err
	}

	config := &genai.GenerateContentConfig{
		ToolConfig: toolConfig,
		Tools:      googleFromTools(options.Params.Tools),
		ThinkingConfig: &genai.ThinkingConfig{
			IncludeThoughts: true,
		},
	}
	if options.Params.Temperature != nil {
		config.Temperature = options.Params.Temperature
	}

	stream := client.Models.GenerateContentStream(ctx, model, contents, config)
	var deltas []ChatMessageDelta
	var lastResult *genai.GenerateContentResponse // Store the last response for usage metadata

	for result, err := range stream {
		if err != nil {
			return nil, fmt.Errorf("failed to iterate on %s tool chat stream: %w", providerName, err)
		}
		// TODO: handle result.UsageMetadata.CandidatesTokenCount
		delta, progress := googleToChatMessageDelta(result)
		if delta != nil {
			deltaChan <- *delta
			deltas = append(deltas, *delta)
		} else {
			log.Debug().Msgf("did not convert result to delta for %s tool chat stream: %s", providerName , utils.PrettyJSON(result))
		}
		if progress != nil && progressChan != nil {
			progressChan <- *progress
		}
		// Keep track of the last response, as usage metadata is often in the final one
		lastResult = result
	}

	message := stitchDeltasToMessage(deltas, true)
	if message.Role == "" {
		// It's possible to get only usage metadata without content/role in some scenarios (e.g., safety filters)
		// If we have a lastResult with metadata, we might still want to return usage.
		// However, the current logic requires a role. If no deltas were received at all, error out.
		if len(deltas) == 0 && (lastResult == nil || lastResult.UsageMetadata == nil) {
			return nil, fmt.Errorf("received no streamed events from %s backend", providerName)
		}
		// If we only got metadata but no actual message content/role delta, we still error for now.
		// A valid message requires a role.
		if len(deltas) == 0 {
			return nil, fmt.Errorf("received no streamed events or final usage metadata from %s backend", providerName)
		}
		// If we got deltas but couldn't form a message (e.g., missing role), it's an error.
		return nil, errors.New("chat message role not found after stitching deltas")
	}

	// Extract usage information from the last response
	usage := Usage{} // Default to zero values
	if lastResult != nil && lastResult.UsageMetadata != nil {
		usage.InputTokens = int(lastResult.UsageMetadata.PromptTokenCount)
		usage.OutputTokens = int(lastResult.UsageMetadata.CandidatesTokenCount)
		// Note: TotalTokenCount is also available if needed later: int(lastResult.UsageMetadata.TotalTokenCount)
	}

	return &ChatMessageResponse{
		ChatMessage: message,
		Model:       model,
		Provider:    providerName,
		Usage:       usage,
	}, nil
}

func googleFromToolChoice(toolChoice ToolChoice) (*genai.ToolConfig, error) {
	var functionCallingMode genai.FunctionCallingConfigMode
	var allowedFunctionNames []string
	switch toolChoice.Type {
	case ToolChoiceTypeAuto, ToolChoiceTypeUnspecified:
		functionCallingMode = genai.FunctionCallingConfigModeAuto
	case ToolChoiceTypeRequired:
		functionCallingMode = genai.FunctionCallingConfigModeAny
	case ToolChoiceTypeTool:
		functionCallingMode = genai.FunctionCallingConfigModeAny
		allowedFunctionNames = append(allowedFunctionNames, toolChoice.Name)
	default:
		return nil, fmt.Errorf("unknown tool choice type: %s", toolChoice.Type)
	}

	return &genai.ToolConfig{
		FunctionCallingConfig: &genai.FunctionCallingConfig{
			Mode:                 functionCallingMode,
			AllowedFunctionNames: allowedFunctionNames,
		},
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
			if content != "" {
				if msg.Role == ChatMessageRoleTool {
					functionResponse := genai.FunctionResponse{
						ID:   msg.ToolCallId,
						Name: msg.Name,
					}
					if msg.IsError {
						functionResponse.Response = map[string]any{"error": msg.Content}
					} else {
						functionResponse.Response = map[string]any{"output": msg.Content}
					}
					currentParts = append(currentParts, &genai.Part{
						FunctionResponse: &functionResponse,
					})
				} else {
					currentParts = append(currentParts, &genai.Part{
						Text: content,
					})
				}
			}
		}

		for _, toolCall := range msg.ToolCalls {
			args := make(map[string]any, 0)
			err := json.Unmarshal([]byte(RepairJson(toolCall.Arguments)), &args)
			if err != nil {
				// anthropic requires valid json, but didn't give us valid json. we improvise.
				// NOTE: copied to google from anthropic without checking
				args["invalid_json_stringified"] = toolCall.Arguments
			}

			currentParts = append(currentParts, &genai.Part{
				FunctionCall: &genai.FunctionCall{
					ID:   toolCall.Id,
					Name: toolCall.Name,
					Args: args,
				},
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

	var genaiTools []*genai.Tool
	if len(tools) > 0 {
		genaiTool := &genai.Tool{
			FunctionDeclarations: utils.Map(tools, func(tool *Tool) *genai.FunctionDeclaration {
				return &genai.FunctionDeclaration{
					Name:        tool.Name,
					Description: tool.Description,
					Parameters:  googleFromSchema(tool.Parameters),
				}
			}),
		}
		genaiTools = append(genaiTools, genaiTool)
	}
	return genaiTools
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

	if schema.Items != nil {
		geminiSchema.Items = googleFromSchema(schema.Items)
	}

	return geminiSchema
}

func googleToChatMessageDelta(result *genai.GenerateContentResponse) (*ChatMessageDelta, *ProgressInfo) {
	if result == nil || len(result.Candidates) == 0 {
		return nil, nil
	}

	candidate := result.Candidates[0]
	if candidate.Content == nil || len(candidate.Content.Parts) == 0 {
		return nil, nil
	}

	delta := &ChatMessageDelta{
		Role: ChatMessageRoleAssistant,
	}

	// Handle function calls
	for _, part := range candidate.Content.Parts {
		if part.FunctionCall != nil {
			delta.ToolCalls = []ToolCall{
				{
					Id:        part.FunctionCall.ID,
					Name:      part.FunctionCall.Name,
					Arguments: utils.PanicJSON(part.FunctionCall.Args),
				},
			}
			return delta, nil
		}
	}

	// Handle text content
	var content strings.Builder
	var progress *ProgressInfo

	for _, part := range candidate.Content.Parts {
		if part.Text != "" {
			if part.Thought {
				// Parse thinking content into progress info
				lines := strings.Split(strings.TrimSpace(part.Text), "\n")
				if len(lines) == 0 {
					continue
				}

				title := lines[0]
				// Strip ** markers if present
				title = strings.TrimPrefix(title, "**")
				title = strings.TrimSuffix(title, "**")
				title = strings.TrimSpace(title)

				// Truncate title at last space before 120 chars if too long
				if len(title) > 120 {
					lastSpace := strings.LastIndex(title[:120], " ")
					if lastSpace == -1 {
						lastSpace = 117
					}
					title = title[:lastSpace] + "..."
				}

				// Details should only include remaining lines after title
				details := strings.Join(lines[1:], "\n")
				progress = &ProgressInfo{
					Title:   title,
					Details: details,
				}
			} else {
				content.WriteString(part.Text)
			}
		}
	}

	if content.Len() > 0 {
		delta.Content = content.String()
	}

	if delta.Content == "" && delta.ToolCalls == nil && progress == nil {
		return nil, nil
	}

	return delta, progress
}
