package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sidekick/common"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/shared/constant"
	"github.com/rs/zerolog/log"
	"github.com/zalando/go-keyring"
	"go.temporal.io/sdk/activity"
)

const AnthropicDefaultModel = "claude-opus-4-5"

const AnthropicApiKeySecretName = "ANTHROPIC_API_KEY"
const AnthropicOAuthSecretName = "ANTHROPIC_OAUTH"

const (
	anthropicOAuthBetaHeaders = "oauth-2025-04-20,claude-code-20250219,interleaved-thinking-2025-05-14,fine-grained-tool-streaming-2025-05-14"
	anthropicTokenEndpoint    = "https://console.anthropic.com/v1/oauth/token"
	anthropicClientID         = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"
	keyringService            = "sidekick"
)

type OAuthCredentials struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresAt    int64  `json:"expires_at,omitempty"`
}

type AnthropicToolChat struct{}

func (AnthropicToolChat) ChatStream(ctx context.Context, options ToolChatOptions, deltaChan chan<- ChatMessageDelta, progressChan chan<- ProgressInfo) (*ChatMessageResponse, error) {
	httpClient := &http.Client{Timeout: 20 * time.Minute}

	// Try OAuth credentials first, fall back to API key
	oauthCreds, useOAuth, err := GetAnthropicOAuthCredentials(options.Secrets.SecretManager)
	if err != nil {
		return nil, fmt.Errorf("failed to get Anthropic OAuth credentials: %w", err)
	}
	var client anthropic.Client
	if useOAuth {
		client = anthropic.NewClient(
			option.WithHeader("Authorization", "Bearer "+oauthCreds.AccessToken),
			option.WithHeader("anthropic-beta", anthropicOAuthBetaHeaders),
			option.WithHTTPClient(httpClient),
		)
	} else {
		token, err := options.Secrets.SecretManager.GetSecret(AnthropicApiKeySecretName)
		if err != nil {
			return nil, fmt.Errorf("failed to get Anthropic API key: %w", err)
		}
		client = anthropic.NewClient(
			option.WithAPIKey(token),
			option.WithHTTPClient(httpClient),
		)
	}

	messages, err := anthropicFromChatMessages(options.Params.Messages)
	if err != nil {
		return nil, fmt.Errorf("failed to convert chat messages: %w", err)
	}

	tools, err := anthropicFromTools(options.Params.Tools)
	if err != nil {
		return nil, fmt.Errorf("failed to convert tools: %w", err)
	}

	model := options.Params.Model
	if model == "" {
		model = AnthropicDefaultModel
	}

	var temperature float32 = defaultTemperature
	if options.Params.Temperature != nil {
		temperature = *options.Params.Temperature
	}

	maxTokensToUse := int64(16000)
	if options.Params.MaxTokens > 0 {
		maxTokensToUse = int64(options.Params.MaxTokens)
	}

	if modelInfo, ok := common.GetModel(options.Params.Provider, model); ok {
		if modelInfo.Limit.Output > 0 && maxTokensToUse > int64(modelInfo.Limit.Output) {
			maxTokensToUse = int64(modelInfo.Limit.Output)
		}
	}

	messageParams := anthropic.MessageNewParams{
		Temperature: anthropic.Opt(float64(temperature)),
		Model:       anthropic.Model(model),
		MaxTokens:   maxTokensToUse,
		Messages:    messages,
		Tools:       tools,
	}

	if options.Params.ServiceTier != "" {
		messageParams.ServiceTier = anthropic.MessageNewParamsServiceTier(options.Params.ServiceTier)
	}

	// TODO move into helper function
	switch options.Params.ToolChoice.Type {
	case ToolChoiceTypeAuto:
		messageParams.ToolChoice = anthropic.ToolChoiceUnionParam{
			OfAuto: &anthropic.ToolChoiceAutoParam{},
		}
	case ToolChoiceTypeRequired:
		messageParams.ToolChoice = anthropic.ToolChoiceUnionParam{
			OfAny: &anthropic.ToolChoiceAnyParam{},
		}
	case ToolChoiceTypeTool:
		messageParams.ToolChoice = anthropic.ToolChoiceUnionParam{
			OfTool: &anthropic.ToolChoiceToolParam{
				Name: options.Params.ToolChoice.Name,
			},
		}
	}

	if useOAuth {
		// NOTE: OAuth tokens require using the Claude Code system prompt, otherwise you get a 400 error
		var systemMessages []anthropic.TextBlockParam
		systemMessages = append(systemMessages, anthropic.TextBlockParam{Text: "You are Claude Code, Anthropic's official CLI for Claude."})
		messageParams.System = systemMessages
	}

	stream := client.Messages.NewStreaming(ctx, messageParams)

	var finalMessage anthropic.Message
	startedBlocks := 0
	stoppedBlocks := 0
	for stream.Next() {
		event := stream.Current()
		if activity.IsActivity(ctx) {
			activity.RecordHeartbeat(ctx, event)
		}
		log.Trace().Interface("event", event).Msg("Received streamed event from Anthropic API")

		err := finalMessage.Accumulate(event)
		if err != nil {
			return nil, fmt.Errorf("failed to accumulate message: %w", err)
		}

		switch event := event.AsAny().(type) {
		case anthropic.ContentBlockStartEvent:
			deltaChan <- anthropicContentStartToChatMessageDelta(event.ContentBlock)
			startedBlocks++
		case anthropic.ContentBlockDeltaEvent:
			if len(finalMessage.Content) == 0 {
				return nil, fmt.Errorf("anthropic tool chat failure: received event of type %s but there was no content block", event.Type)
			}
			deltaChan <- anthropicToChatMessageDelta(event.Delta)
		case anthropic.ContentBlockStopEvent:
			stoppedBlocks++
		}
	}

	if stream.Err() != nil {
		log.Error().Err(stream.Err()).Msg("Anthropic tool chat stream error")
		return nil, fmt.Errorf("stream error: %w", stream.Err())
	}

	// the anthropic-go-sdk library seems to have a bug where if the stream
	// stops midway, we don't get an error back in stream.Err(). this can be
	// reproduced not passing in the httpclient, and causing a tool call
	// response where a single chunk may take some time (>3s roughly). to detect
	// this scenario, we check that all content blocks that are started are also
	// stopped.
	if startedBlocks != stoppedBlocks {
		log.Error().Int("started", startedBlocks).Int("stopped", stoppedBlocks).Msg("Anthropic tool chat: number of started and stopped content blocks do not match: did something disconnect?")
		return nil, fmt.Errorf("anthropic tool chat failure: started %d blocks but stopped %d", startedBlocks, stoppedBlocks)
	}

	log.Trace().Interface("responseMessage", finalMessage).Msg("Anthropic tool chat response message")

	response, err := anthropicToChatMessageResponse(finalMessage, options.Params.Provider)
	if err != nil {
		return nil, fmt.Errorf("failed to convert response: %w", err)
	}

	return response, nil
}

