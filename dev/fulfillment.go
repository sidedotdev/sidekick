package dev

import (
	"encoding/json"
	"fmt"
	"sidekick/coding/git"
	"sidekick/common"
	"sidekick/fflag"
	"sidekick/flow_action"
	"sidekick/llm"
	"sidekick/persisted_ai"
	"strings"

	"github.com/invopop/jsonschema"
	"go.temporal.io/sdk/workflow"
)

var determineCriteriaFulfillmentTool = llm.Tool{
	Name:        "determine_criteria_fulfillment",
	Description: "Determines if the criteria have been met based on the given analysis.",
	Parameters:  (&jsonschema.Reflector{DoNotReference: true}).Reflect(&CriteriaFulfillment{}),
}

// CriteriaFulfillment represents whether specific criteria have been met
type CriteriaFulfillment struct {
	WorkDescription string `json:"whatWasActuallyDone" jsonschema:"description=A summary of what was actually done\\, i.e. the work being analyzed. Includes specific details like names & locations etc\\, for future readers to determine exactly what was done. Should be in past tense."`
	Analysis        string `json:"analysis" jsonschema:"description=The analysis based on which the fulfillment of criteria is assessed."`
	IsFulfilled     bool   `json:"isFulfilled" jsonschema:"description=Indicates if the given criteria have been met."`
	//Confidence      int    `json:"confidence" jsonschema:"description=How likely the final is_fulfilled decision is correct\\, from 1 to 5. 1: not sure at all\\, just guessing. 3: somewhat sure. 5: extremely sure."`
	FeedbackMessage string `json:"feedbackMessage,omitempty" jsonschema:"description=Provide this only when the criteria is not fulfilled. It is a short message containing salient details to help someone else doing the work understand and figure out how to fulfill the criteria."`
}

