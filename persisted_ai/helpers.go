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
	"sidekick/utils"

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

// TODO /gen add a test for this function
// TODO move json unmarshaling from callers into this function, using
// reflect.Zero(tool.ParametersType). And return a slice of ToolCall, where the
// unmarshaled parameters are included in the ToolCall struct as ParsedArguments
// TODO move to persisted_ai package after adding AIConfig to flow_action.ActionContext
func ForceToolCallWithTrackOptions(actionCtx flow_action.ActionContext, trackOptions flow_action.TrackOptions, llmConfig common.LLMConfig, params *llm.ToolChatParams, tools ...*llm.Tool) (*llm.ChatMessageResponse, error) {
	var la *LlmActivities // use a nil struct pointer to call activities that are part of a structure

	if params.ModelConfig.Provider == "" {
		modelConfig, _ := llmConfig.GetModelConfig(common.DefaultKey, 0)
		params.ModelConfig = modelConfig
	}

	options := ChatStreamOptions{
		ToolChatOptions: llm.ToolChatOptions{
			Secrets: *actionCtx.Secrets,
			Params: llm.ToolChatParams{
				Messages:    params.Messages, // TODO use go get dario.cat/mergo
				ModelConfig: params.ModelConfig,
				Temperature: params.Temperature,
				Tools:       tools,
				ToolChoice: llm.ToolChoice{
					Type: llm.ToolChoiceTypeRequired,
				},
			},
		},
	}

	if len(tools) == 1 {
		options.Params.ToolChoice.Type = llm.ToolChoiceTypeTool
		options.Params.ToolChoice.Name = tools[0].Name
	}

	flowId := workflow.GetInfo(actionCtx).WorkflowExecution.ID
	options.WorkspaceId = actionCtx.WorkspaceId
	options.FlowId = flowId
	actionCtx.ActionParams = options.ActionParams()
	chatResponse, err := flow_action.TrackWithOptions(actionCtx, trackOptions, func(flowAction *domain.FlowAction) (llm.ChatMessageResponse, error) {
		options.FlowActionId = flowAction.Id
		var chatResponse llm.ChatMessageResponse
		actionCtx.Context = utils.LlmHeartbeatCtx(actionCtx)
		err := flow_action.PerformWithUserRetry(actionCtx, la.ChatStream, &chatResponse, options)
		if err == nil {
			(*params).Messages = append(params.Messages, chatResponse.ChatMessage)
		}
		return chatResponse, err
	})

	// single retry in case the llm is being dumb and not returning a tool call
	// TODO /gen avoid this additional call: try to parse out the tool call from the
	// content if it's there and matches the function definition
	// TODO /gen also retry if the tool call doesn't match the function
	// parameters defined. the easiest way to do this is to use the
	// tool.UnmarshalInto value reference and json unmarshal into it, and see if
	// that succeeds. Note that this won't check for required fields and some
	// other aspects of the schema. for this case, we need to use
	if err == nil && len(chatResponse.ToolCalls) == 0 {
		(*params).Messages = append(params.Messages, llm.ChatMessage{
			Role:    llm.ChatMessageRoleSystem,
			Content: "Expected a tool call, but didn't get it. Embedding the json in the content is not sufficient. Please use the provided tool(s).",
		})
		options.Params.Messages = params.Messages
		actionCtx.ActionParams = options.ActionParams()
		chatResponse, err = flow_action.TrackWithOptions(actionCtx, trackOptions, func(flowAction *domain.FlowAction) (llm.ChatMessageResponse, error) {
			var chatResponse llm.ChatMessageResponse
			options.FlowActionId = flowAction.Id
			actionCtx.Context = utils.LlmHeartbeatCtx(actionCtx.Context)
			err := flow_action.PerformWithUserRetry(actionCtx, la.ChatStream, &chatResponse, options)
			if err == nil {
				(*params).Messages = append(params.Messages, chatResponse.ChatMessage)

				if len(chatResponse.ToolCalls) == 0 {
					return chatResponse, fmt.Errorf("no tool calls found in llm response")
				}
			}
			return chatResponse, err
		})
	}

	return &chatResponse, err
}

// ForceToolCallWithTrackOptionsV2 is like ForceToolCallWithTrackOptions but works with
// ChatHistoryContainer and delegates to ExecuteChatStream for LLM calls.
// Returns common.MessageResponse which provides GetMessage().GetToolCalls() for accessing tool calls.
func ForceToolCallWithTrackOptionsV2(
	ctx workflow.Context,
	actionCtx flow_action.ActionContext,
	trackOptions flow_action.TrackOptions,
	llmConfig common.LLMConfig,
	chatHistory *llm2.ChatHistoryContainer,
	tools ...*llm.Tool,
) (common.MessageResponse, error) {
	modelConfig, _ := llmConfig.GetModelConfig(common.DefaultKey, 0)

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
		Providers:   actionCtx.Providers,
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

func ForceToolCall(actionCtx flow_action.ActionContext, llmConfig common.LLMConfig, params *llm.ToolChatParams, tools ...*llm.Tool) (*llm.ChatMessageResponse, error) {
	return ForceToolCallWithTrackOptions(actionCtx, flow_action.TrackOptions{}, llmConfig, params, tools...)
}