func anthropicToChatMessageDelta(eventDelta anthropic.RawContentBlockDeltaUnion) ChatMessageDelta {
	outDelta := ChatMessageDelta{Role: ChatMessageRoleAssistant}

	switch delta := eventDelta.AsAny().(type) {
	case anthropic.TextDelta:
		outDelta.Content = delta.Text
	case anthropic.InputJSONDelta:
		outDelta.ToolCalls = append(outDelta.ToolCalls, ToolCall{
			Arguments: delta.PartialJSON,
		})
	default:
		panic(fmt.Sprintf("unsupported delta type: %v", eventDelta.Type))
	}

	return outDelta
}

func anthropicContentStartToChatMessageDelta(contentBlockUnion anthropic.ContentBlockStartEventContentBlockUnion) ChatMessageDelta {
	switch contentBlock := contentBlockUnion.AsAny().(type) {
	case anthropic.TextBlock:
		return ChatMessageDelta{
			Role:    ChatMessageRoleAssistant,
			Content: contentBlock.Text,
		}
	case anthropic.ToolUseBlock:
		return ChatMessageDelta{
			Role: ChatMessageRoleAssistant,
			ToolCalls: []ToolCall{
				{
					Id:        contentBlockUnion.ID,
					Name:      contentBlockUnion.Name,
					Arguments: string(contentBlock.Input),
				},
			},
		}
	default:
		panic(fmt.Sprintf("unsupported content block type: %s", contentBlockUnion.Type))
	}
}

