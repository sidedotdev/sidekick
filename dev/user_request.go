package dev

import (
	"fmt"
	"sidekick/domain"
	"sidekick/flow_action"
	"sidekick/llm"
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

// MergeApprovalParams contains parameters specific to merge approval requests
type MergeApprovalParams struct {
	DefaultTargetBranch string `json:"defaultTargetBranch"` // the default target branch, which is to be confirmed/overridden by the user
	SourceBranch        string `json:"sourceBranch"`
	Diff                string `json:"diff"`
}

type MergeApprovalResponse struct {
	Approved     bool   `json:"approved"`
	TargetBranch string `json:"targetBranch"` // actual target branch selected by the user
	Message      string `json:"message"`      // feedback message when not approved
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
	params := map[string]any{
		"requestContent": r.Content,
		"requestKind":    r.RequestKind,
	}
	for k, v := range r.RequestParams {
		params[k] = v
	}
	return params
}

type UserResponse struct {
	TargetWorkflowId string
	Content          string
	Approved         *bool
	Choice           string
	Params           map[string]interface{}
}

func GetUserApproval(dCtx DevContext, approvalType, approvalPrompt string, requestParams map[string]interface{}) (*UserResponse, error) {
	actionType := "approve." + approvalType
	actionCtx := dCtx.NewActionContext("user_request." + actionType)
	if actionCtx.RepoConfig.DisableHumanInTheLoop {
		// auto-approve for now if humans are not in the loop
		// TODO: add a self-review process in this case
		approved := true
		return &UserResponse{Approved: &approved}, nil
	}

	// Create a RequestForUser struct for approval request
	req := RequestForUser{
		OriginWorkflowId: workflow.GetInfo(actionCtx).WorkflowExecution.ID,
		Content:          approvalPrompt,
		Subflow:          actionCtx.FlowScope.SubflowName,
		RequestParams:    requestParams,
		RequestKind:      RequestKindApproval,
	}
	actionCtx.ActionParams = req.ActionParams()

	// Ensure tracking of the flow action within the guidance request
	return TrackHuman(actionCtx, func(flowAction domain.FlowAction) (*UserResponse, error) {
		req.FlowActionId = flowAction.Id
		return GetUserResponse(actionCtx.DevContext, req)
	})
	/*
		if err != nil {
			return UserResponse{}, fmt.Errorf("failed to get user response: %v", err)
		}
		return *userResponse, nil
	*/
}

func GetUserMergeApproval(
	dCtx DevContext,
	approvalPrompt string,
	requestParams map[string]interface{},
	getGitDiff func(dCtx DevContext, baseBranch string) (string, error),
) (MergeApprovalResponse, error) {
	actionCtx := dCtx.NewActionContext("user_request.approve.merge")
	if actionCtx.RepoConfig.DisableHumanInTheLoop {
		// auto-approve for now if humans are not in the loop
		// TODO: add a self-review process in this case
		approved := true
		targetBranch := "main" // TODO: store the startBranch as part of the worktree object when creating it, then retrieve it here
		return MergeApprovalResponse{Approved: approved, TargetBranch: targetBranch}, nil
	}

	// Create a RequestForUser struct for approval request
	req := RequestForUser{
		OriginWorkflowId: workflow.GetInfo(actionCtx).WorkflowExecution.ID,
		Content:          approvalPrompt,
		Subflow:          actionCtx.FlowScope.SubflowName,
		RequestParams:    requestParams,
		RequestKind:      RequestKindMergeApproval,
	}
	actionCtx.ActionParams = req.ActionParams()

	// Ensure tracking of the flow action within the guidance request
	userResponse, err := TrackHuman(actionCtx, func(flowAction domain.FlowAction) (*UserResponse, error) {
		req.FlowActionId = flowAction.Id

		// Get the initial user response
		currentResponse, err := GetUserResponse(actionCtx.DevContext, req)
		if err != nil {
			return nil, err
		}

		// handle branch switching until final approval/rejection
		for {
			if currentResponse.Approved != nil {
				// final approval/rejection
				return currentResponse, nil
			}

			// if Approved is nil, this is a branch switch update
			newTargetBranch, ok := currentResponse.Params["targetBranch"].(string)
			if !ok {
				return nil, fmt.Errorf("targetBranch not found in user response params")
			}

			// Regenerate the diff with the new target branch
			newDiff, err := getGitDiff(actionCtx.DevContext, newTargetBranch)
			if err != nil {
				return nil, fmt.Errorf("failed to generate diff for target branch %s: %v", newTargetBranch, err)
			}

			// Update the mergeApprovalInfo with the new diff and target branch
			mergeApprovalInfo := req.RequestParams["mergeApprovalInfo"].(MergeApprovalParams)
			mergeApprovalInfo.Diff = newDiff
			mergeApprovalInfo.DefaultTargetBranch = newTargetBranch
			req.RequestParams["mergeApprovalInfo"] = mergeApprovalInfo

			// Update the flow action with the new parameters
			flowAction.ActionParams = req.ActionParams()
			var fa *flow_action.FlowActivities
			err = workflow.ExecuteActivity(actionCtx.DevContext, fa.PersistFlowAction, flowAction).Get(actionCtx.DevContext, nil)
			if err != nil {
				return nil, fmt.Errorf("failed to update flow action params: %v", err)
			}

			// wait for the next user response signal
			selector := workflow.NewNamedSelector(actionCtx.DevContext, "mergeApprovalUserResponseSelector")
			selector.AddReceive(workflow.GetSignalChannel(actionCtx.DevContext, SignalNameUserResponse), func(c workflow.ReceiveChannel, more bool) {
				c.Receive(actionCtx.DevContext, &currentResponse)
			})
			selector.Select(actionCtx.DevContext)
		}
	})

	if err != nil {
		return MergeApprovalResponse{}, err
	}

	return MergeApprovalResponse{
		Approved:     *userResponse.Approved,
		TargetBranch: userResponse.Params["targetBranch"].(string),
	}, nil
}

// Generic function for all user request kinds (free-form, multiple-choice,
// approval, etc). It does NOT support disabling human-in-the-loop, that is
// expected to be handled by the caller and never get this far.
func GetUserResponse(dCtx DevContext, req RequestForUser) (*UserResponse, error) {
	if dCtx.RepoConfig.DisableHumanInTheLoop {
		return nil, fmt.Errorf("can't get user response as human-in-the-loop process is disabled")
	}

	// Signal the workflow
	workflowInfo := workflow.GetInfo(dCtx)
	parentWorkflowID := workflowInfo.ParentWorkflowExecution.ID
	req.OriginWorkflowId = workflowInfo.WorkflowExecution.ID
	workflowErr := workflow.SignalExternalWorkflow(dCtx, parentWorkflowID, "", SignalNameRequestForUser, req).Get(dCtx, nil)
	if workflowErr != nil {
		return nil, fmt.Errorf("failed to signal external workflow: %v", workflowErr)
	}

	v := workflow.GetVersion(dCtx, "pause-flow", workflow.DefaultVersion, 1)
	if v == 1 {
		// update the flow status as paused. required if user feedback was requested from
		// within the flow rather than via user intervention to pause it from
		// the outside (both cases flow through this code path), otherwise the
		// flow will appear "pausable" even though it's really just waiting for
		// user response, i.e. paused
		var flow domain.Flow
		err := workflow.ExecuteActivity(dCtx, srv.Activities.GetFlow, dCtx.WorkspaceId, req.OriginWorkflowId).Get(dCtx, &flow)
		if err != nil {
			return nil, fmt.Errorf("failed to get flow: %v", err)
		}
		if flow.Status != domain.FlowStatusPaused {
			flow.Status = domain.FlowStatusPaused
			err := workflow.ExecuteActivity(dCtx, srv.Activities.PersistFlow, flow).Get(dCtx, nil)
			if err != nil {
				return nil, fmt.Errorf("failed to set flow status to paused: %v", err)
			}
		}
	}

	// Wait for the 'userResponse' signal
	var userResponse UserResponse
	selector := workflow.NewNamedSelector(dCtx, "userResponseSelector")
	selector.AddReceive(workflow.GetSignalChannel(dCtx, SignalNameUserResponse), func(c workflow.ReceiveChannel, more bool) {
		c.Receive(dCtx, &userResponse)
	})
	selector.Select(dCtx)

	// NOTE: unpausing of the flow is always done via the complete flow action
	// handler, so it is omitted here

	return &userResponse, nil
}

func GetUserContinue(actionCtx DevActionContext, prompt string, requestParams map[string]any) error {
	if actionCtx.RepoConfig.DisableHumanInTheLoop {
		return nil
	}

	// Create a RequestForUser struct for continue request
	req := RequestForUser{
		OriginWorkflowId: workflow.GetInfo(actionCtx).WorkflowExecution.ID,
		Content:          prompt,
		Subflow:          actionCtx.FlowScope.SubflowName,
		RequestParams:    requestParams,
		RequestKind:      RequestKindContinue,
	}
	actionCtx.ActionParams = req.ActionParams()

	// Ensure tracking of the flow action within the guidance request
	_, err := TrackHuman(actionCtx, func(flowAction domain.FlowAction) (*UserResponse, error) {
		req.FlowActionId = flowAction.Id
		return GetUserResponse(actionCtx.DevContext, req)
	})
	return err
}

func GetUserGuidance(dCtx DevContext, guidanceContext string, requestParams map[string]any) (*UserResponse, error) {
	if dCtx.RepoConfig.DisableHumanInTheLoop {
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

	guidanceRequest := &RequestForUser{
		OriginWorkflowId: workflow.GetInfo(dCtx).WorkflowExecution.ID,
		Subflow:          dCtx.FlowScope.SubflowName,
		Content:          guidanceContext,
		RequestKind:      RequestKindFreeForm,
		RequestParams:    requestParams,
	}

	actionCtx := dCtx.NewActionContext("user_request.guidance")
	actionCtx.ActionParams = guidanceRequest.ActionParams()

	// Ensure tracking of the flow action within the guidance request
	return TrackHuman(actionCtx, func(flowAction domain.FlowAction) (*UserResponse, error) {
		guidanceRequest.FlowActionId = flowAction.Id
		return GetUserResponse(dCtx, *guidanceRequest)
	})
}

// NOTE: this function is only needed due to the poor structure where feedback
// and function calls are incorporated outside of the context where those are
// generated. Once we stop doing that, this function can be removed in favor of
// GetUserGuidance, or at least can be greatly simplified to not take in the
// currentPromptInfo and chatHistory.
//
// before replacing, we'll need a better solution for remembering user feedback too.
func GetUserFeedback(dCtx DevContext, currentPromptInfo PromptInfo, guidanceContext string, chatHistory *[]llm.ChatMessage, requestParams map[string]any) (FeedbackInfo, error) {
	userResponse, err := GetUserGuidance(dCtx, guidanceContext, requestParams)
	if err != nil {
		return FeedbackInfo{}, fmt.Errorf("failed to get user response: %v", err)
	}

	switch info := currentPromptInfo.(type) {
	case FeedbackInfo:
		info.Feedback += "\n\n#START Guidance From the User\n\nBased on all the work done so far and the above feedback, we asked the user to intervene and provide guidance, or fix the problem. Here is what they said about how to move forward from here: " + userResponse.Content + "\n#END Guidance From the User"
		return info, nil
	case SkipInfo:
		feedbackInfo := FeedbackInfo{Feedback: userResponse.Content}
		return feedbackInfo, nil
	case ToolCallResponseInfo:
		// the caller is replacing the prompt info so will lose this unless we
		// append it to chat history
		*chatHistory = append(*chatHistory, llm.ChatMessage{
			Role:       llm.ChatMessageRoleTool,
			Content:    info.Response,
			Name:       info.FunctionName,
			ToolCallId: info.ToolCallId,
			IsError:    info.IsError,
		})
		feedbackInfo := FeedbackInfo{Feedback: userResponse.Content}
		return feedbackInfo, nil
	case InitialDevStepInfo:
		content := renderAuthorEditBlockInitialDevStepPrompt(dCtx, info.CodeContext, info.Requirements, info.PlanExecution.String(), info.Step.Definition)
		*chatHistory = append(*chatHistory, llm.ChatMessage{
			Role:    llm.ChatMessageRoleUser,
			Content: content,
		})
		feedbackInfo := FeedbackInfo{Feedback: userResponse.Content}
		return feedbackInfo, nil
	default:
		return FeedbackInfo{}, fmt.Errorf("unsupported current prompt info type: %s", currentPromptInfo.GetType())
	}
}
