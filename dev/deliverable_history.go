package dev

import (
	"encoding/json"

	"sidekick/common"
	"sidekick/flow_action"
	"sidekick/llm"
	"sidekick/llm2"
	"sidekick/persisted_ai"

	"go.temporal.io/sdk/workflow"
)

func deliverableHistoryEnabled(eCtx flow_action.ExecContext) bool {
	return workflow.GetVersion(eCtx, "deliverable-history", workflow.DefaultVersion, 1) == 1
}

func toolDeliverable(name string) (string, string) {
	switch name {
	case recordDevRequirementsTool.Name:
		return "dev_requirements", "record"
	case updateDevRequirementsTool.Name:
		return "dev_requirements", "update"
	case recordDevPlanTool.Name:
		return "dev_plan", "record"
	case updateDevPlanTool.Name:
		return "dev_plan", "update"
	default:
		return "", ""
	}
}

func serializeDeliverableCalls(eCtx flow_action.ExecContext, calls []llm.ToolCall) bool {
	for _, call := range calls {
		if id, _ := toolDeliverable(call.Name); id != "" {
			return deliverableHistoryEnabled(eCtx)
		}
	}
	return false
}

func appendGenerationMessage(eCtx flow_action.ExecContext, history *persisted_ai.ChatHistoryContainer, message common.Message) error {
	if serializeDeliverableCalls(eCtx, message.GetToolCalls()) {
		return nil
	}
	return AppendChatHistory(eCtx, history, message)
}

func handleDeliverableToolCalls(dCtx DevContext, calls []llm.ToolCall, history *persisted_ai.ChatHistoryContainer, handlers map[string]func(DevContext, llm.ToolCall) (llm2.ToolResultBlock, error), options toolCallOptions) ([]llm2.ToolResultBlock, error) {
	message := persisted_ai.MessageFromCommon(options.AssistantMessage)
	var pending []llm2.ContentBlock
	var results []llm2.ToolResultBlock
	callIndex := 0
	for _, block := range message.Content {
		if block.ToolUse == nil {
			pending = append(pending, block)
			continue
		}
		id, operation := toolDeliverable(block.ToolUse.Name)
		if id != "" {
			persisted_ai.SetDeliverable(&block, persisted_ai.DeliverableMetadata{
				ID: id, Operation: operation, Snapshot: operation == "record",
			})
		}
		pending = append(pending, block)
		request := &llm2.Message{Role: llm2.RoleAssistant, Content: pending}
		if err := AppendChatHistory(dCtx.ExecContext, history, request); err != nil {
			return nil, err
		}
		pending = nil
		singleOptions := options
		singleOptions.requestsAppended = true
		output, err := handleToolCallsWithOptions(dCtx, calls[callIndex:callIndex+1], history, handlers, singleOptions)
		if err != nil {
			return nil, err
		}
		results = append(results, output...)
		callIndex++
	}
	if len(pending) > 0 {
		if err := AppendChatHistory(dCtx.ExecContext, history, &llm2.Message{Role: llm2.RoleAssistant, Content: pending}); err != nil {
			return nil, err
		}
	}
	return results, nil
}

func deliverableToolHandlers(handlers map[string]func(DevContext, llm.ToolCall) (llm2.ToolResultBlock, error), id string, snapshot func() any) map[string]func(DevContext, llm.ToolCall) (llm2.ToolResultBlock, error) {
	for name, handler := range handlers {
		_, operation := toolDeliverable(name)
		if operation == "" {
			continue
		}
		handlers[name] = func(dCtx DevContext, call llm.ToolCall) (llm2.ToolResultBlock, error) {
			if !deliverableHistoryEnabled(dCtx.ExecContext) {
				return handler(dCtx, call)
			}
			result, err := handler(dCtx, call)
			meta := persisted_ai.DeliverableMetadata{ID: id, Outcome: "success"}
			if err == nil && !result.IsError && operation == "update" {
				var data []byte
				data, err = json.MarshalIndent(snapshot(), "", "  ")
				if err == nil {
					meta.Snapshot = true
					result.Content = append(result.Content, llm2.TextContentBlocks(string(data))...)
				}
			}
			if err != nil || result.IsError {
				meta.Outcome = "failure"
				result.IsError = true
				if err != nil {
					result.Content = llm2.TextContentBlocks(err.Error())
				}
			}
			result.Name = call.Name
			result.ToolCallId = call.Id
			if len(result.Content) == 0 {
				result.Content = llm2.TextContentBlocks("Recorded.")
			}
			persisted_ai.SetDeliverable(&result.Content[0], meta)
			return result, nil
		}
	}
	return handlers
}

func deliverableApprovalFeedback(dCtx DevContext, result llm2.ToolResultBlock, acknowledgment, feedback string) llm2.ToolResultBlock {
	if !deliverableHistoryEnabled(dCtx.ExecContext) {
		return result
	}
	result.Content = llm2.TextContentBlocks(acknowledgment)
	if feedback != "" {
		block := llm2.ContentBlock{Type: llm2.ContentBlockTypeText, Text: feedback}
		persisted_ai.SetContextType(&block, persisted_ai.ContextTypeUserFeedback)
		result.Content = append(result.Content, block)
	}
	return result
}

func toolResultMessage(trb llm2.ToolResultBlock) *llm2.Message {
	msg := &llm2.Message{Role: llm2.RoleUser}
	var deliverable *persisted_ai.DeliverableMetadata
	if len(trb.Content) > 0 {
		deliverable = persisted_ai.GetDeliverable(trb.Content[0])
	}
	var feedback []llm2.ContentBlock
	if deliverable != nil {
		content := make([]llm2.ContentBlock, 0, len(trb.Content))
		for _, block := range trb.Content {
			if persisted_ai.GetContextType(block) == persisted_ai.ContextTypeUserFeedback {
				feedback = append(feedback, block)
				continue
			}
			metadata := make(map[string]any, len(block.Metadata))
			for key, value := range block.Metadata {
				if key != persisted_ai.MetadataNamespaceSidekick {
					metadata[key] = value
				}
			}
			block.Metadata = metadata
			content = append(content, block)
		}
		trb.Content = content
	}
	msg.Content = []llm2.ContentBlock{{Type: llm2.ContentBlockTypeToolResult, ToolResult: &trb}}
	if deliverable != nil {
		persisted_ai.SetDeliverable(&msg.Content[0], *deliverable)
		msg.Content = append(msg.Content, feedback...)
	}
	allHuman := len(trb.Content) > 0
	for _, block := range trb.Content {
		if persisted_ai.GetContextType(block) != ContextTypeUserFeedback {
			allHuman = false
			break
		}
	}
	if allHuman {
		// Legacy conversion reads context metadata from the outer block.
		persisted_ai.SetContextType(&msg.Content[0], ContextTypeUserFeedback)
	}
	return msg
}
