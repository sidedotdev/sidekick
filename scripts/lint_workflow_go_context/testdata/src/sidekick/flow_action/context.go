package flow_action

import "go.temporal.io/sdk/workflow"

type ExecContext struct {
	workflow.Context
}

type ActionContext struct {
	ExecContext
}
