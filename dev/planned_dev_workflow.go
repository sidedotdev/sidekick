package dev

import (
	"errors"
	"fmt"
	"os"
	"sidekick/common"
	"sidekick/domain"
	"sidekick/env"
	"sidekick/utils"

	"go.temporal.io/sdk/workflow"
)

// SetupPause is already in the dev package, so we don't need to import it

type PlannedDevInput struct {
	RepoDir      string
	Requirements string
	WorkspaceId  string
	PlannedDevOptions
}
type PlannedDevOptions struct {
	PlanningPrompt        string                 `json:"planningPrompt"`
	ReproduceIssue        bool                   `json:"reproduceIssue"`
	DetermineRequirements bool                   `json:"determineRequirements"`
	EnvType               env.EnvType            `json:"envType,omitempty" default:"local"`
	StartBranch           *string                `json:"startBranch,omitempty"` // Optional branch for git worktree env
	ConfigOverrides       common.ConfigOverrides `json:"configOverrides"`
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
				_ = signalWorkflowClosure(ctx, "failed")
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

	dCtx, err := SetupDevContext(ctx, input.WorkspaceId, input.RepoDir, string(input.EnvType), input.PlannedDevOptions.StartBranch, input.Requirements, input.PlannedDevOptions.ConfigOverrides)
	if err != nil {
		_ = signalWorkflowClosure(ctx, "failed")
		return DevPlanExecution{}, fmt.Errorf("failed to setup dev context: %v", err)
	}
	defer handleFlowCancel(dCtx)
	defer func() {
		if err != nil && !errors.Is(dCtx.Err(), workflow.ErrCanceled) {
			_ = signalWorkflowClosure(dCtx, "failed")
			return
		}
	}()

	// Set up the pause and user action handlers
	SetupPauseHandler(dCtx, "Paused for user input", nil)
	SetupUserActionHandler(dCtx)

	// TODO move environment creation to an activity within EnsurePrerequisites
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
	if input.EnvType == env.EnvTypeLocalGitWorktree && v == 1 {
		err := reviewAndResolve(dCtx, MergeWithReviewParams{
			CommitRequired: false, // planned dev flow writes commits already
			Requirements: input.Requirements + `

Here is the plan for meeting the requirements, along with updates per step:

` + devPlan.String(),
			StartBranch: input.StartBranch,
			GetGitDiff:  nil,
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
		if err != nil {
			return fmt.Errorf("failed to run tests: %v", err)
		}

		if testResult.TestsPassed {
			if len(dCtx.RepoConfig.IntegrationTestCommands) == 0 {
				break
			}

			integrationTestResult, err := RunTests(dCtx, dCtx.RepoConfig.IntegrationTestCommands)
			if err != nil {
				return fmt.Errorf("failed to run integration tests: %v", err)
			}
			if integrationTestResult.TestsPassed {
				break
			}

			// use the integration test results as part of the prompt
			testResult = integrationTestResult
		}

		// TODO if it's integration tests that failed, override the configured
		// test commands that should be run within dCtx, to include the
		// integration tests as well, to ensure that the inner loop of editing
		// code within completeDevStep has access to the output of integration
		// test results too.
		_, err = completeDevStep(dCtx, input.Requirements, planExec, DevStep{
			Type:               "edit",
			Title:              "Ensure Tests Pass",
			Definition:         "The plan has now been fully executed, but please ensure tests pass: they are unfortunately still failing. If you notice errors in the code, fix them but ensure all of the original requirements are being met with your changes. Here are test results:\n\n" + testResult.Output,
			CompletionAnalysis: "This final step will be considered complete when *all* tests pass. Any test failures mean the requirements are not met and thus the criteria have not been fulfilled. Furthermore, it's required that no changes were made that are not in line with the original requirements.",
		})

		if err != nil {
			return err
		}
	}

	return nil
}
