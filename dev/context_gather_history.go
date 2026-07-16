package dev

import (
	"fmt"
	"sidekick/common"
	"sidekick/llm2"
	"sidekick/persisted_ai"
	"strings"
)

const contextGatherReferencePrefix = "call_"

type contextGatherReferenceArgs struct {
	References []string `json:"references"`
}

type contextGatherReferenceState struct {
	RetainedReferences  []string `json:"retainedReferences"`
	ForgottenReferences []string `json:"forgottenReferences"`
}

type contextGatherExchange struct {
	ToolCallId string
	ToolName   string
}

type contextGatherHistory struct {
	history           *persisted_ai.ChatHistoryContainer
	forgettingEnabled bool
	nextReference     int
	exchanges         map[string]contextGatherExchange
	referenceOrder    []string
	forgotten         map[string]bool
	llm2Transcript    []llm2.Message
}

func newContextGatherHistory(history *persisted_ai.ChatHistoryContainer, forgettingEnabled bool) *contextGatherHistory {
	return &contextGatherHistory{
		history:           history,
		forgettingEnabled: forgettingEnabled,
		exchanges:         make(map[string]contextGatherExchange),
		forgotten:         make(map[string]bool),
	}
}

func (h *contextGatherHistory) registerContextToolResult(result *llm2.ToolResultBlock) (string, error) {
	if result == nil {
		return "", fmt.Errorf("context tool result is required")
	}
	if result.ToolCallId == "" {
		return "", fmt.Errorf("context tool result has no tool call ID")
	}
	for _, exchange := range h.exchanges {
		if exchange.ToolCallId == result.ToolCallId {
			return "", fmt.Errorf("context tool call %q already has a reference", result.ToolCallId)
		}
	}

	h.nextReference++
	reference := fmt.Sprintf("%s%d", contextGatherReferencePrefix, h.nextReference)
	h.exchanges[reference] = contextGatherExchange{
		ToolCallId: result.ToolCallId,
		ToolName:   result.Name,
	}
	h.referenceOrder = append(h.referenceOrder, reference)

	result.Content = append(result.Content, llm2.ContentBlock{
		Type: llm2.ContentBlockTypeText,
		Text: "Context reference: " + reference,
	})
	return reference, nil
}

func (h *contextGatherHistory) forgetReferences(references []string) (contextGatherReferenceState, error) {
	if !h.forgettingEnabled {
		return h.referenceState(), fmt.Errorf("forget is not available")
	}
	if err := h.validateReferenceChange(references, false); err != nil {
		return h.referenceState(), err
	}
	for _, reference := range references {
		h.forgotten[reference] = true
	}
	return h.referenceState(), nil
}

func (h *contextGatherHistory) rememberReferences(references []string) (contextGatherReferenceState, error) {
	if !h.forgettingEnabled {
		return h.referenceState(), fmt.Errorf("remember is not available")
	}
	if err := h.validateReferenceChange(references, true); err != nil {
		return h.referenceState(), err
	}
	for _, reference := range references {
		delete(h.forgotten, reference)
	}
	return h.referenceState(), nil
}

func (h *contextGatherHistory) validateReferenceChange(references []string, mustBeForgotten bool) error {
	if len(references) == 0 {
		return fmt.Errorf("at least one context reference is required")
	}

	seen := make(map[string]bool, len(references))
	for _, reference := range references {
		if seen[reference] {
			return fmt.Errorf("context reference %q was provided more than once", reference)
		}
		seen[reference] = true

		if _, ok := h.exchanges[reference]; !ok {
			return fmt.Errorf("unknown context reference %q", reference)
		}
		if mustBeForgotten && !h.forgotten[reference] {
			return fmt.Errorf("context reference %q is not scheduled for forgetting", reference)
		}
		if !mustBeForgotten && h.forgotten[reference] {
			return fmt.Errorf("context reference %q is already scheduled for forgetting", reference)
		}
	}
	return nil
}

func (h *contextGatherHistory) referenceState() contextGatherReferenceState {
	state := contextGatherReferenceState{
		RetainedReferences:  make([]string, 0, len(h.referenceOrder)),
		ForgottenReferences: make([]string, 0, len(h.forgotten)),
	}
	for _, reference := range h.referenceOrder {
		if h.forgotten[reference] {
			state.ForgottenReferences = append(state.ForgottenReferences, reference)
		} else {
			state.RetainedReferences = append(state.RetainedReferences, reference)
		}
	}
	return state
}

func (h *contextGatherHistory) filterForCoding() error {
	if h.history == nil || h.history.History == nil {
		return nil
	}

	retained := h.retainedToolCallIds()
	switch history := h.history.History.(type) {
	case *persisted_ai.LegacyChatHistory:
		h.history.History = persisted_ai.NewLegacyChatHistoryFromChatMessages(
			filterLegacyContextGatherMessages(history.Messages(), retained),
		)
	case *persisted_ai.Llm2ChatHistory:
		h.llm2Transcript = filterLlm2ContextGatherMessages(h.llm2Transcript, retained)
		history.SetMessages(h.llm2Transcript)
	default:
		return fmt.Errorf("unsupported chat history type %T", h.history.History)
	}
	return nil
}

