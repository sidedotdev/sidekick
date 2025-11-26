package dev

import (
	"errors"
	"fmt"
	"sidekick/coding/git"
	"sidekick/common"
	"sidekick/domain"
	"sidekick/env"
	"sidekick/fflag"
	"sidekick/flow_action"
	"sidekick/llm"
	"strings"

	"github.com/rs/zerolog/log"
	"go.temporal.io/sdk/workflow"
)

type DevPlanExecution struct {
	Plan           *DevPlan
	StepExecutions []DevStepExecution
}

type DevStepExecution struct {
	DevStep  DevStep
	Complete bool
	// not sure about this, but maybe helpful if the "complete" boolean on
	// previous plan steps is not enough context for the next steps
	ExecutionSummary string
}

type DevStepResult struct {
	Successful  bool
	Summary     string
	Fulfillment *CriteriaFulfillment // TODO /gen set this value and use it instead of summary when available
	// step number to go to
	GoTo string
}

func (planExec DevPlanExecution) String() string {
	writer := &strings.Builder{}
	if len(planExec.Plan.Learnings) > 0 {
		writer.WriteString("Plan Learnings:\n")
		for _, learning := range planExec.Plan.Learnings {
			writer.WriteString(learning)
			writer.WriteString("\n")
		}
		writer.WriteString("Plan:\n")
	}
	for i, stepExecution := range planExec.StepExecutions {
		if i > 0 {
			writer.WriteString("\n")
		}
		// TODO /gen add a Status field to step exec to track unstarted vs
		// complete vs in progress, update that in the follow dev plan loop,
		// then use that here
		//writer.WriteString("Status: ")
		//writer.WriteString(stepeExec.Status)
		writer.WriteString(stepExecution.String())
	}
	return writer.String()
}

func (stepExec DevStepExecution) String() string {
	writer := &strings.Builder{}
	if stepExec.Complete {
		// ballot box with check
		writer.WriteString("☑️ ")
	} else {
		// ballot box
		writer.WriteString("☐ ")
	}
	writer.WriteString(stepExec.DevStep.StepNumber)
	writer.WriteString(") ")
	writer.WriteString(stepExec.DevStep.Definition)
	if stepExec.Complete {
		writer.WriteString("\n")
		writer.WriteString(stepExec.ExecutionSummary)
	}
	return writer.String()
}

type FollowDevPlanInput struct {
	WorkspaceId  string
	EnvContainer env.EnvContainer
	Requirements string
	DevPlan      *DevPlan
}

// NOTE this is not yet used, but will be used in the future
func FollowDevPlan(dCtx DevContext, input FollowDevPlanInput) (DevPlanExecution, error) {
	return RunSubflow(dCtx, "follow_dev_plan", "Follow Dev Plan", func(_ domain.Subflow) (DevPlanExecution, error) {
		return followDevPlanSubflow(dCtx, input)
	})
}

func followDevPlanSubflow(dCtx DevContext, input FollowDevPlanInput) (DevPlanExecution, error) {
	plan := input.DevPlan
	planExecution := DevPlanExecution{
		Plan:           plan,
		StepExecutions: initializeStepExecutions(plan.Steps),
	}

	// XXX this loop does not allow for goto to work, so let's adjust so we get
	// the next dev step based on the current plan execution + last result
	for i, step := range plan.Steps {
		result, err := completeDevStep(dCtx, input.Requirements, planExecution, step)
		planExecution.StepExecutions[i].Complete = result.Successful
		planExecution.StepExecutions[i].ExecutionSummary = result.Summary

		if err != nil {
			return planExecution, fmt.Errorf("failed to complete dev step: %v", err)
		}
		if !result.Successful {
			return planExecution, fmt.Errorf("step %s was not successful: %s", step.StepNumber, result.Summary)
		}
	}

	return planExecution, nil
}

