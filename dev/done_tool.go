package dev

import (
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
