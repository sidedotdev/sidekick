package flow_action

import (
	"fmt"
	"sidekick/domain"
	"sidekick/srv"

	"go.temporal.io/sdk/workflow"
)

type RequestKind string

const (
	RequestKindFreeForm       RequestKind = "free_form"
	RequestKindMultipleChoice RequestKind = "multiple_choice"
	RequestKindApproval       RequestKind = "approval"
	RequestKindMergeApproval  RequestKind = "merge_approval"
	RequestKindContinue       RequestKind = "continue"
)

const (
	SignalNameRequestForUser = "requestForUser"
	SignalNameUserResponse   = "userResponse"
)

type UserResponse struct {
	TargetWorkflowId string
	Content          string
	Approved         *bool
	Choice           string
	Params           map[string]interface{}
}

type RequestForUser struct {
	OriginWorkflowId string
	FlowActionId     string
	Content          string
	Subflow          string
	SubflowId        string
	RequestParams    map[string]interface{}
	RequestKind      RequestKind
}

func (r RequestForUser) ActionParams() map[string]any {
	params := map[string]any{
		"requestContent": r.Content,
		"requestKind":    r.RequestKind,
	}
	for k, v := range r.RequestParams {
		params[k] = v
	}
	return params
}

// Generic function for all user request kinds (free-form, multiple-choice,
// approval, etc). It does NOT support disabling human-in-the-loop, that is
// expected to be handled by the caller and never get this far.
func GetUserResponse(ctx ExecContext, req RequestForUser) (*UserResponse, error) {
	if ctx.DisableHumanInTheLoop {
		return nil, fmt.Errorf("can't get user response as human-in-the-loop process is disabled")
	}

	// Signal the workflow
	workflowInfo := workflow.GetInfo(ctx.Context)
	parentWorkflow := workflowInfo.ParentWorkflowExecution
	if parentWorkflow == nil {
		return nil, fmt.Errorf("failed to signal external workflow: no parent workflow found")
	}
	req.OriginWorkflowId = workflowInfo.WorkflowExecution.ID
	workflowErr := workflow.SignalExternalWorkflow(ctx.Context, parentWorkflow.ID, "", SignalNameRequestForUser, req).Get(ctx.Context, nil)
	if workflowErr != nil {
		return nil, fmt.Errorf("failed to signal external workflow: %v", workflowErr)
	}

	v := workflow.GetVersion(ctx.Context, "pause-flow", workflow.DefaultVersion, 1)
	if v == 1 {
		// update the flow status as paused. required if user feedback was requested from
		// within the flow rather than via user intervention to pause it from
		// the outside (both cases flow through this code path), otherwise the
		// flow will appear "pausable" even though it's really just waiting for
		// user response, i.e. paused
		var flow domain.Flow
		err := workflow.ExecuteActivity(ctx.Context, srv.Activities.GetFlow, ctx.WorkspaceId, req.OriginWorkflowId).Get(ctx.Context, &flow)
		if err != nil {
			return nil, fmt.Errorf("failed to get flow: %v", err)
		}
		if flow.Status != domain.FlowStatusPaused {
			flow.Status = domain.FlowStatusPaused
			err := workflow.ExecuteActivity(ctx.Context, srv.Activities.PersistFlow, flow).Get(ctx.Context, nil)
			if err != nil {
				return nil, fmt.Errorf("failed to set flow status to paused: %v", err)
			}
		}
	}

	// Wait for the 'userResponse' signal
	var userResponse UserResponse
	selector := workflow.NewNamedSelector(ctx.Context, "userResponseSelector")
	selector.AddReceive(workflow.GetSignalChannel(ctx.Context, SignalNameUserResponse), func(c workflow.ReceiveChannel, more bool) {
		c.Receive(ctx.Context, &userResponse)
	})
	selector.Select(ctx.Context)

	// NOTE: unpausing of the flow is always done via the complete flow action
	// handler, so it is omitted here

	return &userResponse, nil
}