func (h *contextGatherHistory) retainedToolCallIds() map[string]bool {
	retained := make(map[string]bool, len(h.exchanges))
	for reference, exchange := range h.exchanges {
		if !h.forgotten[reference] {
			retained[exchange.ToolCallId] = true
		}
	}
	return retained
}

func filterLegacyContextGatherMessages(messages []common.Message, retained map[string]bool) []common.ChatMessage {
	complete := completeLegacyContextToolCallIds(messages, retained)
	filtered := make([]common.ChatMessage, 0, len(messages))

	for _, message := range messages {
		chatMessage, ok := asChatMessage(message)
		if !ok {
			continue
		}

		if len(chatMessage.ToolCalls) > 0 {
			toolCalls := make([]common.ToolCall, 0, len(chatMessage.ToolCalls))
			for _, toolCall := range chatMessage.ToolCalls {
				if complete[toolCall.Id] {
					toolCalls = append(toolCalls, toolCall)
				}
			}
			if len(toolCalls) > 0 {
				chatMessage.Content = ""
				chatMessage.ToolCalls = toolCalls
				filtered = append(filtered, chatMessage)
			}
			continue
		}

		if complete[chatMessage.ToolCallId] {
			filtered = append(filtered, chatMessage)
		}
	}
	return filtered
}

func completeLegacyContextToolCallIds(messages []common.Message, retained map[string]bool) map[string]bool {
	calls := make(map[string]bool, len(retained))
	results := make(map[string]bool, len(retained))
	for _, message := range messages {
		chatMessage, ok := asChatMessage(message)
		if !ok {
			continue
		}
		for _, toolCall := range chatMessage.ToolCalls {
			if retained[toolCall.Id] {
				calls[toolCall.Id] = true
			}
		}
		if retained[chatMessage.ToolCallId] {
			results[chatMessage.ToolCallId] = true
		}
	}
	return intersectToolCallIds(retained, calls, results)
}

func asChatMessage(message common.Message) (common.ChatMessage, bool) {
	switch message := message.(type) {
	case common.ChatMessage:
		return message, true
	case *common.ChatMessage:
		return *message, true
	default:
		return common.ChatMessage{}, false
	}
}

func filterLlm2ContextGatherMessages(messages []llm2.Message, retained map[string]bool) []llm2.Message {
	complete := completeLlm2ContextToolCallIds(messages, retained)
	filtered := make([]llm2.Message, 0, len(messages))

	for _, message := range messages {
		blocks := make([]llm2.ContentBlock, 0, len(message.Content))
		for _, block := range message.Content {
			switch {
			case block.Type == llm2.ContentBlockTypeToolUse &&
				block.ToolUse != nil &&
				complete[block.ToolUse.Id]:
				blocks = append(blocks, block)
			case block.Type == llm2.ContentBlockTypeToolResult &&
				block.ToolResult != nil &&
				complete[block.ToolResult.ToolCallId]:
				blocks = append(blocks, block)
			}
		}
		if len(blocks) > 0 {
			message.Content = blocks
			filtered = append(filtered, message)
		}
	}
	return filtered
}

func completeLlm2ContextToolCallIds(messages []llm2.Message, retained map[string]bool) map[string]bool {
	calls := make(map[string]bool, len(retained))
	results := make(map[string]bool, len(retained))
	for _, message := range messages {
		for _, block := range message.Content {
			if block.Type == llm2.ContentBlockTypeToolUse &&
				block.ToolUse != nil &&
				retained[block.ToolUse.Id] {
				calls[block.ToolUse.Id] = true
			}
			if block.Type == llm2.ContentBlockTypeToolResult &&
				block.ToolResult != nil &&
				retained[block.ToolResult.ToolCallId] {
				results[block.ToolResult.ToolCallId] = true
			}
		}
	}
	return intersectToolCallIds(retained, calls, results)
}

func intersectToolCallIds(retained, calls, results map[string]bool) map[string]bool {
	complete := make(map[string]bool, len(retained))
	for toolCallId := range retained {
		if calls[toolCallId] && results[toolCallId] {
			complete[toolCallId] = true
		}
	}
	return complete
}

func formatContextGatherReferenceState(state contextGatherReferenceState) string {
	return fmt.Sprintf(
		"Retained context references: %s\nScheduled for forgetting: %s",
		formatContextGatherReferences(state.RetainedReferences),
		formatContextGatherReferences(state.ForgottenReferences),
	)
}

func formatContextGatherReferences(references []string) string {
	if len(references) == 0 {
		return "none"
	}
	return strings.Join(references, ", ")
}
