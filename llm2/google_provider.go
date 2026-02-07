package llm2

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sidekick/common"
	"sidekick/utils"
	"strings"
	"time"

	"github.com/invopop/jsonschema"
	"github.com/rs/zerolog/log"
	"go.temporal.io/sdk/activity"
	"google.golang.org/genai"
)

const (
	googleDefaultModel     = "gemini-3-pro-preview"
	googleApiKeySecretName = "GOOGLE_API_KEY"
	geminiApiKeySecretName = "GEMINI_API_KEY"
)

var googleLegacyThinkingBudgetLlm2 = map[string]int32{
	"minimal": 1024,
	"low":     1024,
	"medium":  8192,
	"high":    24576,
}

type GoogleProvider struct{}

func (p GoogleProvider) Stream(ctx context.Context, options Options, eventChan chan<- Event) (*MessageResponse, error) {
	messages := options.Params.ChatHistory.Llm2Messages()

	// Heartbeat goroutine: Google may suppress thinking token deltas, causing
	// long gaps that would trigger Temporal heartbeat timeouts.
	done := make(chan struct{})
	defer close(done)
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				if activity.IsActivity(ctx) {
					activity.RecordHeartbeat(ctx, map[string]bool{"fake": true})
				}
			}
		}
	}()

	providerName := options.Params.ModelConfig.Provider
	apiKey, err := options.Secrets.SecretManager.GetSecret(googleApiKeySecretName)
	if err != nil {
		apiKey, err = options.Secrets.SecretManager.GetSecret(geminiApiKeySecretName)
		if err != nil {
			return nil, fmt.Errorf("failed to get %s API key: %w", providerName, err)
		}
	}

	httpClient := &http.Client{Timeout: 10 * time.Minute}
	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:     apiKey,
		Backend:    genai.BackendGeminiAPI,
		HTTPClient: httpClient,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create %s client: %w", providerName, err)
	}

	model := googleDefaultModel
	if options.Params.Model != "" {
		model = options.Params.Model
	}

	modelInfo, _ := common.GetModel(options.Params.Provider, model)
	isReasoningModel := modelInfo != nil && modelInfo.Reasoning

	contents := googleFromLlm2Messages(messages, isReasoningModel)

	config := &genai.GenerateContentConfig{}

	if len(options.Params.Tools) > 0 {
		toolConfig, err := googleFromLlm2ToolChoice(options.Params.ToolChoice)
		if err != nil {
			return nil, err
		}
		config.ToolConfig = toolConfig
		config.Tools = googleFromLlm2Tools(options.Params.Tools)

		// Google API does not support a parallel tool calls toggle;
		// the model decides autonomously whether to emit multiple calls.
		if options.Params.ParallelToolCalls != nil {
			log.Debug().Bool("parallelToolCalls", *options.Params.ParallelToolCalls).
				Msg("Google provider does not support ParallelToolCalls setting; ignoring")
		}
	}

	var actualReasoningEffort string
	if isReasoningModel {
		config.ThinkingConfig = &genai.ThinkingConfig{
			IncludeThoughts: true,
		}
		if options.Params.ReasoningEffort != "" {
			isLegacyThinkingBudget := strings.Contains(model, "2.5")
			if isLegacyThinkingBudget {
				budget, ok := googleLegacyThinkingBudgetLlm2[strings.ToLower(options.Params.ReasoningEffort)]
				if !ok {
					log.Warn().Str("reasoningEffort", options.Params.ReasoningEffort).
						Msg("unknown reasoning effort for legacy thinking budget model; using default")
				} else {
					config.ThinkingConfig.ThinkingBudget = &budget
					actualReasoningEffort = options.Params.ReasoningEffort
				}
			} else {
				config.ThinkingConfig.ThinkingLevel = genai.ThinkingLevel(strings.ToUpper(options.Params.ReasoningEffort))
				actualReasoningEffort = options.Params.ReasoningEffort
			}
		}
	}

	if options.Params.Temperature != nil {
		config.Temperature = options.Params.Temperature
	}

	if options.Params.MaxTokens > 0 {
		config.MaxOutputTokens = int32(options.Params.MaxTokens)
	}

	stream := client.Models.GenerateContentStream(ctx, model, contents, config)

	var events []Event
	var lastResult *genai.GenerateContentResponse
	state := &googleStreamState{}

	for result, err := range stream {
		if err != nil {
			return nil, fmt.Errorf("failed to iterate on %s stream: %w", providerName, err)
		}
		lastResult = result

		newEvents := googleResultToEvents(result, state)
		for _, ev := range newEvents {
			events = append(events, ev)
			eventChan <- ev
		}
	}

	// Finalize any open blocks
	finalEvents := googleFinalizeStream(state)
	for _, ev := range finalEvents {
		events = append(events, ev)
		eventChan <- ev
	}

	output := accumulateGoogleEventsToMessage(events)

	usage := Usage{}
	if lastResult != nil && lastResult.UsageMetadata != nil {
		usage.InputTokens = int(lastResult.UsageMetadata.PromptTokenCount)
		usage.OutputTokens = int(lastResult.UsageMetadata.CandidatesTokenCount) + int(lastResult.UsageMetadata.ThoughtsTokenCount)
		usage.CacheReadInputTokens = int(lastResult.UsageMetadata.CachedContentTokenCount)
	}

	stopReason := ""
	if lastResult != nil && len(lastResult.Candidates) > 0 {
		stopReason = string(lastResult.Candidates[0].FinishReason)
	}

	return &MessageResponse{
		Id:              "",
		Model:           model,
		Provider:        providerName,
		Output:          output,
		StopReason:      stopReason,
		Usage:           usage,
		ReasoningEffort: actualReasoningEffort,
	}, nil
}