func anthropicFromChatMessages(messages []ChatMessage) ([]anthropic.MessageParam, error) {
	var anthropicMessages []anthropic.MessageParam
	for _, msg := range messages {
		var blocks []anthropic.ContentBlockParamUnion

		if msg.Content != "" {
			if msg.Role == ChatMessageRoleTool {
				block := anthropic.NewToolResultBlock(msg.ToolCallId, invalidJsonWorkaround(msg.Content), msg.IsError)
				if msg.CacheControl != "" {
					block.OfToolResult.CacheControl = anthropic.NewCacheControlEphemeralParam()
				}
				blocks = append(blocks, block)
			} else {
				block := anthropic.NewTextBlock(invalidJsonWorkaround(msg.Content))
				if msg.CacheControl != "" {
					block.OfText.CacheControl = anthropic.NewCacheControlEphemeralParam()
				}
				blocks = append(blocks, block)
			}
		}
		for _, toolCall := range msg.ToolCalls {
			args := make(map[string]any, 0)
			err := json.Unmarshal([]byte(RepairJson(toolCall.Arguments)), &args)
			if err != nil {
				// anthropic requires valid json, but didn't give us valid json. we improvise.
				args["invalid_json_stringified"] = toolCall.Arguments
			}
			toolUseBlock := anthropic.NewToolUseBlock(toolCall.Id, args, toolCall.Name)
			if msg.CacheControl != "" {
				toolUseBlock.OfToolUse.CacheControl = anthropic.NewCacheControlEphemeralParam()
			}
			blocks = append(blocks, toolUseBlock)
		}
		anthropicMessages = append(anthropicMessages, anthropic.MessageParam{
			Role:    anthropicFromChatMessageRole(msg.Role),
			Content: blocks,
		})
	}

	// anthropic doesn't allow multiple consecutive messages from the same role
	var mergedAnthropicMessages []anthropic.MessageParam
	for _, msg := range anthropicMessages {
		if len(mergedAnthropicMessages) == 0 {
			mergedAnthropicMessages = append(mergedAnthropicMessages, msg)
			continue
		}

		lastMsg := &mergedAnthropicMessages[len(mergedAnthropicMessages)-1]
		if lastMsg.Role == msg.Role {
			lastMsg.Content = append(lastMsg.Content, msg.Content...)
		} else {
			mergedAnthropicMessages = append(mergedAnthropicMessages, msg)
		}
	}

	return mergedAnthropicMessages, nil
}

func invalidJsonWorkaround(s string) string {
	// replace "\x1b" with "" to avoid invalid json error from anthropic
	// with current version of sdk
	return strings.ReplaceAll(s, "\x1b", "")
}

func anthropicFromChatMessageRole(role ChatMessageRole) anthropic.MessageParamRole {
	switch role {
	case ChatMessageRoleSystem, ChatMessageRoleUser, ChatMessageRoleTool:
		// anthropic doesn't have a system role nor a tool role
		return anthropic.MessageParamRoleUser
	case ChatMessageRoleAssistant:
		return anthropic.MessageParamRoleAssistant
	default:
		panic(fmt.Sprintf("unknown role: %s", role))
	}
}

func anthropicFromTools(tools []*Tool) ([]anthropic.ToolUnionParam, error) {
	var result []anthropic.ToolUnionParam
	for _, tool := range tools {
		toolParam := anthropic.ToolParam{
			Name:        tool.Name,
			Description: anthropic.Opt(tool.Description),
			InputSchema: anthropic.ToolInputSchemaParam{
				Properties:  tool.Parameters.Properties,
				Required:    tool.Parameters.Required,
				Type:        constant.Object(tool.Parameters.Type),
				ExtraFields: tool.Parameters.Extras,
			},
		}

		result = append(result, anthropic.ToolUnionParam{OfTool: &toolParam})
	}
	return result, nil
}

