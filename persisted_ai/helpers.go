package persisted_ai

import (
	"context"
	"encoding/json"
	"fmt"
	"sidekick/common"
	"sidekick/domain"
	"sidekick/flow_action"
	"sidekick/llm"
	"sidekick/llm2"

	"go.temporal.io/sdk/workflow"
)

// TODO remove in favor of ForceToolCall
func GetOpenaiFuncArgs(ctx context.Context, la LlmActivities, toolOptions llm.ToolChatOptions, funcArgs interface{}) error {
	// Constructing the ChatStreamOptions with necessary details
	chatOptions := ChatStreamOptions{
		ToolChatOptions: toolOptions,
		WorkspaceId:     "workspace_id",   // Placeholder as actual value should be provided
		FlowId:          "flow_id",        // Placeholder as actual value should be provided
		FlowActionId:    "flow_action_id", // Placeholder as actual value should be provided
	}
	chatResponse, err := la.ChatStream(ctx, chatOptions)
	if err != nil {
		return err
	}

	jsonStr := chatResponse.ToolCalls[0].Arguments
	return json.Unmarshal([]byte(llm.RepairJson(jsonStr)), funcArgs)
}

// ForceToolCallWithTrackOptionsV2
// ChatHistoryContainer and delegates to ExecuteChatStream for LLM calls.
// Returns common.MessageResponse which provides GetMessage().GetToolCalls() for accessing tool calls.
func ForceToolCallWithTrackOptionsV2(
	ctx workflow.Context,
	actionCtx flow_action.ActionContext,
	trackOptions flow_action.TrackOptions,
	modelConfig common.ModelConfig,
	chatHistory *llm2.ChatHistoryContainer,
	tools ...*llm.Tool,
) (common.MessageResponse, error) {

	toolChoice := llm.ToolChoice{
		Type: llm.ToolChoiceTypeRequired,
	}
	if len(tools) == 1 {
		toolChoice.Type = llm.ToolChoiceTypeTool
		toolChoice.Name = tools[0].Name
	}

	options := ChatStreamOptionsV2{
		Options: llm2.Options{
			Secrets: *actionCtx.Secrets,
			Params: llm2.Params{
				ChatHistory: chatHistory,
				ModelConfig: modelConfig,
				Tools:       tools,
				ToolChoice:  toolChoice,
			},
		},
		WorkspaceId: actionCtx.WorkspaceId,
		FlowId:      workflow.GetInfo(ctx).WorkflowExecution.ID,
	}

	actionCtx.ActionParams = llm.ToolChatOptions{Secrets: options.Secrets, Params: llm.ToolChatParams{ModelConfig: modelConfig}}.ActionParams()
	response, err := flow_action.TrackWithOptions(actionCtx, trackOptions, func(flowAction *domain.FlowAction) (common.MessageResponse, error) {
		options.FlowActionId = flowAction.Id

		msgResponse, err := ExecuteChatStream(ctx, options)
		if err != nil {
			return nil, err
		}

		return msgResponse, nil
	})

	// Append the response to chat history
	if err == nil {
		chatHistory.Append(response.GetMessage())
	}

	// single retry in case the llm is being dumb and not returning a tool call
	if err == nil && len(response.GetMessage().GetToolCalls()) == 0 {
		// Use common.ChatMessage for compatibility with both history types
		retryMsg := common.ChatMessage{
			Role:    common.ChatMessageRoleSystem,
			Content: "Expected a tool call, but didn't get it. Embedding the json in the content is not sufficient. Please use the provided tool(s).",
		}
		chatHistory.Append(retryMsg)

		actionCtx.ActionParams = llm.ToolChatOptions{Secrets: options.Secrets, Params: llm.ToolChatParams{ModelConfig: modelConfig}}.ActionParams()
		response, err = flow_action.TrackWithOptions(actionCtx, trackOptions, func(flowAction *domain.FlowAction) (common.MessageResponse, error) {
			options.FlowActionId = flowAction.Id

			msgResponse, err := ExecuteChatStream(ctx, options)
			if err != nil {
				return nil, err
			}

			if len(msgResponse.GetMessage().GetToolCalls()) == 0 {
				return nil, fmt.Errorf("no tool calls found in llm response")
			}

			return msgResponse, nil
		})

		// Append the retry response to chat history
		if err == nil {
			chatHistory.Append(response.GetMessage())
		}
	}

	return response, err
}

func ForceToolCall(ctx workflow.Context, actionCtx flow_action.ActionContext, modelConfig common.ModelConfig, chatHistory *llm2.ChatHistoryContainer, tools ...*llm.Tool) (common.MessageResponse, error) {
	return ForceToolCallWithTrackOptionsV2(ctx, actionCtx, flow_action.TrackOptions{}, modelConfig, chatHistory, tools...)
}
