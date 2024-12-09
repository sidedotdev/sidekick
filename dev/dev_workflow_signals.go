package dev

import (
	"fmt"

	"go.temporal.io/sdk/workflow"
)

// TODO move to common dev constants file
const SignalNameRequestForUser = "requestForUser"
const SignalNameUserResponse = "userResponse"
const SignalNameWorkflowClosed = "workflowClosed"

type WorkflowClosure struct {
	FlowId string
	Reason string // reasons are: https://docs.temporal.io/workflows#closed
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