func initializeStepExecutions(steps []DevStep) []DevStepExecution {
	stepExecutions := make([]DevStepExecution, len(steps))

	for i, step := range steps {
		stepExecutions[i] = DevStepExecution{
			DevStep:  step,
			Complete: false,
		}
	}

	return stepExecutions
}

func completeDevStep(dCtx DevContext, requirements string, planExecution DevPlanExecution, step DevStep) (result DevStepResult, err error) {
	subflowName := step.Title
	if step.StepNumber != "" {
		subflowName = step.StepNumber + ". " + step.Title
	}

	return RunSubflow(dCtx, "step.dev", subflowName, func(subflow domain.Subflow) (DevStepResult, error) {
		return completeDevStepSubflow(dCtx, requirements, planExecution, step)
	})
}

func completeDevStepSubflow(dCtx DevContext, requirements string, planExecution DevPlanExecution, step DevStep) (result DevStepResult, err error) {
	// Step 1: prepare code context
	//prompt := fmt.Sprintf("#START Background Info\n%s\n#END Background Info\n#START Plan#\n%s\n", requirements, planExecution.String(), step.Title)
	codeContext, fullCodeContext, err := PrepareInitialCodeContext(dCtx, requirements, &planExecution, &step)
	contextSizeExtension := len(fullCodeContext) - len(codeContext)
	if err != nil {
		return result, fmt.Errorf("failed to prepare code context: %v", err)
	}

	if v := workflow.GetVersion(dCtx, "initial-code-repo-summary", workflow.DefaultVersion, 2); v >= 1 && fflag.IsEnabled(dCtx, fflag.InitialRepoSummary) {
		repoSummary, err := GetRepoSummaryForPrompt(dCtx, requirements, 5000)
		if err != nil {
			return result, fmt.Errorf("failed to retrieve repo summary: %v", err)
		}
		codeContext = repoSummary + "\n\n" + codeContext
	}

	// TODO store chat history in a way that can be referred to by id, and pass
	// id to the activities to avoid bloating temporal db
	chatHistory := &[]llm.ChatMessage{}

	modelConfigs, _ := dCtx.LLMConfig.GetModelsOrDefault(common.CodingKey)
	modelAttemptCount := 0
	modelIndex := 0

	maxAttempts := 17
	repoConfig := dCtx.RepoConfig
	if repoConfig.MaxIterations > 0 {
		maxAttempts = repoConfig.MaxIterations
	}

	// TODO decide how to set the dev step info based on the step type, eg
	// perhaps different structs per step type
	initialPromptInfo := InitialDevStepInfo{
		CodeContext:   codeContext,
		Requirements:  requirements,
		PlanExecution: planExecution,
		Step:          step,
	}

	attemptCount := 0
	var promptInfo PromptInfo
	promptInfo = initialPromptInfo

	goToNextModel := func() error {
		// reset everything to let the next model start fresh
		modelAttemptCount = 0
		modelIndex++
		// only reset working dir and chat history if we haven't looped back to the first model
		if modelIndex < len(modelConfigs) {
			// TODO /gen capture the git checkout head all in a "Revert Edits" flow action
			promptInfo = initialPromptInfo
			chatHistory = &[]llm.ChatMessage{}
			err := git.GitCheckoutHeadAll(dCtx.ExecContext)
			if err != nil {
				return fmt.Errorf("failed to reset working directory via git checkout: %v", err)
			}
		}
		return nil
	}

	for {
		// TODO /gen introduce config.MaxModelAttempts to allow for better control
		if modelAttemptCount >= maxAttempts/2 {
			if err := goToNextModel(); err != nil {
				return result, err
			}
		}
		modelConfig := modelConfigs[modelIndex%len(modelConfigs)]

		// TODO /gen don't get user feedback if it just got help via the
		// get_help_or_input tool recently already, i.e. keep track of count
		// of attempts since last helped and use that here instead
		// TODO figure out best way to do this when human is in the loop
		if modelAttemptCount > 0 && modelAttemptCount%2 == 0 && len(*chatHistory) > 0 {
			guidanceContext := "The system looped 2 times attempting to complete this step and failed, so the LLM probably needs some help. Please provide some guidance for the next step based on the above log. Look at the latest test result and git diff for an idea of what's going wrong."

			// get the latest git diff, since it could be different from the
			// last time we got it, if we ever did
			gitDiff, diffErr := git.GitDiff(dCtx.ExecContext)
			if diffErr != nil {
				return result, fmt.Errorf("failed to get git diff: %v", diffErr)
			}

			// Run tests (TODO: replace this with using the latest DevStepResult)
			testResult, err := RunTests(dCtx, dCtx.RepoConfig.TestCommands)
			if err != nil {
				return result, fmt.Errorf("failed to run tests: %v", err)
			}

			// Get user feedback with git diff and test results
			requestParams := map[string]any{
				"gitDiff":     gitDiff,
				"testResult":  testResult,
				"fulfillment": result.Fulfillment,
			}
			feedbackInfo, err := GetUserFeedback(dCtx, promptInfo, guidanceContext, chatHistory, requestParams)
			if err != nil {
				return result, fmt.Errorf("failed to get user feedback: %v", err)
			}
			promptInfo = feedbackInfo
		}

		if attemptCount >= maxAttempts {
			return result, errors.New("failed to complete dev step, max attempts reached")
		}

		// Step 2: execute step
		err = performStep(dCtx, modelConfig, contextSizeExtension, chatHistory, promptInfo, step, planExecution)
		if err != nil && !errors.Is(err, flow_action.PendingActionError) {
			log.Warn().Err(err).Msg("Error executing step")
			// TODO: on repeated overloaded_error from anthropic, we want to
			// fallback to another provider higher up. similar for other provider
			// errors.
			if errors.Is(err, ErrMaxAttemptsReached) {
				if err := goToNextModel(); err != nil {
					return result, err
				}
				continue
			}
			return result, fmt.Errorf("failed to perform step: %w", err)
		}

		// Step 3: evaluate completion of step
		executeNormalStepEvaluation := true
		if v := workflow.GetVersion(dCtx, "user-action-go-next", workflow.DefaultVersion, 1); v == 1 {
			action := dCtx.ExecContext.GlobalState.GetPendingUserAction()
			if action != nil && *action == flow_action.UserActionGoNext {
				dCtx.ExecContext.GlobalState.ConsumePendingUserAction()
				executeNormalStepEvaluation = false
			}
		}
		if executeNormalStepEvaluation {
			result, err = checkIfDevStepCompleted(dCtx, requirements, step, planExecution)
			if err != nil {
				return result, fmt.Errorf("failed to check if requirements are fulfilled: %w", err)
			}
		} else {
			// we're forcibly going to the next step, which requires this step
			// to be considered "successful"
			result.Summary = fmt.Sprintf("The user forcibly ended step %s's execution to go to the next step. Assume that it has been completed, though likely in a manner slightly different from what was originally specified. Take any changes in stride, but ask for clarifications if necessary.", step.StepNumber)
			result.Successful = true
			break
		}

		if result.Successful {
			break
		} else {
			promptInfo = FeedbackInfo{Feedback: fmt.Sprintf("The step did not seem to have not been completed per its criteria. Here is the feedback: %s", result.Summary)}
			attemptCount++
			modelAttemptCount++
			continue
		}
	}

	// Step 4: auto-format code and add OR commit all changes to git (depending on flag)
	// only do this if the type was edit
	if step.Type == "edit" {
		err = AutoFormatCode(dCtx)
		if err != nil {
			return result, fmt.Errorf("failed to auto-format code: %w", err)
		}

		// needed when check edits is enabled to ensure we commit the auto-formatting
		// needed when check edits is disabled because staging is how we limit future diffs in that case
		err = git.GitAddAll(dCtx.ExecContext)
		if err != nil {
			return result, fmt.Errorf("failed to git add all: %w", err)
		}

		if fflag.IsEnabled(dCtx, fflag.CheckEdits) {
			// TODO use an LLM to write a better commit message
			err = git.GitCommit(dCtx.ExecContext, fmt.Sprintf("%s\n\n%s", step.Title, step.Definition))
			if err != nil {
				return result, fmt.Errorf("failed to git commit: %w", err)
			}
		}
	}

	return result, nil
}

