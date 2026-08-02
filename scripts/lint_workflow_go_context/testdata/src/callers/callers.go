package callers

import (
	otherworkflow "other/workflow"
	"sidekick/dev"
	"sidekick/flow_action"

	temporal "go.temporal.io/sdk/workflow"
)

func validWorkflowContext(ctx temporal.Context) {
	temporal.Go(ctx, func(temporal.Context) {})
}

func invalidDevContext(dCtx dev.DevContext) {
	temporal.Go(dCtx, func(temporal.Context) {}) // want "workflow.Go parent has type sidekick/dev.DevContext; pass the embedded workflow.Context explicitly"
}

func invalidExecContext(eCtx flow_action.ExecContext) {
	temporal.Go(eCtx, func(temporal.Context) {}) // want "workflow.Go parent has type sidekick/flow_action.ExecContext; pass the embedded workflow.Context explicitly"
}

func invalidActionContext(actionCtx flow_action.ActionContext) {
	temporal.Go(actionCtx, func(temporal.Context) {}) // want "workflow.Go parent has type sidekick/flow_action.ActionContext; pass the embedded workflow.Context explicitly"
}

func invalidDevActionContext(actionCtx dev.DevActionContext) {
	temporal.Go(actionCtx, func(temporal.Context) {}) // want "workflow.Go parent has type sidekick/dev.DevActionContext; pass the embedded workflow.Context explicitly"
}

func validExplicitEmbeddedContext(dCtx dev.DevContext) {
	temporal.Go(dCtx.Context, func(temporal.Context) {})
}

func unrelatedGoIsAllowed(dCtx dev.DevContext) {
	otherworkflow.Go(dCtx, func(interface{}) {})
}