// TODO /gen add a test for this function
func CheckWorkMeetsCriteria(dCtx DevContext, promptInfo CheckWorkInfo) (CriteriaFulfillment, error) {
	// Use GlobalState as single source of truth for base branch, falling back to caller-provided value
	if v := workflow.GetVersion(dCtx, "check-work-global-base-branch", workflow.DefaultVersion, 1); v >= 1 {
		if globalBase := dCtx.ExecContext.GlobalState.GetStringValue(common.KeyCurrentTargetBranch); globalBase != "" {
			promptInfo.BaseBranch = globalBase
		}
	}

	var diff string
	var err error

	ignoreWhitespace := true

	v := workflow.GetVersion(dCtx, "check-work-diff-since-review", workflow.DefaultVersion, 6)
	if v >= 6 && fflag.IsEnabled(dCtx, fflag.CheckEdits) {
		// When CheckEdits is enabled, all current-step work is staged, so
		// git diff --staged captures exactly the relevant changes without
		// including prior committed steps or merged upstream changes.
		//
		// We deliberately re-evaluate the CheckEdits flag below to preserve
		// the exact activity command sequence previously emitted by
		// git.GitDiff (which internally checks the flag again before
		// running GitDiffActivity). This keeps in-flight and completed
		// workflows replay-deterministic with the prior implementation.
		_ = fflag.IsEnabled(dCtx, fflag.CheckEdits)
		err = flow_action.PerformActivityWithUserRetry(dCtx.ExecContext, "Generate git diff", git.GitDiffActivity, &diff, *dCtx.EnvContainer, git.GitDiffParams{Staged: true})
		if err != nil {
			return CriteriaFulfillment{}, fmt.Errorf("failed to get staged git diff: %v", err)
		}
	} else if v >= 4 && promptInfo.BaseBranch != "" && promptInfo.LastReviewTreeHash != "" {
		diff, err = getOwnChangesSinceReview(dCtx, promptInfo.BaseBranch, promptInfo.LastReviewTreeHash, ignoreWhitespace)
		if err != nil {
			workflow.GetLogger(dCtx).Warn("Failed to get own changes since review, falling back to base branch diff", "error", err)
			diff, err = GetGitDiff(dCtx, promptInfo.BaseBranch, ignoreWhitespace)
			if err != nil {
				return CriteriaFulfillment{}, fmt.Errorf("failed to get fallback base branch diff: %v", err)
			}
		}
	} else if v >= 3 && promptInfo.BaseBranch != "" && promptInfo.LastReviewTreeHash != "" {
		diff, err = legacyOwnChangesSinceReviewV3(dCtx, promptInfo.BaseBranch, promptInfo.LastReviewTreeHash, ignoreWhitespace)
		if err != nil {
			workflow.GetLogger(dCtx).Warn("Failed to get own changes since review, falling back to base branch diff", "error", err)
			diff, err = GetGitDiff(dCtx, promptInfo.BaseBranch, ignoreWhitespace)
			if err != nil {
				return CriteriaFulfillment{}, fmt.Errorf("failed to get fallback base branch diff: %v", err)
			}
		}
	} else if v >= 2 && promptInfo.BaseBranch != "" && promptInfo.LastReviewTreeHash != "" {
		diff, err = legacyOwnChangesSinceReview(dCtx, promptInfo.BaseBranch, promptInfo.LastReviewTreeHash, ignoreWhitespace)
		if err != nil {
			workflow.GetLogger(dCtx).Warn("Failed to get own changes since review, falling back to base branch diff", "error", err)
			diff, err = GetGitDiff(dCtx, promptInfo.BaseBranch, ignoreWhitespace)
			if err != nil {
				return CriteriaFulfillment{}, fmt.Errorf("failed to get fallback base branch diff: %v", err)
			}
		}
	} else if v >= 2 && promptInfo.BaseBranch != "" {
		// No last review tree hash available, fall back to full three-dot diff
		diff, err = GetGitDiff(dCtx, promptInfo.BaseBranch, ignoreWhitespace)
		if err != nil {
			return CriteriaFulfillment{}, fmt.Errorf("failed to get three-dot diff: %v", err)
		}
	} else if v >= 1 && promptInfo.LastReviewTreeHash != "" {
		diff, err = getDiffSinceLastReview(dCtx, promptInfo.LastReviewTreeHash, ignoreWhitespace, nil)
		if err != nil {
			workflow.GetLogger(dCtx).Warn("Failed to get diff since last review, falling back to staged diff", "error", err)
			diff, err = git.GitDiff(dCtx.ExecContext)
			if err != nil {
				return CriteriaFulfillment{}, fmt.Errorf("failed to get fallback staged diff: %v", err)
			}
		}
	} else {
		diff, err = git.GitDiff(dCtx.ExecContext)
		if err != nil {
			return CriteriaFulfillment{}, fmt.Errorf("failed to get git diff: %v", err)
		}
	}

	// Summarize diff to fit within 50% of the judging model's context capacity
	summarizeVersion := workflow.GetVersion(dCtx, "summarize-diff-for-fulfillment", workflow.DefaultVersion, 1)
	if summarizeVersion >= 1 && len(diff) > 0 {
		modelConfig := dCtx.GetModelConfig(common.JudgingKey, 0, "default")
		metadata := dCtx.ExecContext.FetchModelMetadata(modelConfig.Provider, modelConfig.Model)
		maxDiffChars := metadata.MaxChars() / 2
		if len(diff) > maxDiffChars {
			embeddingModelConfig := dCtx.ExecContext.GetEmbeddingModelConfig("diff_summarize")
			var summarizedDiff string
			err = workflow.ExecuteActivity(dCtx, SummarizeDiffActivity, SummarizeDiffActivityInput{
				GitDiff:                diff,
				ReviewFeedback:         promptInfo.Requirements,
				EnvContainer:           *dCtx.EnvContainer,
				ModelConfig:            embeddingModelConfig,
				SecretManagerContainer: *dCtx.Secrets,
				MaxChars:               maxDiffChars,
			}).Get(dCtx, &summarizedDiff)
			if err == nil {
				diff = summarizedDiff
			} else {
				diff = diff[:maxDiffChars]
			}
		}
	}

	promptInfo.Work = diff
	if strings.TrimSpace(diff) == "" {
		promptInfo.Work = "git diff is empty: no changes were made."
	}
	fulfillment, err := CheckIfCriteriaFulfilled(dCtx, promptInfo)
	if err == nil {
		// add unique test and review tags to the feedback message, to tag it for easy management of chat history
		if fulfillment.FeedbackMessage != "" && !fulfillment.IsFulfilled {
			fulfillment.FeedbackMessage = testReviewStart + "\n" + fulfillment.FeedbackMessage + "\n" + testReviewEnd
		} else {
			fulfillment.FeedbackMessage = testReviewStart + "\n" + fulfillment.Analysis + "\n" + testReviewEnd
		}
	}

	return fulfillment, err
}

