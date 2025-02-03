package persisted_ai

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sidekick/common"
	"sidekick/domain"
	"sidekick/flow_action"
	"sidekick/llm"
	"sidekick/utils"
	"strings"

	"github.com/google/uuid"
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
func ForceToolCall(actionCtx flow_action.ActionContext, llmConfig common.LLMConfig, params *llm.ToolChatParams, tools ...*llm.Tool) (*llm.ChatMessageResponse, error) {
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
	chatResponse, err := flow_action.Track(actionCtx, func(flowAction domain.FlowAction) (llm.ChatMessageResponse, error) {
		options.FlowActionId = flowAction.Id
		var chatResponse llm.ChatMessageResponse
		err := workflow.ExecuteActivity(utils.LlmHeartbeatCtx(actionCtx), la.ChatStream, options).Get(actionCtx, &chatResponse)

		if err == nil && len(chatResponse.Content) > 0 && len(tools) == 1 {
			if toolCall, ok := ParseForcedToolCall(tools[0], chatResponse.Content); ok {
				chatResponse.Content = ""
				chatResponse.ToolCalls = []llm.ToolCall{toolCall}
				return chatResponse, nil
			}
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

		options.Params.Messages = append(options.Params.Messages, chatResponse.ChatMessage)
		options.Params.Messages = append(options.Params.Messages, llm.ChatMessage{
			Role:    llm.ChatMessageRoleSystem,
			Content: "Expected a tool call, but didn't get it. Embedding the json in the content is not sufficient. Please use the provided tool(s).",
		})
		actionCtx.ActionParams = options.ActionParams()
		chatResponse, err = flow_action.Track(actionCtx, func(flowAction domain.FlowAction) (llm.ChatMessageResponse, error) {
			var chatResponse2 llm.ChatMessageResponse
			options.FlowActionId = flowAction.Id
			err := workflow.ExecuteActivity(utils.LlmHeartbeatCtx(actionCtx), la.ChatStream, options).Get(actionCtx, &chatResponse2)
			if err == nil {
				if len(chatResponse2.Content) > 0 && len(tools) == 1 {
					if toolCall, ok := ParseForcedToolCall(tools[0], chatResponse2.Content); ok {
						chatResponse2.Content = ""
						chatResponse2.ToolCalls = []llm.ToolCall{toolCall}
						return chatResponse2, nil
					}
				}

				if len(chatResponse2.ToolCalls) == 0 {
					return chatResponse2, fmt.Errorf("no tool calls found in llm response")
				}
			}
			return chatResponse2, err
		})
	}

	if err == nil {
		(*params).Messages = append(params.Messages, chatResponse.ChatMessage)
	}
	return &chatResponse, err
}

// o3-mini seems to want to put its tool calls into the content, so we're
// handling that by parsing it into a tool call, in the case we are forcing a
// single tool call. if the content is valid json, we infer it to be the tool
// call arguments
func ParseForcedToolCall(tool *llm.Tool, s string) (llm.ToolCall, bool) {
	parsedJson := map[string]interface{}{}
	err := json.Unmarshal([]byte(s), &parsedJson)
	if err != nil {
		return llm.ToolCall{}, false
	}

	// to avoid max id length errors, generate very short fake unique id string,
	// not in usual uuid format, via base64 encoding of the UUID ID.
	// https://go.dev/play/p/hcKURudLpZ
	idBytes, err := uuid.New().MarshalBinary()
	if err != nil {
		panic(err)
	}
	var escaper = strings.NewReplacer("9", "99", "-", "90", "_", "91")
	id := escaper.Replace(base64.RawURLEncoding.EncodeToString(idBytes))

	return llm.ToolCall{
		Id: "fromcontent_" + id,
		Name: tool.Name,
		Arguments: s,
	}, true
}
