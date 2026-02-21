package dev

import (
	"encoding/json"
	"sidekick/llm"
	"sidekick/llm2"

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

func handleDoneToolCall(dCtx DevContext, toolCall llm.ToolCall) (llm2.ToolResultBlock, error) {
	toolCallResult := llm2.ToolResultBlock{
		Name:       toolCall.Name,
		ToolCallId: toolCall.Id,
	}

	var args DoneArguments
	err := json.Unmarshal([]byte(llm.RepairJson(toolCall.Arguments)), &args)
	if err != nil {
		toolCallResult.IsError = true
		toolCallResult.Content = llm2.TextContentBlocks("Failed to parse done tool arguments: " + err.Error())
		return toolCallResult, err
	}

	toolCallResult.Content = llm2.TextContentBlocks("Continuing to test & review stages.")
	return toolCallResult, nil
}