func GetUserContinue(eCtx ExecContext, prompt string, requestParams map[string]any) error {
	if eCtx.DisableHumanInTheLoop {
		return nil
	}

	// Create a RequestForUser struct for continue request
	req := RequestForUser{
		OriginWorkflowId: workflow.GetInfo(eCtx).WorkflowExecution.ID,
		Content:          prompt,
		Subflow:          eCtx.FlowScope.SubflowName,
		RequestParams:    requestParams,
		RequestKind:      RequestKindContinue,
	}
	actionCtx := eCtx.NewActionContext("user_request.continue")
	actionCtx.ActionParams = req.ActionParams()

	// Ensure tracking of the flow action within the guidance request
	_, err := TrackHuman(actionCtx, func(flowAction *domain.FlowAction) (*UserResponse, error) {
		req.FlowActionId = flowAction.Id
		return GetUserResponse(actionCtx.ExecContext, req)
	})
	return err
}

func GetUserGuidance(eCtx ExecContext, guidanceContext string, requestParams map[string]any) (*UserResponse, error) {
	if eCtx.DisableHumanInTheLoop {
		return &UserResponse{
			Content: `
Automated response: User guidance is actually not available and will not be in
this case because the human-in-the-loop process has been disabled. Thus there
will be no way to clarify requirements or get other more specific guidance. But
here is some general guidance that should help, as you seem to be stuck going in
circles given that you tripped the threshold number of iterations to engage the
human-in-the-loop process.

1. If requirements are unclear, make a best guess at what the requirements are
asking for.
2. If you are solving a problem or debugging an error, make use of lateral
thinking and try something completely new from what you were doing before.

Randomly select and apply one of these techniques: TRIZ principles, SCAMPER, or
oblique strategies, to explore diverse approaches for creatively solving the
problem at hand. First choose one technique, then consider step by step what it
would mean to apply it to your current situation.
		`,
		}, nil
	}

	guidanceRequest := RequestForUser{
		OriginWorkflowId: workflow.GetInfo(eCtx).WorkflowExecution.ID,
		Subflow:          eCtx.FlowScope.SubflowName,
		SubflowId:        eCtx.FlowScope.GetSubflowId(),
		Content:          guidanceContext,
		RequestKind:      RequestKindFreeForm,
		RequestParams:    requestParams,
	}

	actionCtx := eCtx.NewActionContext("user_request.guidance")
	actionCtx.ActionParams = guidanceRequest.ActionParams()

	return TrackHuman(actionCtx, func(flowAction *domain.FlowAction) (*UserResponse, error) {
		guidanceRequest.FlowActionId = flowAction.Id
		response, err := GetUserResponse(actionCtx.ExecContext, guidanceRequest)
		if err == nil && response != nil && response.Content != "" {
			response.Content = "#START Guidance From the User\n\n" + response.Content + "\n#END Guidance From the User"
		}
		return response, err
	})
}

func GetUserApproval(eCtx ExecContext, approvalType, approvalPrompt string, requestParams map[string]interface{}) (*UserResponse, error) {
	actionType := "approve." + approvalType
	actionCtx := eCtx.NewActionContext("user_request." + actionType)

	if actionCtx.DisableHumanInTheLoop {
		// auto-approve for now if humans are not in the loop
		// TODO: add a self-review process in this case
		approved := true
		return &UserResponse{Approved: &approved}, nil
	}

	req := RequestForUser{
		OriginWorkflowId: workflow.GetInfo(actionCtx).WorkflowExecution.ID,
		Content:          approvalPrompt,
		Subflow:          actionCtx.FlowScope.SubflowName,
		SubflowId:        actionCtx.FlowScope.GetSubflowId(),
		RequestParams:    requestParams,
		RequestKind:      RequestKindApproval,
	}
	actionCtx.ActionParams = req.ActionParams()

	return TrackHuman(actionCtx, func(flowAction *domain.FlowAction) (*UserResponse, error) {
		req.FlowActionId = flowAction.Id
		return GetUserResponse(actionCtx.ExecContext, req)
	})
}
