package persisted_ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sidekick/common"
	"sidekick/domain"
	"sidekick/flow_action"
	"sidekick/llm"
	"sidekick/llm2"
	"sidekick/utils"
	"time"

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

// ErrLLMRefusal indicates the model explicitly refused to respond, returning a
// refusal content block instead of the required tool call. Retrying will not
// elicit a tool call, so callers can check for this via errors.Is to handle
// refusals distinctly from other failures.
var ErrLLMRefusal = errors.New("llm refused to respond")

// responseHasRefusal reports whether the response's message contains a refusal
// content block.
func responseHasRefusal(response common.MessageResponse) bool {
	if response == nil {
		return false
	}
	var msg llm2.Message
	switch m := response.GetMessage().(type) {
	case *llm2.Message:
		msg = *m
	case llm2.Message:
		msg = m
	default:
		return false
	}
	for _, block := range msg.Content {
		if block.Type == llm2.ContentBlockTypeRefusal {
			return true
		}
	}
	return false
}

// forceToolCallV2 forces the LLM to produce a tool call using the given
// ChatHistoryContainer and delegates to ExecuteChatStream for LLM calls.
// parallelToolCalls, when non-nil, explicitly enables/disables parallel tool
// calls for providers that support the toggle.
// Returns common.MessageResponse which provides GetMessage().GetToolCalls() for accessing tool calls.
func forceToolCallV2(
	actionCtx flow_action.ActionContext,
	trackOptions flow_action.TrackOptions,
	modelConfig common.ModelConfig,
	chatHistory *ChatHistoryContainer,
	toolNameMapping *ToolNameMappingConfig,
	parallelToolCalls *bool,
	tools ...*llm.Tool,
) (common.MessageResponse, error) {

	toolChoice := llm.ToolChoice{
		Type: llm.ToolChoiceTypeRequired,
	}
	if len(tools) == 1 {
		toolChoice.Type = llm.ToolChoiceTypeTool
		toolChoice.Name = tools[0].Name
	}

	streamInput := StreamInput{
		Options: llm2.Options{
			ModelConfig:       modelConfig,
			Tools:             tools,
			ToolChoice:        toolChoice,
			ParallelToolCalls: parallelToolCalls,
		},
		Secrets:     *actionCtx.Secrets,
		ChatHistory: chatHistory,
		WorkspaceId: actionCtx.WorkspaceId,
		FlowId:      workflow.GetInfo(actionCtx).WorkflowExecution.ID,
		Providers:   actionCtx.Providers,
	}

	for k, v := range streamInput.ActionParams() {
		actionCtx.ActionParams[k] = v
	}
	response, err := flow_action.TrackWithOptions(actionCtx, trackOptions, func(trackedActionCtx flow_action.ActionContext, flowAction *domain.FlowAction) (common.MessageResponse, error) {
		streamInput.FlowActionId = flowAction.Id

		msgResponse, err := ExecuteChatStream(trackedActionCtx, streamInput, toolNameMapping)
		if err != nil {
			return nil, err
		}

		return msgResponse, nil
	})

	// Append the response to chat history
	if err == nil {
		if appendErr := AppendChatHistory(actionCtx.ExecContext, chatHistory, response.GetMessage()); appendErr != nil {
			return nil, appendErr
		}
	}

	// A refusal is a definitive response; retrying won't produce a tool call, so
	// surface it distinctly instead of masking it as a generic failure.
	if err == nil && responseHasRefusal(response) {
		return response, ErrLLMRefusal
	}

	// single retry in case the llm is being dumb and not returning a tool call
	if err == nil && len(response.GetMessage().GetToolCalls()) == 0 {
		retryMsg := common.ChatMessage{
			Role:    common.ChatMessageRoleSystem,
			Content: "Expected a tool call, but didn't get it. Embedding the json in the content is not sufficient. Please use the provided tool(s).",
		}
		if appendErr := AppendChatHistory(actionCtx.ExecContext, chatHistory, retryMsg); appendErr != nil {
			return nil, appendErr
		}

		for k, v := range streamInput.ActionParams() {
			actionCtx.ActionParams[k] = v
		}
		response, err = flow_action.TrackWithOptions(actionCtx, trackOptions, func(trackedActionCtx flow_action.ActionContext, flowAction *domain.FlowAction) (common.MessageResponse, error) {
			streamInput.FlowActionId = flowAction.Id

			msgResponse, err := ExecuteChatStream(trackedActionCtx, streamInput, toolNameMapping)
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
			if appendErr := AppendChatHistory(actionCtx.ExecContext, chatHistory, response.GetMessage()); appendErr != nil {
				return nil, appendErr
			}
		}
	}

	return response, err
}

// ForceToolCallWithTrackOptionsV2 forces a single required tool call, leaving
// parallel tool calls at the provider default.
func ForceToolCallWithTrackOptionsV2(
	actionCtx flow_action.ActionContext,
	trackOptions flow_action.TrackOptions,
	modelConfig common.ModelConfig,
	chatHistory *ChatHistoryContainer,
	toolNameMapping *ToolNameMappingConfig,
	tools ...*llm.Tool,
) (common.MessageResponse, error) {
	return forceToolCallV2(actionCtx, trackOptions, modelConfig, chatHistory, toolNameMapping, nil, tools...)
}

func ForceToolCall(actionCtx flow_action.ActionContext, modelConfig common.ModelConfig, chatHistory *ChatHistoryContainer, tools ...*llm.Tool) (common.MessageResponse, error) {
	return forceToolCallV2(actionCtx, flow_action.TrackOptions{}, modelConfig, chatHistory, nil, nil, tools...)
}

// ForceParallelToolCall forces at least one tool call while explicitly enabling
// parallel tool calls for providers that support the toggle.
func ForceParallelToolCall(actionCtx flow_action.ActionContext, modelConfig common.ModelConfig, chatHistory *ChatHistoryContainer, tools ...*llm.Tool) (common.MessageResponse, error) {
	parallel := true
	return forceToolCallV2(actionCtx, flow_action.TrackOptions{}, modelConfig, chatHistory, nil, &parallel, tools...)
}

// AppendChatHistory appends a message to chat history, using an activity to
// persist for llm2 history or direct append for legacy history.
func AppendChatHistory(eCtx flow_action.ExecContext, chatHistory *ChatHistoryContainer, msg common.Message) error {
	llm2History, ok := chatHistory.History.(*Llm2ChatHistory)
	if !ok {
		chatHistory.Append(msg)
		return nil
	}

	m := MessageFromCommon(msg)

	var cha *ChatHistoryActivities
	var ref *MessageRef
	input := AppendMessageInput{
		FlowId:      llm2History.FlowId(),
		WorkspaceId: llm2History.WorkspaceId(),
		Message:     m,
	}

	version := workflow.GetVersion(eCtx, "append-chat-history-user-retry", workflow.DefaultVersion, 1)
	if version < 1 {
		err := workflow.ExecuteActivity(eCtx, cha.AppendMessage, input).Get(eCtx, &ref)
		if err != nil {
			panic(fmt.Errorf("AppendChatHistory failed: %w", err))
		}
	} else {
		retryEctx := eCtx
		retryEctx.Context = utils.RetryCtx(eCtx.Context, 30, 1*time.Second)
		err := flow_action.PerformActivityWithUserRetry(retryEctx, "append_chat_history", cha.AppendMessage, &ref, input)
		if err != nil {
			return fmt.Errorf("AppendChatHistory failed: %w", err)
		}
	}
	llm2History.AppendRef(*ref)
	return nil
}
