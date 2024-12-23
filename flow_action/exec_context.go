package flow_action

import (
	"sidekick/domain"
	"sidekick/env"
	"sidekick/secret_manager"

	"go.temporal.io/sdk/workflow"
)

// ExecContext encapsulates environment, secret configuration, and workspace
// context necessary for running activities. Most of these items are required
// across all stages of all flows, hence grouping into a single value to pass
// around.
type ExecContext struct {
	workflow.Context
	WorkspaceId  string
	EnvContainer *env.EnvContainer
	Secrets      *secret_manager.SecretManagerContainer
	FlowScope    *FlowScope
}

type ActionContext struct {
	ExecContext
	ActionType   string
	ActionParams map[string]interface{}
}

func (ec *ExecContext) NewActionContext(actionType string) ActionContext {
	return ActionContext{
		ExecContext:  *ec,
		ActionType:   actionType,
		ActionParams: map[string]interface{}{},
	}
}

type FlowScope struct {
	SubflowName        string // TODO /gen Remove this field after migration to Subflow model is complete
	subflowDescription string // TODO /gen Remove this field after migration to Subflow model is complete
	startedSubflow     bool   // TODO /gen Remove this field after migration to Subflow model is complete
	Subflow            *domain.Subflow
}