/*
This newer version ignores completion criteria and instead forces the same
criteria for a given step type since that is more reliable and less error prone
than having the LLM specify criteria and miss things at that step
*/
func checkIfDevStepCompleted(dCtx DevContext, overallRequirements string, step DevStep, planExecution DevPlanExecution) (result DevStepResult, err error) {
	// FIXME support step.Type set to "other"
	switch step.Type {
	case "edit":
		// Pass a git diff of the repo + test results to the llm and ask if it looks good
		testResult, err := RunTests(dCtx, dCtx.RepoConfig.TestCommands)
		if err != nil {
			return result, fmt.Errorf("failed to run tests: %v", err)
		}
		fulfillment, err := CheckWorkMeetsCriteria(dCtx, CheckWorkInfo{
			CodeContext:   "", // TODO providing the code context will help with checking for criteria fulfillment
			Requirements:  overallRequirements,
			Step:          step,
			PlanExecution: planExecution,
			AutoChecks:    testResult.Output,
		})
		if err != nil {
			return result, fmt.Errorf("error checking if criteria are fulfilled: %w", err)
		}
		result.Fulfillment = &fulfillment
		result.Successful = fulfillment.IsFulfilled
		// including test results when not successful only, so the outer process
		// can use them to self-correct, but excluding them when successful to
		// avoid bloating the chat history in plan execution summary
		if result.Successful {
			result.Summary = result.Summary + "\n" + fulfillment.WorkDescription
		} else {
			result.Summary = result.Summary + "\n" + testResult.Output + "\n" + fulfillment.FeedbackMessage
		}
		// TODO add a result.Details field to store the diff and other details (maybe test results when successful)
	default:
		result.Successful = false
		return result, fmt.Errorf("unknown step type: %s", step.Type)
	}
	return result, nil
}

