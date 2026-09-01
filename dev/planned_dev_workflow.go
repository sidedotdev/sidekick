package dev

import (
	"errors"
	"fmt"
	"os"
	"sidekick/common"
	"sidekick/domain"
	"sidekick/env"
	"sidekick/flow_action"
	"sidekick/utils"

	"github.com/rs/zerolog/log"
	"go.temporal.io/sdk/workflow"
)

// SetupPause is already in the dev package, so we don't need to import it

type PlannedDevInput struct {
	RepoDir      string
	Requirements string
	WorkspaceId  string
	// Title, when set, is a concise summary of the work used for commit and
	// merge messages in place of the verbose requirements text.
	Title string
	PlannedDevOptions
}
type PlannedDevOptions struct {
	PlanningPrompt        string                 `json:"planningPrompt"`
	ReproduceIssue        bool                   `json:"reproduceIssue"`
	DetermineRequirements bool                   `json:"determineRequirements"`
	EnvType               env.EnvType            `json:"envType,omitempty" default:"local"`
	RepoMode              env.RepoMode           `json:"repoMode,omitempty" default:"worktree"`
	StartBranch           *string                `json:"startBranch,omitempty"`
	ConfigOverrides       common.ConfigOverrides `json:"configOverrides"`
	ContextGatherType     ContextGatherType      `json:"contextGatherType,omitempty"`
	// AutoMerge skips the human merge approval and merges automatically into the
	// start branch. Used by IDD sub-tasks so their worktree merges back into the
	// parent idd worktree.
	AutoMerge bool `json:"autoMerge,omitempty"`
	// Idd marks the sub-task as originating from an Intent Driven Development
	// flow, enabling the intent/ directory guidance in coding-agent prompts.
	Idd bool `json:"idd,omitempty"`
}

var SideAppEnv = os.Getenv("SIDE_APP_ENV")

func PlannedDevWorkflow(ctx workflow.Context, input PlannedDevInput) (planExec DevPlanExecution, err error) {
	// don't recover panics in development so we can debug via temporal UI, at
	// the cost of failed tasks appearing stuck without UI feedback in sidekick
	if SideAppEnv != "development" {
		defer func() {
			// panics should not be used for control flow, but if we do panic, we
			// want to make the error visible in the Sidekick UI and mark the task
			// as failed
			if r := recover(); r != nil {
				signalWorkflowFailureOrCancel(ctx)
				var ok bool
				err, ok = r.(error)
				if !ok {
					err = fmt.Errorf("panic: %v", r)
				}
				// TODO create a flow event that will be displayed in the UI
			}
		}()
	}

	ctx = utils.DefaultRetryCtx(ctx)

	dCtx, err := SetupDevContext(ctx, input.WorkspaceId, input.RepoDir, string(input.EnvType), string(input.RepoMode), input.PlannedDevOptions.StartBranch, input.Requirements, input.PlannedDevOptions.ConfigOverrides)
	if err != nil {
		signalWorkflowFailureOrCancel(ctx)
		return DevPlanExecution{}, fmt.Errorf("failed to setup dev context: %v", err)
	}
	dCtx.ContextGatherType = input.PlannedDevOptions.ContextGatherType
	dCtx.Idd = input.PlannedDevOptions.Idd
	defer handleFlowCancel(dCtx)
	defer stopActiveDevRun(dCtx)
	defer func() {
		if err != nil && !errors.Is(dCtx.Err(), workflow.ErrCanceled) {
			_ = signalWorkflowClosure(dCtx, "failed")
			return
		}
	}()

	// Set up the pause, user action, and query handlers
	SetupPauseHandler(dCtx, "Paused for user input", nil)
	SetupUserActionHandler(dCtx)
	SetupDevRunConfigQuery(dCtx)
	SetupDevRunStateQuery(dCtx)
	if err = SetupModelConfigHandlers(dCtx); err != nil {
		return DevPlanExecution{}, err
	}
	if err = SetupModalConfigHandlers(dCtx); err != nil {
		return DevPlanExecution{}, err
	}

	// TODO move environment creation to an activity within EnsurePrerequisites
	hibernateVersion := workflow.GetVersion(dCtx, "hibernate-worktree", workflow.DefaultVersion, 3)
	if hibernateVersion >= 1 {
		SetupHibernateHandler(dCtx)
	}
	if hibernateVersion == 1 {
		if _, err = WakeIfHibernated(dCtx); err != nil {
			return DevPlanExecution{}, fmt.Errorf("failed to wake hibernated worktree: %w", err)
		}
	}
	err = EnsurePrerequisites(dCtx)
	if err != nil {
		return DevPlanExecution{}, err
	}

	if input.DetermineRequirements {
		refinedRequirements, err := BuildDevRequirements(dCtx, InitialDevRequirementsInfo{Requirements: input.Requirements})
		if err != nil {
			return DevPlanExecution{}, err
		}
		input.Requirements = refinedRequirements.String()
	}

	devPlan, err := BuildDevPlan(dCtx, input.Requirements, input.PlanningPrompt, input.ReproduceIssue)
	if err != nil {
		return DevPlanExecution{}, err
	}

	planExec, err = FollowDevPlan(dCtx, FollowDevPlanInput{
		DevPlan:      devPlan,
		WorkspaceId:  input.WorkspaceId,
		EnvContainer: *dCtx.EnvContainer,
		Requirements: input.Requirements,
	})
	if err != nil {
		return DevPlanExecution{}, err
	}

	err = EnsureTestsPassAfterDevPlanExecuted(dCtx, input, planExec)
	if err != nil {
		return DevPlanExecution{}, err
	}

	err = AutoFormatCode(dCtx)
	if err != nil {
		return DevPlanExecution{}, fmt.Errorf("failed to auto-format code: %v", err)
	}

	// Handle merge if using worktree and workflow version is new enough
	v := workflow.GetVersion(ctx, "git-worktree-merge", workflow.DefaultVersion, 1)
	if dCtx.Worktree != nil && v == 1 {
		err := reviewAndResolve(dCtx, MergeWithReviewParams{
			CommitRequired: false, // planned dev flow writes commits already
			Requirements: input.Requirements + `

Here is the plan for meeting the requirements, along with updates per step:

` + devPlan.String(),
			Title:       input.Title,
			StartBranch: input.StartBranch,
			AutoMerge:   input.AutoMerge,
		})
		if err != nil {
			return DevPlanExecution{}, err
		}
	}

	// emit signal when workflow ends successfully
	err = signalWorkflowClosure(ctx, "completed")
	if err != nil {
		return DevPlanExecution{}, fmt.Errorf("failed to signal workflow closure: %v", err)
	}

	return planExec, nil
}