// googleStreamState tracks the current streaming state across multiple
// GenerateContentResponse chunks to properly coalesce text deltas.
type googleStreamState struct {
	nextBlockIndex     int
	currentBlockType   ContentBlockType
	currentBlockIdx    int
	blockStarted       bool
	currentBlockHasSig bool
	pendingSignature   []byte
}

// googleResultToEvents converts a single streamed GenerateContentResponse into
// llm2 Events. It uses state to track the current block and coalesce text deltas
// into a single block, while emitting tool_use blocks as complete units.
func googleResultToEvents(result *genai.GenerateContentResponse, state *googleStreamState) []Event {
	if result == nil || len(result.Candidates) == 0 {
		return nil
	}

	candidate := result.Candidates[0]
	if candidate.Content == nil || len(candidate.Content.Parts) == 0 {
		return nil
	}

	var events []Event

	for _, part := range candidate.Content.Parts {
		if part.FunctionCall != nil {
			// Close any open text/reasoning block before emitting tool_use
			if state.blockStarted && (state.currentBlockType == ContentBlockTypeText || state.currentBlockType == ContentBlockTypeReasoning) {
				events = append(events, Event{
					Type:  EventBlockDone,
					Index: state.currentBlockIdx,
				})
				state.blockStarted = false
			}

			idx := state.nextBlockIndex
			state.nextBlockIndex++

			argsBytes, err := json.Marshal(part.FunctionCall.Args)
			if err != nil {
				log.Error().Err(err).Msg("failed to marshal function call args")
				argsBytes = []byte("{}")
			}

			// Tool calls from Google arrive complete, so emit only block_done with full content
			doneBlock := ContentBlock{
				Type: ContentBlockTypeToolUse,
				ToolUse: &ToolUseBlock{
					Id:        part.FunctionCall.ID,
					Name:      part.FunctionCall.Name,
					Arguments: string(argsBytes),
					Signature: part.ThoughtSignature,
				},
			}
			events = append(events, Event{
				Type:         EventBlockDone,
				Index:        idx,
				ContentBlock: &doneBlock,
			})
			continue
		}

		// Handle parts with text OR parts with only a signature (no text)
		if part.Text != "" || len(part.ThoughtSignature) > 0 {
			// A part with only a signature (empty text) attaches to the current block
			if part.Text == "" && len(part.ThoughtSignature) > 0 {
				// Store the signature to attach to the final block
				state.pendingSignature = part.ThoughtSignature
				continue
			}

			var blockType ContentBlockType
			if part.Thought {
				blockType = ContentBlockTypeReasoning
			} else {
				blockType = ContentBlockTypeText
			}

			partHasSig := len(part.ThoughtSignature) > 0

			// Per Google docs: don't concatenate parts with signatures, and don't
			// merge parts with signatures with parts without signatures.
			needNewBlock := !state.blockStarted ||
				state.currentBlockType != blockType ||
				state.currentBlockHasSig ||
				partHasSig

			if needNewBlock {
				// Close previous block if open
				if state.blockStarted {
					events = append(events, Event{
						Type:  EventBlockDone,
						Index: state.currentBlockIdx,
					})
				}

				// Start new block
				idx := state.nextBlockIndex
				state.nextBlockIndex++
				state.currentBlockIdx = idx
				state.currentBlockType = blockType
				state.blockStarted = true
				state.currentBlockHasSig = partHasSig

				var startBlock ContentBlock
				if blockType == ContentBlockTypeReasoning {
					startBlock = ContentBlock{
						Type:      ContentBlockTypeReasoning,
						Reasoning: &ReasoningBlock{Text: "", Signature: part.ThoughtSignature},
					}
				} else {
					startBlock = ContentBlock{
						Type:      ContentBlockTypeText,
						Text:      "",
						Signature: part.ThoughtSignature,
					}
				}
				events = append(events, Event{
					Type:         EventBlockStarted,
					Index:        idx,
					ContentBlock: &startBlock,
				})
			}

			// Emit text delta for the current block
			events = append(events, Event{
				Type:  EventTextDelta,
				Index: state.currentBlockIdx,
				Delta: part.Text,
			})
		}
	}

	return events
}