func performStep(dCtx DevContext, codingModelConfig common.ModelConfig, contextSizeExtension int, chatHistory *[]llm.ChatMessage, promptInfo PromptInfo, step DevStep, planExec DevPlanExecution) error {
	v := workflow.GetVersion(dCtx, "performStep", workflow.DefaultVersion, 1)
	if v == workflow.DefaultVersion {
		return RunSubflowWithoutResult(dCtx, "perform_step", "Perform Step", func(_ domain.Subflow) error {
			return performStepSubflow(dCtx, codingModelConfig, contextSizeExtension, chatHistory, promptInfo, step, planExec)
		})
	} else {
		// remove the perform step subflow going forward, since we'll have
		// subflows when needed for individual step types (eg edit_code)
		return performStepSubflow(dCtx, codingModelConfig, contextSizeExtension, chatHistory, promptInfo, step, planExec)
	}
}

func performStepSubflow(dCtx DevContext, codingModelConfig common.ModelConfig, contextSizeExtension int, chatHistory *[]llm.ChatMessage, promptInfo PromptInfo, step DevStep, planExec DevPlanExecution) error {
	switch step.Type {
	case "edit":
		return EditCode(dCtx, codingModelConfig, contextSizeExtension, chatHistory, promptInfo)
	case "other":
		request := HelpOrInputRequest{
			Content: step.Definition,
		}
		_, err := GetHelpOrInput(dCtx, []HelpOrInputRequest{request})
		if err != nil {
			return err
		}
	default:
		return fmt.Errorf("unsupported step type: %s", step.Type)
	}
	return nil
}
