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
	Description: "Call this tool ONLY when ALL necessary edits and actions have been fully completed (or you've confirmed none are needed after thorough analysis). Do NOT call this after merely answering a question, responding to feedback, or completing only part of the work; continue working until the full scope is finished. Only call this on its own without any other tool calls or edit blocks in the same response.",
	Parameters:  (&jsonschema.Reflector{DoNotReference: true}).Reflect(&DoneArguments{}),
}

var doneToolWithPlan = llm.Tool{
	Name:        "done",
	Description: "Call this tool ONLY when ALL work for the current step has been fully completed — meaning all required edits and actions have been made (or you've confirmed none are needed after thorough analysis). Do NOT call this after merely answering a question, gathering information, or completing a sub-task; the step is not done until its full scope of work is finished. Only call this on its own without any other tool calls or edit blocks in the same response.",
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
