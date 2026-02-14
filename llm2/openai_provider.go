package llm2

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sidekick/common"
	"strings"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/shared"
)

const openaiChatDefaultModel = "gpt-5.2"

type OpenAIProvider struct {
	BaseURL      string
	DefaultModel string
}

func (p OpenAIProvider) Stream(ctx context.Context, options Options, eventChan chan<- Event) (*MessageResponse, error) {
	messages := options.Params.ChatHistory.Llm2Messages()

	providerNameNormalized := options.Params.ModelConfig.NormalizedProviderName()
	token, err := options.Secrets.SecretManager.GetSecret(fmt.Sprintf("%s_API_KEY", providerNameNormalized))
	if err != nil {
		return nil, err
	}

	httpClient := &http.Client{
		Timeout: 45 * time.Minute,
	}
	clientOptions := []option.RequestOption{
		option.WithAPIKey(token),
		option.WithHTTPClient(httpClient),
	}
	if p.BaseURL != "" {
		clientOptions = append(clientOptions, option.WithBaseURL(p.BaseURL))
	}
	client := openai.NewClient(clientOptions...)

	model := options.Params.Model
	if model == "" {
		if p.DefaultModel != "" {
			model = p.DefaultModel
		} else {
			model = openaiChatDefaultModel
		}
	}

	chatMessages, err := messagesToChatCompletionParams(messages)
	if err != nil {
		return nil, fmt.Errorf("failed to build messages: %w", err)
	}

	params := openai.ChatCompletionNewParams{
		Messages: chatMessages,
		Model:    shared.ChatModel(model),
		StreamOptions: openai.ChatCompletionStreamOptionsParam{
			IncludeUsage: openai.Bool(true),
		},
	}

	if options.Params.Temperature != nil {
		params.Temperature = openai.Float(float64(*options.Params.Temperature))
	}

	if options.Params.MaxTokens > 0 {
		params.MaxCompletionTokens = param.NewOpt(int64(options.Params.MaxTokens))
	}

	if options.Params.ParallelToolCalls != nil {
		params.ParallelToolCalls = param.NewOpt(*options.Params.ParallelToolCalls)
	}

	if options.Params.ReasoningEffort != "" {
		params.ReasoningEffort = shared.ReasoningEffort(options.Params.ReasoningEffort)
	}

	if len(options.Params.Tools) > 0 {
		toolsToUse := options.Params.Tools
		if options.Params.ToolChoice.Type == common.ToolChoiceTypeTool {
			toolsToUse = filterToolsByName(options.Params.Tools, options.Params.ToolChoice.Name)
		}

		tools, err := openaiChatFromTools(toolsToUse)
		if err != nil {
			return nil, fmt.Errorf("failed to convert tools: %w", err)
		}
		params.Tools = tools
		params.ToolChoice = openaiChatFromToolChoice(options.Params.ToolChoice, toolsToUse)
	}

	var extraBodyOptions []option.RequestOption
	for key, value := range options.Params.ExtraBody {
		extraBodyOptions = append(extraBodyOptions, option.WithJSONSet(key, value))
	}

	stream := client.Chat.Completions.NewStreaming(ctx, params, extraBodyOptions...)

	var events []Event
	var finishReason string
	var usage Usage
	var responseModel string

	// Track tool calls being built across deltas.
	// The chat completions API streams tool calls as incremental deltas
	// with an index field; we map each index to the block index we assigned.
	toolCallBlockIndex := make(map[int]int) // delta tool call index -> event block index
	nextBlockIndex := 0

	for stream.Next() {
		chunk := stream.Current()

		if chunk.Model != "" {
			responseModel = chunk.Model
		}

		// Usage may arrive on any chunk (including the final one with no
		// choices), so check every chunk unconditionally.
		if chunk.Usage.JSON.PromptTokens.Valid() {
			usage.InputTokens = int(chunk.Usage.PromptTokens)
			usage.OutputTokens = int(chunk.Usage.CompletionTokens)
			if chunk.Usage.PromptTokensDetails.CachedTokens > 0 {
				usage.CacheReadInputTokens = int(chunk.Usage.PromptTokensDetails.CachedTokens)
			}
			// litellm proxying Anthropic models returns cache_creation_input_tokens
			// as a top-level field in the usage object.
			if f, ok := chunk.Usage.JSON.ExtraFields["cache_creation_input_tokens"]; ok {
				var cacheWriteTokens int
				if json.Unmarshal([]byte(f.Raw()), &cacheWriteTokens) == nil {
					usage.CacheWriteInputTokens = cacheWriteTokens
				}
			}
		}

		if len(chunk.Choices) == 0 {
			continue
		}

		choice := chunk.Choices[0]

		if choice.FinishReason != "" {
			finishReason = choice.FinishReason
		}

		delta := choice.Delta

		// Handle text content
		if delta.Content != "" {
			if nextBlockIndex == 0 || !hasTextBlockAtIndex(events, nextBlockIndex-1) {
				evt := Event{
					Type:  EventBlockStarted,
					Index: nextBlockIndex,
					ContentBlock: &ContentBlock{
						Type: ContentBlockTypeText,
						Text: "",
					},
				}
				eventChan <- evt
				events = append(events, evt)
				nextBlockIndex++
			}

			textBlockIdx := findLastTextBlockIndex(events)
			evt := Event{
				Type:  EventTextDelta,
				Index: textBlockIdx,
				Delta: delta.Content,
			}
			eventChan <- evt
			events = append(events, evt)
		}

		// Handle tool calls
		for _, tc := range delta.ToolCalls {
			tcIdx := int(tc.Index)

			if _, exists := toolCallBlockIndex[tcIdx]; !exists {
				// New tool call
				blockIdx := nextBlockIndex
				toolCallBlockIndex[tcIdx] = blockIdx
				nextBlockIndex++

				name := tc.Function.Name
				// Clean up rarely-occurring bad syntax from openai
				name = strings.TrimPrefix(name, "tools.")
				name = strings.TrimPrefix(name, "tool.")
				name = strings.TrimPrefix(name, "functions.")
				name = strings.TrimPrefix(name, "function.")

				evt := Event{
					Type:  EventBlockStarted,
					Index: blockIdx,
					ContentBlock: &ContentBlock{
						Id:   tc.ID,
						Type: ContentBlockTypeToolUse,
						ToolUse: &ToolUseBlock{
							Id:        tc.ID,
							Name:      name,
							Arguments: "",
						},
					},
				}
				eventChan <- evt
				events = append(events, evt)
			}

			blockIdx := toolCallBlockIndex[tcIdx]

			if tc.Function.Arguments != "" {
				evt := Event{
					Type:  EventTextDelta,
					Index: blockIdx,
					Delta: tc.Function.Arguments,
				}
				eventChan <- evt
				events = append(events, evt)
			}
		}
	}

	if err := stream.Err(); err != nil {
		return nil, wrapOpenAIError(err)
	}

	// Emit block_done events for all blocks
	doneBlocks := make(map[int]bool)
	for _, evt := range events {
		if evt.Type == EventBlockStarted {
			doneBlocks[evt.Index] = true
		}
	}
	for idx := range doneBlocks {
		evt := Event{
			Type:  EventBlockDone,
			Index: idx,
		}
		eventChan <- evt
		events = append(events, evt)
	}

	outputMessage := accumulateOpenaiChatEventsToMessage(events)

	if responseModel == "" {
		responseModel = model
	}

	stopReason := finishReason
	if stopReason == "stop" || stopReason == "" {
		stopReason = "stop"
	}

	return &MessageResponse{
		Model:           responseModel,
		Provider:        options.Params.Provider,
		Output:          outputMessage,
		StopReason:      stopReason,
		Usage:           usage,
		ReasoningEffort: options.Params.ReasoningEffort,
	}, nil
}

