package dev

import (
	"errors"
	"fmt"

	"go.temporal.io/sdk/workflow"
)

// TODO move to common dev constants file
const SignalNameRequestForUser = "requestForUser"
const SignalNameUserResponse = "userResponse"
const SignalNameWorkflowClosed = "workflowClosed"
const SignalNamePause = "pause"
const SignalNameUserAction = "userAction"

type WorkflowClosure struct {
	FlowId string
	Reason string // reasons are: https://docs.temporal.io/workflows#closed
}

// signalWorkflowFailureOrCancel signals the parent workflow with "canceled" if
// the context was canceled, otherwise signals "failed".
func signalWorkflowFailureOrCancel(ctx workflow.Context) {
	if errors.Is(ctx.Err(), workflow.ErrCanceled) {
		disconnectedCtx, _ := workflow.NewDisconnectedContext(ctx)
		_ = signalWorkflowClosure(disconnectedCtx, "canceled")
	} else {
		_ = signalWorkflowClosure(ctx, "failed")
	}
}

func signalWorkflowClosure(ctx workflow.Context, reason string) (err error) {
	info := workflow.GetInfo(ctx)
	parentWorkflowID := info.ParentWorkflowExecution.ID
	closure := WorkflowClosure{
		FlowId: info.WorkflowExecution.ID,
		Reason: reason,
	}
	err = workflow.SignalExternalWorkflow(ctx, parentWorkflowID, "", SignalNameWorkflowClosed, closure).Get(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to signal external workflow: %v", err)
	}
	return nil
}
