package dev

import (
	"encoding/json"
	"sidekick/domain"
	"sidekick/llm"

	"github.com/invopop/jsonschema"
)

type DoneArguments struct {
	Summary string `json:"summary" jsonschema:"description=A summary of the changes made and the reasoning behind them."`
}

var doneTool = llm.Tool{
	Name:        "done",
	Description: "Call this tool when you have completed all necessary edits and are ready to finish. This signals that no further changes are needed. You must provide a summary of what was done.",
	Parameters:  (&jsonschema.Reflector{DoNotReference: true}).Reflect(&DoneArguments{}),
}

func handleDoneToolCall(dCtx DevContext, toolCall llm.ToolCall) (ToolCallResponseInfo, error) {
	toolCallResult := ToolCallResponseInfo{
		FunctionName: toolCall.Name,
		ToolCallId:   toolCall.Id,
	}

	actionParams := make(map[string]interface{})
	err := json.Unmarshal([]byte(llm.RepairJson(toolCall.Arguments)), &actionParams)
	if err != nil {
		toolCallResult.IsError = true
		toolCallResult.Response = "Failed to parse done tool arguments: " + err.Error()
		return toolCallResult, err
	}

	actionCtx := dCtx.NewActionContext("tool_call.done")
	actionCtx.ActionParams = actionParams

	return Track(actionCtx, func(flowAction *domain.FlowAction) (ToolCallResponseInfo, error) {
		toolCallResult.Response = "Acknowledged. Completing the edit session."
		return toolCallResult, nil
	})
}