// googleFinalizeStream emits any pending block_done events for open blocks.
func googleFinalizeStream(state *googleStreamState) []Event {
	if state.blockStarted {
		event := Event{
			Type:  EventBlockDone,
			Index: state.currentBlockIdx,
		}
		// Attach pending signature to the final block if present
		if len(state.pendingSignature) > 0 {
			event.Signature = state.pendingSignature
		}
		return []Event{event}
	}
	return nil
}

func accumulateGoogleEventsToMessage(events []Event) Message {
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
				} else if blockCopy.Type == ContentBlockTypeReasoning && blockCopy.Reasoning != nil {
					reasoningCopy := *blockCopy.Reasoning
					blockCopy.Reasoning = &reasoningCopy
				}
				blocks[evt.Index] = &blockCopy
			}

		case EventTextDelta:
			if block, ok := blocks[evt.Index]; ok {
				if block.Type == ContentBlockTypeText {
					block.Text += evt.Delta
				} else if block.Type == ContentBlockTypeReasoning && block.Reasoning != nil {
					block.Reasoning.Text += evt.Delta
				} else if block.Type == ContentBlockTypeToolUse && block.ToolUse != nil {
					block.ToolUse.Arguments += evt.Delta
				}
			}

		case EventSummaryTextDelta:
			if block, ok := blocks[evt.Index]; ok {
				if block.Type == ContentBlockTypeText {
					block.Text += evt.Delta
				} else if block.Type == ContentBlockTypeReasoning && block.Reasoning != nil {
					block.Reasoning.Summary += evt.Delta
				}
			}

		case EventBlockDone:
			if evt.ContentBlock != nil {
				// For tool_use blocks that arrive complete (no prior block_started)
				if evt.ContentBlock.Type == ContentBlockTypeToolUse && evt.ContentBlock.ToolUse != nil {
					if _, exists := blocks[evt.Index]; !exists {
						blockCopy := *evt.ContentBlock
						toolUseCopy := *evt.ContentBlock.ToolUse
						blockCopy.ToolUse = &toolUseCopy
						blocks[evt.Index] = &blockCopy
					}
				}
				// Handle reasoning block finalization
				if block, ok := blocks[evt.Index]; ok {
					if block.Type == ContentBlockTypeReasoning && evt.ContentBlock.Type == ContentBlockTypeReasoning {
						if evt.ContentBlock.Reasoning != nil {
							if block.Reasoning == nil {
								block.Reasoning = &ReasoningBlock{}
							}
							if evt.ContentBlock.Reasoning.Text != "" {
								block.Reasoning.Text = evt.ContentBlock.Reasoning.Text
							}
							if evt.ContentBlock.Reasoning.Summary != "" {
								block.Reasoning.Summary = evt.ContentBlock.Reasoning.Summary
							}
							if evt.ContentBlock.Reasoning.EncryptedContent != "" {
								block.Reasoning.EncryptedContent = evt.ContentBlock.Reasoning.EncryptedContent
							}
						}
					}
				}
			}
			// Attach signature from block_done event to the block
			if len(evt.Signature) > 0 {
				if block, ok := blocks[evt.Index]; ok {
					if block.Type == ContentBlockTypeReasoning && block.Reasoning != nil {
						block.Reasoning.Signature = evt.Signature
					} else if block.Type == ContentBlockTypeText {
						block.Signature = evt.Signature
					}
				}
			}
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

