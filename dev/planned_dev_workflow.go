package dev

import (
	"fmt"
	"os"
	"sidekick/models"
	"sidekick/utils"

	"go.temporal.io/sdk/workflow"
)

type PlannedDevInput struct {
	RepoDir      string
	Requirements string
	WorkspaceId  string
	PlannedDevOptions
}
type PlannedDevOptions struct {
	PlanningPrompt        string `json:"planningPrompt"`
	ReproduceIssue        bool   `json:"reproduceIssue"`
	DetermineRequirements bool   `json:"determineRequirements"`
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

	dCtx, err := SetupDevContext(ctx, input.WorkspaceId, input.RepoDir)
	if err != nil {
		_ = signalWorkflowClosure(ctx, "failed")
		return DevPlanExecution{}, fmt.Errorf("failed to setup dev context: %v", err)
	}

	// TODO move environment creation to an activity within EnsurePrerequisites
	err = EnsurePrerequisites(dCtx, input.Requirements)
	if err != nil {
		_ = signalWorkflowClosure(ctx, "failed")
		return DevPlanExecution{}, err
	}

	if input.DetermineRequirements {
		refinedRequirements, err := BuildDevRequirements(dCtx, InitialDevRequirementsInfo{Requirements: input.Requirements})
		if err != nil {
			_ = signalWorkflowClosure(ctx, "failed")
			return DevPlanExecution{}, err
		}
		input.Requirements = refinedRequirements.String()
	}

	devPlan, err := BuildDevPlan(dCtx, input.Requirements, input.PlanningPrompt, input.ReproduceIssue)
	if err != nil {
		_ = signalWorkflowClosure(ctx, "failed")
		return DevPlanExecution{}, err
	}

	planExec, err = FollowDevPlan(dCtx, FollowDevPlanInput{
		DevPlan:      devPlan,
		WorkspaceId:  input.WorkspaceId,
		EnvContainer: *dCtx.EnvContainer,
		Requirements: input.Requirements,
	})
	if err != nil {
		_ = signalWorkflowClosure(ctx, "failed")
		return DevPlanExecution{}, err
	}

	err = EnsureTestsPassAfterDevPlanExecuted(dCtx, input, planExec)
	if err != nil {
		_ = signalWorkflowClosure(ctx, "failed")
		return DevPlanExecution{}, err
	}

	err = AutoFormatCode(dCtx)
	if err != nil {
		_ = signalWorkflowClosure(ctx, "failed")
		return DevPlanExecution{}, fmt.Errorf("failed to auto-format code: %v", err)
	}

	// emit signal when workflow ends successfully
	err = signalWorkflowClosure(ctx, "completed")
	if err != nil {
		return DevPlanExecution{}, fmt.Errorf("failed to signal workflow closure: %v", err)
	}

	return planExec, nil
}

func EnsureTestsPassAfterDevPlanExecuted(dCtx DevContext, input PlannedDevInput, planExec DevPlanExecution) error {
	return RunSubflowWithoutResult(dCtx, "Finalize", func(_ models.Subflow) error {
		return ensureTestsPassAfterDevPlanExecutedSubflow(dCtx, input, planExec)
	})
}

func ensureTestsPassAfterDevPlanExecutedSubflow(dCtx DevContext, input PlannedDevInput, planExec DevPlanExecution) error {
	maxAttempts := 3
	attempts := 0
	for {
		if attempts >= maxAttempts {
			return fmt.Errorf("failed to ensure tests pass after dev plan executed")
		}
		attempts++

		testResult, err := RunTests(dCtx)
		if err != nil {
			return fmt.Errorf("failed to run tests: %v", err)
		}
		if testResult.TestsPassed {
			break
		}
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