func hasTextBlockAtIndex(events []Event, index int) bool {
	for _, evt := range events {
		if evt.Type == EventBlockStarted && evt.Index == index && evt.ContentBlock != nil && evt.ContentBlock.Type == ContentBlockTypeText {
			return true
		}
	}
	return false
}

func findLastTextBlockIndex(events []Event) int {
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Type == EventBlockStarted && events[i].ContentBlock != nil && events[i].ContentBlock.Type == ContentBlockTypeText {
			return events[i].Index
		}
	}
	return 0
}

func cacheControlExtraFields(value string) map[string]any {
	return map[string]any{
		"cache_control": map[string]string{"type": value},
	}
}

func messagesToChatCompletionParams(messages []Message) ([]openai.ChatCompletionMessageParamUnion, error) {
	var result []openai.ChatCompletionMessageParamUnion

	for _, msg := range messages {
		switch msg.Role {
		case RoleSystem:
			for _, block := range msg.Content {
				if block.Type == ContentBlockTypeText {
					textPart := openai.ChatCompletionContentPartTextParam{Text: block.Text}
					if block.CacheControl != "" {
						textPart.SetExtraFields(cacheControlExtraFields(block.CacheControl))
					}
					result = append(result, openai.ChatCompletionMessageParamUnion{
						OfSystem: &openai.ChatCompletionSystemMessageParam{
							Content: openai.ChatCompletionSystemMessageParamContentUnion{
								OfArrayOfContentParts: []openai.ChatCompletionContentPartTextParam{textPart},
							},
						},
					})
				}
			}

		case RoleUser:
			var userParts []openai.ChatCompletionContentPartUnionParam
			for _, block := range msg.Content {
				switch block.Type {
				case ContentBlockTypeText:
					textPart := openai.ChatCompletionContentPartTextParam{Text: block.Text}
					if block.CacheControl != "" {
						textPart.SetExtraFields(cacheControlExtraFields(block.CacheControl))
					}
					userParts = append(userParts, openai.ChatCompletionContentPartUnionParam{
						OfText: &textPart,
					})
				case ContentBlockTypeImage:
					if block.Image == nil {
						return nil, fmt.Errorf("image block missing Image data")
					}
					url := block.Image.Url
					if strings.HasPrefix(url, "data:") {
						newURL, _, _, err := PrepareImageDataURLForLimits(url, 20*1024*1024, 2048)
						if err != nil {
							return nil, fmt.Errorf("failed to prepare image for OpenAI: %w", err)
						}
						url = newURL
					}
					if strings.HasPrefix(url, "data:") || strings.HasPrefix(url, "http://") || strings.HasPrefix(url, "https://") {
						userParts = append(userParts, openai.ChatCompletionContentPartUnionParam{
							OfImageURL: &openai.ChatCompletionContentPartImageParam{
								ImageURL: openai.ChatCompletionContentPartImageImageURLParam{
									URL:    url,
									Detail: "high",
								},
							},
						})
					} else {
						return nil, fmt.Errorf("unsupported image URL scheme: %s", url)
					}
				case ContentBlockTypeToolResult:
					if block.ToolResult == nil {
						return nil, fmt.Errorf("tool_result block missing ToolResult data")
					}
					toolMsg := openai.ChatCompletionToolMessageParam{
						ToolCallID: block.ToolResult.ToolCallId,
						Content: openai.ChatCompletionToolMessageParamContentUnion{
							OfString: param.NewOpt(block.ToolResult.Text),
						},
					}
					if block.CacheControl != "" {
						toolMsg.SetExtraFields(cacheControlExtraFields(block.CacheControl))
					}
					result = append(result, openai.ChatCompletionMessageParamUnion{
						OfTool: &toolMsg,
					})
				default:
					return nil, fmt.Errorf("unsupported content block type %s for user role", block.Type)
				}
			}
			if len(userParts) > 0 {
				result = append(result, openai.ChatCompletionMessageParamUnion{
					OfUser: &openai.ChatCompletionUserMessageParam{
						Content: openai.ChatCompletionUserMessageParamContentUnion{
							OfArrayOfContentParts: userParts,
						},
					},
				})
			}

		case RoleAssistant:
			assistantMsg := &openai.ChatCompletionAssistantMessageParam{}
			var contentParts []openai.ChatCompletionAssistantMessageParamContentArrayOfContentPartUnion
			var hasContent bool

			for _, block := range msg.Content {
				switch block.Type {
				case ContentBlockTypeText:
					contentParts = append(contentParts, openai.ChatCompletionAssistantMessageParamContentArrayOfContentPartUnion{
						OfText: &openai.ChatCompletionContentPartTextParam{
							Text: block.Text,
						},
					})
					hasContent = true
				case ContentBlockTypeToolUse:
					if block.ToolUse == nil {
						return nil, fmt.Errorf("tool_use block missing ToolUse data")
					}
					assistantMsg.ToolCalls = append(assistantMsg.ToolCalls, openai.ChatCompletionMessageToolCallUnionParam{
						OfFunction: &openai.ChatCompletionMessageFunctionToolCallParam{
							ID: block.ToolUse.Id,
							Function: openai.ChatCompletionMessageFunctionToolCallFunctionParam{
								Name:      block.ToolUse.Name,
								Arguments: block.ToolUse.Arguments,
							},
						},
					})
					hasContent = true
				case ContentBlockTypeReasoning:
					continue
				case ContentBlockTypeRefusal:
					if block.Refusal != nil {
						contentParts = append(contentParts, openai.ChatCompletionAssistantMessageParamContentArrayOfContentPartUnion{
							OfRefusal: &openai.ChatCompletionContentPartRefusalParam{
								Refusal: block.Refusal.Reason,
							},
						})
					}
					hasContent = true
				default:
					return nil, fmt.Errorf("unsupported content block type %s for assistant role", block.Type)
				}
			}

			if hasContent {
				if len(contentParts) == 1 && contentParts[0].OfText != nil {
					assistantMsg.Content = openai.ChatCompletionAssistantMessageParamContentUnion{
						OfString: param.NewOpt(contentParts[0].OfText.Text),
					}
				} else if len(contentParts) > 0 {
					assistantMsg.Content = openai.ChatCompletionAssistantMessageParamContentUnion{
						OfArrayOfContentParts: contentParts,
					}
				}
				for _, block := range msg.Content {
					if block.CacheControl != "" {
						assistantMsg.SetExtraFields(cacheControlExtraFields(block.CacheControl))
						break
					}
				}
				result = append(result, openai.ChatCompletionMessageParamUnion{
					OfAssistant: assistantMsg,
				})
			}

		default:
			return nil, fmt.Errorf("unsupported role: %s", msg.Role)
		}
	}

	return result, nil
}

