package dev

import (
	"fmt"
	"sidekick/coding/git"
	"sidekick/domain"
	"sidekick/flow_action"
	"sidekick/llm"

	"go.temporal.io/sdk/workflow"
)

// GetUserResponse wraps TrackHuman and delegates to flow_action.GetUserResponse
func GetUserResponse(actionCtx DevActionContext, req flow_action.RequestForUser) (*flow_action.UserResponse, error) {
	return TrackHuman(actionCtx, func(flowAction *domain.FlowAction) (*flow_action.UserResponse, error) {
		req.FlowActionId = flowAction.Id
		return flow_action.GetUserResponse(actionCtx.ExecContext, req)
	})
}

// GetUserContinue wraps flow_action.GetUserContinue with DevActionContext
func GetUserContinue(dCtx DevContext, prompt string, params map[string]any) error {
	return flow_action.GetUserContinue(dCtx.ExecContext, prompt, params)
}

// GetUserGuidance wraps flow_action.GetUserGuidance with DevActionContext
func GetUserGuidance(dCtx DevContext, guidanceContext string, params map[string]any) (*flow_action.UserResponse, error) {
	return flow_action.GetUserGuidance(dCtx.ExecContext, guidanceContext, params)
}

// GetUserApproval wraps flow_action.GetUserApproval with DevActionContext
func GetUserApproval(dCtx DevContext, approvalType, approvalPrompt string, params map[string]any) (*flow_action.UserResponse, error) {
	return flow_action.GetUserApproval(dCtx.ExecContext, approvalType, approvalPrompt, params)
}

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

func GetUserMergeApproval(
	dCtx DevContext,
	approvalPrompt string,
	requestParams map[string]interface{},
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
	req := flow_action.RequestForUser{
		OriginWorkflowId: workflow.GetInfo(actionCtx).WorkflowExecution.ID,
		Content:          approvalPrompt,
		Subflow:          actionCtx.FlowScope.SubflowName,
		SubflowId:        actionCtx.FlowScope.GetSubflowId(),
		RequestParams:    requestParams,
		RequestKind:      flow_action.RequestKindMergeApproval,
	}
	actionCtx.ActionParams = req.ActionParams()

	mergeApprovalInfo := req.RequestParams["mergeApprovalInfo"].(MergeApprovalParams)
	finalTarget := mergeApprovalInfo.DefaultTargetBranch
	ignoreWhitespace := false

	// Ensure tracking of the flow action within the guidance request
	userResponse, err := TrackHuman(actionCtx, func(flowAction *domain.FlowAction) (*flow_action.UserResponse, error) {
		req.FlowActionId = flowAction.Id

		// Get the initial user response
		currentResponse, err := flow_action.GetUserResponse(actionCtx.ExecContext, req)
		if err != nil {
			return nil, err
		}

		v := workflow.GetVersion(dCtx, "final-merge-response-update-flow-action", workflow.DefaultVersion, 1)

		// handle branch switching and whitespace toggle until final approval/rejection
		for {
			if v < 1 && currentResponse.Approved != nil {
				// backcompat event history
				return currentResponse, nil
			}

			// branch switch or whitespace toggle update
			if currentResponse.Params != nil {
				paramsChanged := false

				if latestTarget, ok := currentResponse.Params["targetBranch"].(string); ok {
					finalTarget = latestTarget
					paramsChanged = true
				}

				if ignoreWhitespaceVal, ok := currentResponse.Params["ignoreWhitespace"].(bool); ok {
					ignoreWhitespace = ignoreWhitespaceVal
					paramsChanged = true
				}

				if paramsChanged {
					// Regenerate the diff with the updated parameters
					var newDiff string
					err = workflow.ExecuteActivity(actionCtx.DevContext, git.GitDiffActivity, dCtx.EnvContainer, git.GitDiffParams{
						Staged:           true,
						ThreeDotDiff:     true,
						BaseBranch:       finalTarget,
						IgnoreWhitespace: ignoreWhitespace,
					}).Get(actionCtx.DevContext, &newDiff)
					if err != nil {
						return nil, fmt.Errorf("failed to generate diff for target branch %s: %v", finalTarget, err)
					}

					// Update the mergeApprovalInfo with the new diff and target branch
					mergeApprovalInfo.Diff = newDiff
					mergeApprovalInfo.DefaultTargetBranch = finalTarget
					req.RequestParams["mergeApprovalInfo"] = mergeApprovalInfo

					// Update the flow action with the new parameters, so the user sees the updated diff and target
					flowAction.ActionParams = req.ActionParams()
					var fa *flow_action.FlowActivities
					err = workflow.ExecuteActivity(actionCtx.DevContext, fa.PersistFlowAction, flowAction).Get(actionCtx.DevContext, nil)
					if err != nil {
						return nil, fmt.Errorf("failed to update flow action params: %v", err)
					}
				}
			}

			// if Approved is non-nil, this isn't just a branch switch update, we're done either approving or rejecting
			if currentResponse.Approved != nil {
				return currentResponse, nil
			}

			// wait for the next user response signal
			selector := workflow.NewNamedSelector(actionCtx.DevContext, "mergeApprovalUserResponseSelector")
			selector.AddReceive(workflow.GetSignalChannel(actionCtx.DevContext, flow_action.SignalNameUserResponse), func(c workflow.ReceiveChannel, more bool) {
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
		TargetBranch: finalTarget,
		Message:      userResponse.Content,
	}, nil
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

		info.Feedback += "\n\n" + userResponse.Content
		info.Type = FeedbackTypeUserGuidance
		return info, nil
	case SkipInfo:
		feedbackInfo := FeedbackInfo{Feedback: userResponse.Content, Type: FeedbackTypeUserGuidance}
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
		feedbackInfo := FeedbackInfo{Feedback: userResponse.Content, Type: FeedbackTypeUserGuidance}
		return feedbackInfo, nil
	case InitialDevStepInfo:
		content := renderAuthorEditBlockInitialDevStepPrompt(dCtx, info.CodeContext, info.Requirements, info.PlanExecution.String(), info.Step.Definition)
		*chatHistory = append(*chatHistory, llm.ChatMessage{
			Role:    llm.ChatMessageRoleUser,
			Content: content,
		})
		feedbackInfo := FeedbackInfo{Feedback: userResponse.Content, Type: FeedbackTypeUserGuidance}
		return feedbackInfo, nil
	default:
		return FeedbackInfo{}, fmt.Errorf("unsupported current prompt info type: %s", currentPromptInfo.GetType())
	}
}