func googleFromLlm2ToolChoice(toolChoice common.ToolChoice) (*genai.ToolConfig, error) {
	var functionCallingMode genai.FunctionCallingConfigMode
	var allowedFunctionNames []string
	switch toolChoice.Type {
	case common.ToolChoiceTypeAuto, common.ToolChoiceTypeUnspecified:
		functionCallingMode = genai.FunctionCallingConfigModeAuto
	case common.ToolChoiceTypeRequired:
		functionCallingMode = genai.FunctionCallingConfigModeAny
	case common.ToolChoiceTypeTool:
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

func googleFromLlm2Messages(messages []Message, isReasoningModel bool) []*genai.Content {
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
		case RoleUser, RoleSystem:
			role = "user"
		case RoleAssistant:
			role = "model"
		default:
			role = "user"
		}

		if role != currentRole && currentRole != "" {
			addContent()
			currentParts = nil
		}
		currentRole = role

		for _, block := range msg.Content {
			switch block.Type {
			case ContentBlockTypeText:
				if block.Text == "" {
					continue
				}
				currentParts = append(currentParts, &genai.Part{
					Text:             block.Text,
					ThoughtSignature: block.Signature,
				})

			case ContentBlockTypeReasoning:
				if block.Reasoning != nil && block.Reasoning.Text != "" {
					currentParts = append(currentParts, &genai.Part{
						Text:             block.Reasoning.Text,
						Thought:          true,
						ThoughtSignature: block.Reasoning.Signature,
					})
				}

			case ContentBlockTypeToolUse:
				if block.ToolUse != nil {
					args := make(map[string]any)
					if block.ToolUse.Arguments != "" {
						if err := json.Unmarshal([]byte(block.ToolUse.Arguments), &args); err != nil {
							args = map[string]any{"invalid_json_stringified": block.ToolUse.Arguments}
						}
					}

					thoughtSignature := block.ToolUse.Signature
					if isReasoningModel && len(block.ToolUse.Signature) == 0 {
						thoughtSignature = []byte("skip_thought_signature_validator")
					}

					currentParts = append(currentParts, &genai.Part{
						FunctionCall: &genai.FunctionCall{
							ID:   block.ToolUse.Id,
							Name: block.ToolUse.Name,
							Args: args,
						},
						ThoughtSignature: thoughtSignature,
					})
				}

			case ContentBlockTypeToolResult:
				if block.ToolResult != nil {
					// Tool results must be in user role
					if currentRole != "user" {
						addContent()
						currentParts = nil
						currentRole = "user"
					}

					functionResponse := genai.FunctionResponse{
						ID:   block.ToolResult.ToolCallId,
						Name: block.ToolResult.Name,
					}
					if block.ToolResult.IsError {
						functionResponse.Response = map[string]any{"error": block.ToolResult.Text}
					} else {
						functionResponse.Response = map[string]any{"output": block.ToolResult.Text}
					}
					currentParts = append(currentParts, &genai.Part{
						FunctionResponse: &functionResponse,
					})
				}

			case ContentBlockTypeImage, ContentBlockTypeFile:
				log.Warn().Str("type", string(block.Type)).Msg("unsupported content block type for Google provider, skipping")
			}
		}
	}

	addContent()
	return contents
}

func googleFromLlm2Tools(tools []*common.Tool) []*genai.Tool {
	if len(tools) == 0 {
		return nil
	}

	genaiTool := &genai.Tool{
		FunctionDeclarations: utils.Map(tools, func(tool *common.Tool) *genai.FunctionDeclaration {
			return &genai.FunctionDeclaration{
				Name:        tool.Name,
				Description: tool.Description,
				Parameters:  googleFromLlm2Schema(tool.Parameters),
			}
		}),
	}
	return []*genai.Tool{genaiTool}
}

func googleFromLlm2Schema(schema *jsonschema.Schema) *genai.Schema {
	if schema == nil {
		return nil
	}

	geminiSchema := &genai.Schema{
		Type:        genai.Type(schema.Type),
		Description: schema.Description,
		Required:    schema.Required,
	}

	if schema.Enum != nil {
		geminiSchema.Enum = make([]string, 0, len(schema.Enum))
		for _, v := range schema.Enum {
			geminiSchema.Enum = append(geminiSchema.Enum, fmt.Sprintf("%v", v))
		}
	}

	if schema.Properties != nil {
		geminiSchema.Properties = make(map[string]*genai.Schema)
		for pair := schema.Properties.Oldest(); pair != nil; pair = pair.Next() {
			geminiSchema.Properties[pair.Key] = googleFromLlm2Schema(pair.Value)
		}
	}

	if schema.Items != nil {
		geminiSchema.Items = googleFromLlm2Schema(schema.Items)
	}

	return geminiSchema
}