func accumulateOpenaiChatEventsToMessage(events []Event) Message {
	blocks := make(map[int]*ContentBlock)
	maxIndex := -1

	for _, evt := range events {
		if evt.Index > maxIndex {
			maxIndex = evt.Index
		}

		switch evt.Type {
		case EventBlockStarted:
			if evt.ContentBlock != nil {
				blockCopy := *evt.ContentBlock
				if blockCopy.Type == ContentBlockTypeToolUse && blockCopy.ToolUse != nil {
					toolUseCopy := *blockCopy.ToolUse
					blockCopy.ToolUse = &toolUseCopy
				}
				blocks[evt.Index] = &blockCopy
			}

		case EventTextDelta:
			if block, ok := blocks[evt.Index]; ok {
				if block.Type == ContentBlockTypeText {
					block.Text += evt.Delta
				} else if block.Type == ContentBlockTypeToolUse && block.ToolUse != nil {
					block.ToolUse.Arguments += evt.Delta
				}
			}

		case EventBlockDone:
			// no-op, block is already accumulated
		}
	}

	orderedBlocks := make([]ContentBlock, 0, maxIndex+1)
	for i := 0; i <= maxIndex; i++ {
		if block, ok := blocks[i]; ok {
			orderedBlocks = append(orderedBlocks, *block)
		}
	}

	return Message{
		Role:    RoleAssistant,
		Content: orderedBlocks,
	}
}