func EnsureTestsPassAfterDevPlanExecuted(dCtx DevContext, input PlannedDevInput, planExec DevPlanExecution) error {
	return RunSubflowWithoutResult(dCtx, "pass_tests", "Finalize", func(_ domain.Subflow) error {
		return ensureTestsPassAfterDevPlanExecutedSubflow(dCtx, input, planExec)
	})
}

func ensureTestsPassAfterDevPlanExecutedSubflow(dCtx DevContext, input PlannedDevInput, planExec DevPlanExecution) error {
	maxAttempts := 3
	attempts := 0
	for {
		v := workflow.GetVersion(dCtx, "no-max-unless-disabled-human", workflow.DefaultVersion, 1)
		if attempts >= maxAttempts && (v < 1 || dCtx.RepoConfig.DisableHumanInTheLoop) {
			return fmt.Errorf("failed to ensure tests pass after dev plan executed")
		}
		attempts++

		testResult, err := RunTests(dCtx, dCtx.RepoConfig.TestCommands)
		if err != nil && !errors.Is(err, flow_action.PendingActionError) {
			if !gracefullyHandlePausedTestError(dCtx) {
				return fmt.Errorf("failed to run tests: %w", err)
			}
			log.Debug().Err(err).Msg("Ignoring test error while paused")
		}

		if testResult.TestsSkipped {
			return nil
		}

		integrationTestsFailed := false
		if err == nil && testResult.TestsPassed {
			if len(dCtx.RepoConfig.IntegrationTestCommands) == 0 {
				break
			}

			integrationTestResult, err := RunTests(dCtx, dCtx.RepoConfig.IntegrationTestCommands)
			if err != nil && !errors.Is(err, flow_action.PendingActionError) {
				if !gracefullyHandlePausedTestError(dCtx) {
					return fmt.Errorf("failed to run integration tests: %w", err)
				}
				log.Debug().Err(err).Msg("Ignoring integration test error while paused")
			}
			if err == nil {
				if integrationTestResult.TestsPassed || integrationTestResult.TestsSkipped {
					break
				}

				// use the integration test results as part of the prompt
				testResult = integrationTestResult
				integrationTestsFailed = true
			}
		}

		_, err = completeDevStep(dCtx, input.Requirements, planExec, DevStep{
			Type:                "edit",
			Title:               "Ensure Tests Pass",
			Definition:          "The plan has now been fully executed, but please ensure tests pass: they are unfortunately still failing. If you notice errors in the code, fix them but ensure all of the original requirements are being met with your changes. Here are test results:\n\n" + testResult.Output,
			CompletionAnalysis:  "This final step will be considered complete when *all* tests pass. Any test failures mean the requirements are not met and thus the criteria have not been fulfilled. Furthermore, it's required that no changes were made that are not in line with the original requirements.",
			RunIntegrationTests: integrationTestsFailed,
		})

		if err != nil {
			return err
		}
	}

	return nil
}
