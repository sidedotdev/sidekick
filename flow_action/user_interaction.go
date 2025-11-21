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
	Subflow          string // TODO add SubflowId here instead of legacy Subflow (which is the subflow name)
	RequestParams    map[string]interface{}
	RequestKind      RequestKind
}

func (r RequestForUser) ActionParams() map[string]any {
	params := make(map[string]any)
	if r.RequestParams != nil {
		for k, v := range r.RequestParams {
			params[k] = v
		}
	}
	params["kind"] = r.RequestKind
	params["message"] = r.Content
	if r.FlowActionId != "" {
		params["flowActionId"] = r.FlowActionId
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
	parentWorkflowID := workflowInfo.ParentWorkflowExecution.ID
	req.OriginWorkflowId = workflowInfo.WorkflowExecution.ID
	workflowErr := workflow.SignalExternalWorkflow(ctx.Context, parentWorkflowID, "", SignalNameRequestForUser, req).Get(ctx.Context, nil)
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

func GetUserContinue(ctx ExecContext, prompt string, requestParams map[string]any) error {
	if ctx.DisableHumanInTheLoop {
		return nil
	}

	// Create a RequestForUser struct for continue request
	req := RequestForUser{
		OriginWorkflowId: workflow.GetInfo(ctx.Context).WorkflowExecution.ID,
		Content:          prompt,
		Subflow:          ctx.FlowScope.SubflowName,
		RequestParams:    requestParams,
		RequestKind:      RequestKindContinue,
	}

	// TODO: Tracking needs to be implemented in flow_action or accessible.
	// For now, we call GetUserResponse directly without tracking wrapper.
	_, err := GetUserResponse(ctx, req)
	return err
}

func GetUserGuidance(ctx ExecContext, guidanceContext string, requestParams map[string]any) (*UserResponse, error) {
	if ctx.DisableHumanInTheLoop {
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

	// TODO: Handle UserRequestIfPaused if needed.
	// v := workflow.GetVersion(ctx.Context, "guidance-delegate-when-paused", workflow.DefaultVersion, 1)
	// if v == 1 && ctx.GlobalState != nil && ctx.GlobalState.Paused {
	// 	return UserRequestIfPaused(ctx, guidanceContext, requestParams)
	// }

	guidanceRequest := &RequestForUser{
		OriginWorkflowId: workflow.GetInfo(ctx.Context).WorkflowExecution.ID,
		Subflow:          ctx.FlowScope.SubflowName,
		Content:          guidanceContext,
		RequestKind:      RequestKindFreeForm,
		RequestParams:    requestParams,
	}

	// TODO: Tracking
	response, err := GetUserResponse(ctx, *guidanceRequest)
	if err == nil && response != nil && response.Content != "" {
		response.Content = "#START Guidance From the User\n\n" + response.Content + "\n#END Guidance From the User"
	}
	return response, err
}

func GetUserApproval(ctx ExecContext, approvalType, approvalPrompt string, requestParams map[string]interface{}) (*UserResponse, error) {
	if ctx.DisableHumanInTheLoop {
		// auto-approve for now if humans are not in the loop
		// TODO: add a self-review process in this case
		approved := true
		return &UserResponse{Approved: &approved}, nil
	}

	// Create a RequestForUser struct for approval request
	req := RequestForUser{
		OriginWorkflowId: workflow.GetInfo(ctx.Context).WorkflowExecution.ID,
		Content:          approvalPrompt,
		Subflow:          ctx.FlowScope.SubflowName,
		RequestParams:    requestParams,
		RequestKind:      RequestKindApproval,
	}

	// TODO: Tracking
	return GetUserResponse(ctx, req)
}