func anthropicToChatMessageResponse(message anthropic.Message, provider string) (*ChatMessageResponse, error) {
	response := &ChatMessageResponse{
		ChatMessage: ChatMessage{
			Role:    ChatMessageRoleAssistant,
			Content: "",
		},
		Id:           message.ID,
		StopReason:   string(message.StopReason),
		StopSequence: message.StopSequence,
		Usage: Usage{
			InputTokens:           int(message.Usage.InputTokens),
			OutputTokens:          int(message.Usage.OutputTokens),
			CacheReadInputTokens:  int(message.Usage.CacheReadInputTokens),
			CacheWriteInputTokens: int(message.Usage.CacheCreationInputTokens),
		},
		Model:    string(message.Model),
		Provider: provider,
	}

	for _, block := range message.Content {
		switch block.AsAny().(type) {
		case anthropic.TextBlock:
			response.Content += block.Text
		case anthropic.ToolUseBlock:
			response.ToolCalls = append(response.ToolCalls, ToolCall{
				Id:        block.ID,
				Name:      block.Name,
				Arguments: string(block.Input),
			})
		}
	}

	return response, nil
}

func GetAnthropicOAuthCredentials(secretManager interface{ GetSecret(string) (string, error) }) (*OAuthCredentials, bool, error) {
	oauthJSON, err := secretManager.GetSecret(AnthropicOAuthSecretName)
	if err != nil {
		// Secret not found means OAuth isn't configured, fall back to API key
		return nil, false, nil
	}

	var creds OAuthCredentials
	if err := json.Unmarshal([]byte(oauthJSON), &creds); err != nil {
		return nil, false, fmt.Errorf("failed to parse OAuth credentials: %w", err)
	}

	if creds.AccessToken == "" {
		return nil, false, fmt.Errorf("OAuth credentials missing access token")
	}

	// Refresh token proactively if it expires within 5 minutes
	if creds.ExpiresAt > 0 && time.Now().Unix() > creds.ExpiresAt-300 {
		log.Info().Msg("OAuth token expiring soon, refreshing proactively")
		newCreds, err := RefreshAnthropicOAuthToken(creds.RefreshToken)
		if err != nil {
			return nil, false, fmt.Errorf("failed to refresh OAuth token: %w", err)
		}
		if storeErr := StoreAnthropicOAuthCredentials(newCreds); storeErr != nil {
			log.Warn().Err(storeErr).Msg("Failed to store refreshed OAuth credentials")
		}
		return newCreds, true, nil
	}

	return &creds, true, nil
}

// AnthropicOAuthBetaHeaders returns the beta headers required for OAuth authentication
const AnthropicOAuthBetaHeaders = anthropicOAuthBetaHeaders

func RefreshAnthropicOAuthToken(refreshToken string) (*OAuthCredentials, error) {
	reqBody := map[string]string{
		"grant_type":    "refresh_token",
		"refresh_token": refreshToken,
		"client_id":     anthropicClientID,
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal refresh request: %w", err)
	}

	req, err := http.NewRequest("POST", anthropicTokenEndpoint, bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create refresh request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send refresh request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read refresh response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token refresh failed with status %d: %s", resp.StatusCode, string(body))
	}

	var tokenResp struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("failed to parse refresh response: %w", err)
	}

	var expiresAt int64
	if tokenResp.ExpiresIn > 0 {
		expiresAt = time.Now().Unix() + tokenResp.ExpiresIn
	}

	return &OAuthCredentials{
		AccessToken:  tokenResp.AccessToken,
		RefreshToken: tokenResp.RefreshToken,
		ExpiresAt:    expiresAt,
	}, nil
}

func StoreAnthropicOAuthCredentials(creds *OAuthCredentials) error {
	credsJSON, err := json.Marshal(creds)
	if err != nil {
		return fmt.Errorf("failed to marshal credentials: %w", err)
	}
	return keyring.Set(keyringService, AnthropicOAuthSecretName, string(credsJSON))
}