func CheckIfCriteriaFulfilled(dCtx DevContext, promptInfo CheckWorkInfo) (CriteriaFulfillment, error) {
	// new chat history so we can fit a lot of git diff in the context
	// FIXME /gen/req this fails in cases where we figured out that no changes
	// were required to fulfill the requirements (eg already done in previous
	// step), in which case we need more info in the chat history, eg summary of
	// chat, and include that in the CheckWorkInfo struct.
	chatHistory, err := getCriteriaFulfillmentPrompt(dCtx.ExecContext, dCtx.WorkspaceId, dCtx.RepoConfig.EditCode.Hints, promptInfo)
	if err != nil {
		return CriteriaFulfillment{}, err
	}

	modelConfig := dCtx.GetModelConfig(common.JudgingKey, 0, "default")

	var fulfillment CriteriaFulfillment
	attempts := 0
	for {
		// TODO /gen test this, assert it calls the right tool via mock of chat stream method
		actionCtx := dCtx.ExecContext.NewActionContext("check_criteria_fulfillment")
		actionCtx.ActionParams["diffString"] = promptInfo.Work
		toolNameMapping, err := resolveStreamToolNameMapping(actionCtx.ExecContext, modelConfig, *actionCtx.Secrets)
		if err != nil {
			return CriteriaFulfillment{}, fmt.Errorf("failed to resolve tool name mapping: %v", err)
		}
		response, err := persisted_ai.ForceToolCallWithTrackOptionsV2(actionCtx, flow_action.TrackOptions{}, modelConfig, chatHistory, toolNameMapping, &determineCriteriaFulfillmentTool)
		if err != nil {
			return CriteriaFulfillment{}, fmt.Errorf("failed to force tool call: %w", err)
		}
		toolCalls := response.GetMessage().GetToolCalls()
		toolCall := toolCalls[0]
		jsonStr := toolCall.Arguments
		err = json.Unmarshal([]byte(llm.RepairJson(jsonStr)), &fulfillment)
		if err == nil {
			break
		}

		attempts++
		if attempts >= 3 {
			return CriteriaFulfillment{}, fmt.Errorf("%w: %v", llm.ErrToolCallUnmarshal, err)
		}

		// we have an error. get the llm to self-correct with the error message
		newMessage := common.ChatMessage{
			IsError:    true,
			Role:       common.ChatMessageRoleTool,
			Content:    err.Error(),
			Name:       toolCall.Name,
			ToolCallId: toolCall.Id,
		}
		if err := AppendChatHistory(dCtx.ExecContext, chatHistory, newMessage); err != nil {
			return CriteriaFulfillment{}, err
		}
	}
	return fulfillment, nil
}

func getCriteriaFulfillmentPrompt(eCtx flow_action.ExecContext, workspaceId string, editCodeHints string, promptInfo CheckWorkInfo) (*persisted_ai.ChatHistoryContainer, error) {
	chatHistory := NewVersionedChatHistory(eCtx, workspaceId)

	data := map[string]interface{}{
		"editCodeHints":  editCodeHints,
		"requirements":   promptInfo.Requirements,
		"previousReview": promptInfo.PreviousReview,
		"work":           promptInfo.Work,
		"autoChecks":     promptInfo.AutoChecks,
	}

	var content string
	switch {
	case promptInfo.ResolvingMergeConflicts:
		content = RenderPrompt(FulfillmentConflictResolution, data)
	case promptInfo.Step.Definition != "":
		data["planContext"] = promptInfo.PlanExecution.String()
		data["currentStep"] = promptInfo.Step.Definition
		data["completionCriteria"] = promptInfo.Step.CompletionAnalysis
		content = RenderPrompt(FulfillmentInitialWithPlan, data)
	default:
		content = RenderPrompt(FulfillmentInitial, data)
	}

	newMessage := llm.ChatMessage{
		Role:        llm.ChatMessageRoleUser,
		Content:     content,
		ContextType: ContextTypeInitialInstructions,
	}
	if err := AppendChatHistory(eCtx, chatHistory, newMessage); err != nil {
		return nil, err
	}
	return chatHistory, nil
}
