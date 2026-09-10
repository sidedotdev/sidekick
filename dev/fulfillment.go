package dev

import (
	"encoding/json"
	"fmt"
	"sidekick/coding"
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

// criteriaFulfillmentReviewDiff generates the diff the auto-reviewer judges:
// what changed since the last review when there was one, otherwise everything
// since the start point.
//
// The activity degrades comparison trouble internally, so an error here means
// git itself failed, which user-prompted retries can't resolve. We therefore
// skip such retries and fall back to the full diff so review keeps progressing.
// It also reports the full diff since the start point, which callers reviewing
// the same work repeatedly carry forward as the baseline of the next round.
func criteriaFulfillmentReviewDiff(dCtx DevContext, promptInfo CheckWorkInfo, ignoreWhitespace bool) (reviewDiff string, fullDiff string, err error) {
	startPoint := promptInfo.StartPoint
	if startPoint == "" {
		startPoint = promptInfo.BaseBranch
	}
	fallbackBase := promptInfo.BaseBranch
	if fallbackBase == "" {
		fallbackBase = startPoint
	}

	var ca *coding.CodingActivities
	var diffs coding.GenerateReviewDiffsResult
	err = workflow.ExecuteActivity(dCtx, ca.GenerateReviewDiffsActivity, coding.GenerateReviewDiffsParams{
		EnvContainer:     *dCtx.EnvContainer,
		StartPoint:       startPoint,
		BaseBranch:       promptInfo.BaseBranch,
		PriorReviewDiff:  promptInfo.LastReviewDiff,
		IgnoreWhitespace: ignoreWhitespace,
	}).Get(dCtx, &diffs)
	if err != nil {
		workflow.GetLogger(dCtx).Warn("Failed to generate review diffs, falling back to base branch diff", "error", err)
		fallbackDiff, fallbackErr := GetGitDiff(dCtx, fallbackBase, ignoreWhitespace)
		if fallbackErr != nil {
			return "", "", fmt.Errorf("failed to get fallback base branch diff: %v", fallbackErr)
		}
		return fallbackDiff, fallbackDiff, nil
	}

	if promptInfo.LastReviewDiff != "" && diffs.SinceDiffError == "" {
		return diffs.SinceDiff, diffs.FullDiff, nil
	}
	return diffs.FullDiff, diffs.FullDiff, nil
}

// TODO /gen add a test for this function
func CheckWorkMeetsCriteria(dCtx DevContext, promptInfo CheckWorkInfo) (CriteriaFulfillment, error) {
	return checkWorkMeetsCriteria(dCtx, promptInfo, nil)
}

// CheckWorkMeetsCriteriaWithDiff additionally reports the full diff since the
// start point that the review was based on. Flows that review the same work over
// several rounds carry it forward so the next round only covers what changed
// after this one.
func CheckWorkMeetsCriteriaWithDiff(dCtx DevContext, promptInfo CheckWorkInfo) (CriteriaFulfillment, string, error) {
	var reviewedFullDiff string
	fulfillment, err := checkWorkMeetsCriteria(dCtx, promptInfo, &reviewedFullDiff)
	return fulfillment, reviewedFullDiff, err
}

// reviewedFullDiff, when non-nil, receives the full diff since the start point
// that the review was based on, which is only available once review diffs are
// generated rather than derived from legacy git object comparisons.
func checkWorkMeetsCriteria(dCtx DevContext, promptInfo CheckWorkInfo, reviewedFullDiff *string) (CriteriaFulfillment, error) {
	// Use GlobalState as single source of truth for base branch, falling back
	// to caller-provided value. A caller that pinned a start point already
	// decided what the work is compared against, including whether the base
	// branch says anything about it at all, so its choice stands.
	if v := workflow.GetVersion(dCtx, "check-work-global-base-branch", workflow.DefaultVersion, 1); v >= 1 && promptInfo.StartPoint == "" {
		if globalBase := dCtx.ExecContext.GlobalState.GetStringValue(common.KeyCurrentTargetBranch); globalBase != "" {
			promptInfo.BaseBranch = globalBase
		}
	}

	var diff string
	var err error

	ignoreWhitespace := true

	v := workflow.GetVersion(dCtx, "check-work-diff-since-review", workflow.DefaultVersion, 7)
	if v >= 7 {
		if promptInfo.StartPoint == "" && promptInfo.BaseBranch == "" {
			// nothing to diff against, so the current working state is the work
			err = flow_action.PerformActivityWithUserRetry(dCtx.ExecContext, "Generate git diff", git.GitDiffActivity, &diff, *dCtx.EnvContainer, git.GitDiffParams{Staged: true})
			if err != nil {
				return CriteriaFulfillment{}, fmt.Errorf("failed to get git diff: %v", err)
			}
		} else {
			var fullDiff string
			diff, fullDiff, err = criteriaFulfillmentReviewDiff(dCtx, promptInfo, ignoreWhitespace)
			if err != nil {
				return CriteriaFulfillment{}, err
			}
			if reviewedFullDiff != nil {
				*reviewedFullDiff = fullDiff
			}
		}
	} else if v == 6 && fflag.IsEnabled(dCtx, fflag.CheckEdits) {
		// Legacy path: reviews relied on CheckEdits staging the current step's
		// work, which misses anything the agent committed and anything merged
		// in from the target branch. Generated review diffs cover those cases,
		// so this remains only to keep older workflows replay-deterministic.
		//
		// We deliberately re-evaluate the CheckEdits flag below to preserve
		// the exact activity command sequence previously emitted by
		// git.GitDiff (which internally checks the flag again before
		// running GitDiffActivity).
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
	pairedReview := workflow.GetVersion(dCtx, "criteria-user-review-context", workflow.DefaultVersion, 1) >= 1 && promptInfo.LastReviewDiff != ""
	summarizeVersion := workflow.GetVersion(dCtx, "summarize-diff-for-fulfillment", workflow.DefaultVersion, 1)
	if pairedReview {
		diff = prepareUserReviewWork(dCtx, promptInfo, diff)
	} else if summarizeVersion >= 1 && len(diff) > 0 {
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

// Each component has its own budget because an interdiff is not meaningful
// without the work the user originally reviewed.
func prepareUserReviewWork(dCtx DevContext, info CheckWorkInfo, reviewDiff string) string {
	modelConfig := dCtx.GetModelConfig(common.JudgingKey, 0, "default")
	metadata := dCtx.ExecContext.FetchModelMetadata(modelConfig.Provider, modelConfig.Model)
	budget := metadata.MaxChars() / 4
	summarize := func(diff string) string {
		if len(diff) <= budget {
			return diff
		}
		var summary string
		err := workflow.ExecuteActivity(dCtx, SummarizeDiffActivity, SummarizeDiffActivityInput{
			GitDiff:                diff,
			ReviewFeedback:         info.Requirements,
			EnvContainer:           *dCtx.EnvContainer,
			ModelConfig:            dCtx.ExecContext.GetEmbeddingModelConfig("diff_summarize"),
			SecretManagerContainer: *dCtx.Secrets,
			MaxChars:               budget,
		}).Get(dCtx, &summary)
		if err == nil && strings.TrimSpace(summary) != "" {
			diff = summary
		}
		if len(diff) > budget {
			diff = diff[:budget]
		}
		return diff
	}
	original := summarize(info.LastReviewDiff)
	current := "No changes since the rejected user review."
	if strings.TrimSpace(reviewDiff) != "" {
		current = summarize(reviewDiff)
	}
	return "# Original diff from rejected user review\n\n" + original +
		"\n\n# Review changes (full current diff if incremental generation was unavailable)\n\n" + current
}