func openaiChatFromTools(tools []*common.Tool) ([]openai.ChatCompletionToolUnionParam, error) {
	result := make([]openai.ChatCompletionToolUnionParam, 0, len(tools))

	for _, tool := range tools {
		params, err := jsonSchemaToMap(tool.Parameters)
		if err != nil {
			return nil, fmt.Errorf("failed to convert parameters for tool %s: %w", tool.Name, err)
		}

		result = append(result, openai.ChatCompletionToolUnionParam{
			OfFunction: &openai.ChatCompletionFunctionToolParam{
				Function: shared.FunctionDefinitionParam{
					Name:        tool.Name,
					Description: param.NewOpt(tool.Description),
					Parameters:  params,
				},
			},
		})
	}

	return result, nil
}

func openaiChatFromToolChoice(toolChoice common.ToolChoice, tools []*common.Tool) openai.ChatCompletionToolChoiceOptionUnionParam {
	if len(tools) == 0 {
		return openai.ChatCompletionToolChoiceOptionUnionParam{}
	}

	switch toolChoice.Type {
	case common.ToolChoiceTypeAuto, common.ToolChoiceTypeUnspecified:
		return openai.ChatCompletionToolChoiceOptionUnionParam{
			OfAuto: param.NewOpt("auto"),
		}
	case common.ToolChoiceTypeRequired:
		return openai.ChatCompletionToolChoiceOptionUnionParam{
			OfAuto: param.NewOpt("required"),
		}
	case common.ToolChoiceTypeTool:
		return openai.ChatCompletionToolChoiceOptionUnionParam{
			OfFunctionToolChoice: &openai.ChatCompletionNamedToolChoiceParam{
				Function: openai.ChatCompletionNamedToolChoiceFunctionParam{
					Name: toolChoice.Name,
				},
			},
		}
	default:
		panic("unknown tool choice: " + string(toolChoice.Type))
	}
}

