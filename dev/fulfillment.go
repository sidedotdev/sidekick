package dev

import (
	"encoding/json"
	"fmt"
	"sidekick/coding/git"
	"sidekick/common"
	"sidekick/llm"
	"sidekick/persisted_ai"
	"strings"

	"github.com/invopop/jsonschema"
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
	var diff string
	var err error

	if promptInfo.LastReviewTreeHash != "" {
		diff, err = getDiffSinceLastReview(dCtx, promptInfo.LastReviewTreeHash, false)
		if err != nil {
			return CriteriaFulfillment{}, fmt.Errorf("failed to get diff since last review: %v", err)
		}
	} else {
		diff, err = git.GitDiff(dCtx.ExecContext)
		if err != nil {
			return CriteriaFulfillment{}, fmt.Errorf("failed to get git diff: %v", err)
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
	chatHistory := getCriteriaFulfillmentPrompt(promptInfo)

	modelConfig := dCtx.GetModelConfig(common.JudgingKey, 0, "default")
	params := llm.ToolChatParams{Messages: *chatHistory, ModelConfig: modelConfig}

	var fulfillment CriteriaFulfillment
	attempts := 0
	for {
		// TODO /gen test this, assert it calls the right tool via mock of chat stream method
		actionCtx := dCtx.ExecContext.NewActionContext("check_criteria_fulfillment")
		chatResponse, err := persisted_ai.ForceToolCall(actionCtx, dCtx.LLMConfig, &params, &determineCriteriaFulfillmentTool)
		*chatHistory = params.Messages // update chat history with the new messages
		if err != nil {
			return CriteriaFulfillment{}, fmt.Errorf("failed to force tool call: %v", err)
		}
		toolCall := chatResponse.ToolCalls[0]
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
		newMessage := llm.ChatMessage{
			IsError:    true,
			Role:       llm.ChatMessageRoleTool,
			Content:    err.Error(),
			Name:       toolCall.Name,
			ToolCallId: toolCall.Id,
		}
		*chatHistory = append(*chatHistory, newMessage)
	}
	return fulfillment, nil
}

func getCriteriaFulfillmentPrompt(promptInfo CheckWorkInfo) *[]llm.ChatMessage {
	chatHistory := &[]llm.ChatMessage{}

	var content string
	if promptInfo.Step.Definition != "" {
		// TODO /gen adjust this to use RenderPrompt, followiing the same pattern as other functions calling RenderPrompt
		content = fmt.Sprintf(`
You are determining if the work done during a specific step of a plan was
completed successfully and fulfills the criteria set for that step.

Thinking step-by-step as a senior software engineer, analyze the following git
diff and determine if the step has been completed correctly and its completion
criteria fulfilled. First output your analysis of the diff against all aspects
of the criteria. In addition, analyze whether the code changes look correct and
maintain previous functionality that should not have been altered as part of the
change (taking into account the latest test results).

Review the latest code changes for any issues. If there are major issues, we
consider that an unsaid criterium for correctness that has not been fulfilled.
If you're not sure if it's an issue or its something you cannot check for (eg
whether an import already existed), assume it is correct unless test results
tell you otherwise. If there are code changes referenced that we got user
feedback on, then only consider the latest user feedback and the latest code,
not the earlier code/requirements.

Finally, output whether the step is complete and criteria are fulfilled or not.
The "is_fulfilled" field should be set to true if and only if the step is
complete and criteria are completely fulfilled. Do not say that the criteria is
not fulfilled if a later step in the plan is clearly intended to fix the issue.

Here is a reminder of the original requirements, the plan execution context,
the current step and completion criteria:

# START REQUIREMENTS
%s
# END REQUIREMENTS

# START PLAN
%s
# END PLAN

# START CURRENT STEP
%s
# END CURRENT STEP

# START Completion Criteria
%s
# END Completion Criteria

And here is the latest git diff:

%s

And coming up are results of automated checks, which is important for your
analysis. A failure here is not automatically a failure of the criteria -- that
depends on your analysis, since failure may be expected or even desired -- but
it is a strong hint that something might be wrong with the work done. If you
notice that the step is defined in a way that necessarily fails the checks
temporarily, and the manner in which it failed matches what should be expected,
and you also confirm first that there is already a later step defined that is
expected to fix the issue, then this kind of failure doesn't mean the criteria
haven't been fulfilled.

If there is no such later step or the step doesn't necessitate the failure, then
consider the criteria NOT fulfilled. Even if the failure doesn't seem directly
related to the step, if it's not expected nor is there an expected later step
that will fix it, consider the criteria not fulfilled, so you must analyze the
failure and determine if pre-planned steps exist that will fix it. Don't think
about what is "likely" to happen in future steps. Instead, start by spelling out
the specific future step from the above plan that you think will fix the issue
if you think the automatic check failure is not meant to be fixed in the current
step. If there's no such step, consider the criteria not fulfilled due to the
failure.

`, promptInfo.Requirements, promptInfo.PlanExecution.String(), promptInfo.Step.Definition, promptInfo.Step.CompletionAnalysis, promptInfo.Work)
		if promptInfo.AutoChecks != "" {
			content += fmt.Sprintf(`
If the automated tests clearly indicate a failure related to the changes made in
the current step or a previous one without a followup step explicitly defined to
fix it, then don't analyze too much after determining that fact: just indicate
that the tests should be fixed. When they are fixed, we'll come back to analyze
whether criteria is fulfilled fully later.

Anyways, here are the automated check results:

%s
`, promptInfo.AutoChecks)
		}
	} else {
		// TODO /gen adjust this to use RenderPrompt, followiing the same pattern as other functions calling RenderPrompt
		content = fmt.Sprintf(`
Thinking step-by-step as a senior software engineer, analyze the following git
diff and determine if the requirements are fulfilled. First output your
analysis of the diff against all aspects of the requirements. In addition,
analyze whether the code changes look correct and maintain previous
functionality that should not have been altered as part of the change.

Review the latest code changes for any issues. If there are major issues, we
consider that an unsaid criterium for correctness that has not been fulfilled.
If you're not sure if it's an issue or its something you cannot check for (eg
whether an import already existed), assume it is correct if automated checks
(i.e. tests) have passed.  If there are code changes referenced that we got user
feedback on, then only consider the latest user feedback and the latest code,
not the earlier code/requirements.

Finally, output whether the relevant requirements are fulfilled or not.

Here are all the requirements:

# START REQUIREMENTS
%s
# END REQUIREMENTS

And here is the latest git diff:

%s

`, promptInfo.Requirements, promptInfo.Work)
		if promptInfo.AutoChecks != "" {
			content += fmt.Sprintf(`
If the automated tests clearly indicate a failure related to the latest changes
made, then don't analyze other aspects after determining that the test is
related to those changes: just indicate that the tests should be fixed. When
they are fixed, we'll come back to analyze whether other criteria is fulfilled
fully later.

Anyways, here are the automated check results:

%s`, promptInfo.AutoChecks)
		}
	}

	newMessage := llm.ChatMessage{
		Role:        llm.ChatMessageRoleUser,
		Content:     content,
		ContextType: ContextTypeInitialInstructions,
	}
	*chatHistory = append(*chatHistory, newMessage)
	return chatHistory
}