// wrapOpenAIError extracts detailed error information from OpenAI API errors.
// The openai-go library's Error type only populates its JSON fields when the
// response body matches OpenAI's error format ({"error": {...}}). Third-party
// OpenAI-compatible providers (e.g. Cerebras) often return errors in a
// different format, leaving the parsed fields empty and producing an unhelpful
// error message like `POST "...": 404 Not Found`. This helper dumps the raw
// response body so the actual error details are surfaced.
func wrapOpenAIError(err error) error {
	var apiErr *openai.Error
	if !errors.As(err, &apiErr) {
		return err
	}

	// If the library successfully parsed the error body (e.g. standard OpenAI
	// format), the Message field will be populated — use it directly.
	if apiErr.Message != "" {
		return fmt.Errorf("%s %q: %d %s (message: %s, type: %s, code: %s)",
			apiErr.Request.Method, apiErr.Request.URL,
			apiErr.StatusCode, apiErr.Type,
			apiErr.Message, apiErr.Type, apiErr.Code)
	}

	// Otherwise, dump the response to capture the raw body from non-standard
	// providers. Extract only the body (after the header/body separator) to
	// avoid logging verbose headers.
	dump := apiErr.DumpResponse(true)
	if len(dump) > 0 {
		body := dump
		for _, sep := range [][]byte{[]byte("\r\n\r\n"), []byte("\n\n")} {
			if parts := bytes.SplitN(dump, sep, 2); len(parts) == 2 {
				body = bytes.TrimSpace(parts[1])
				break
			}
		}
		return fmt.Errorf("%s %q: %d - response body: %s",
			apiErr.Request.Method, apiErr.Request.URL,
			apiErr.StatusCode, string(body))
	}

	return err
}
