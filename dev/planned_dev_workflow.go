package dev

import (
	"fmt"
	"os"
	"sidekick/coding/git"
	"sidekick/domain"
	"sidekick/env"
	"sidekick/utils"

	"go.temporal.io/sdk/workflow"
)

// SetupPauseHandler is already in the dev package, so we don't need to import it

type PlannedDevInput struct {
	RepoDir      string
	Requirements string
	WorkspaceId  string
	PlannedDevOptions
}
type PlannedDevOptions struct {
	PlanningPrompt        string      `json:"planningPrompt"`
	ReproduceIssue        bool        `json:"reproduceIssue"`
	DetermineRequirements bool        `json:"determineRequirements"`
	EnvType               env.EnvType `json:"envType,omitempty" default:"local"`
	StartBranch           *string     `json:"startBranch,omitempty"` // Optional branch for git worktree env
}

var SideAppEnv = os.Getenv("SIDE_APP_ENV")

func PlannedDevWorkflow(ctx workflow.Context, input PlannedDevInput) (planExec DevPlanExecution, err error) {
	globalState := &GlobalState{}

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

	dCtx, err := SetupDevContext(ctx, input.WorkspaceId, input.RepoDir, string(input.EnvType), input.PlannedDevOptions.StartBranch)
	if err != nil {
		_ = signalWorkflowClosure(ctx, "failed")
		return DevPlanExecution{}, fmt.Errorf("failed to setup dev context: %v", err)
	}
	dCtx.GlobalState = globalState

	// Set up the pause handler
	SetupPauseHandler(dCtx, "Paused for user input", nil)

	// TODO move environment creation to an activity within EnsurePrerequisites
	err = EnsurePrerequisites(dCtx)
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

	// Handle merge if using worktree and workflow version is new enough
	v := workflow.GetVersion(ctx, "git-worktree-merge", workflow.DefaultVersion, 1)
	if input.EnvType == env.EnvTypeLocalGitWorktree && v == 1 {
		defaultTarget := "main"
		if input.PlannedDevOptions.StartBranch != nil {
			defaultTarget = *input.PlannedDevOptions.StartBranch
		}

		// Get diff between branches using three-dot syntax
		var gitDiff string
		future := workflow.ExecuteActivity(ctx, git.GitDiffActivity, dCtx.EnvContainer, git.GitDiffParams{
			ThreeDotDiff: true,
			BaseBranch:   defaultTarget,
		})
		err = future.Get(ctx, &gitDiff)
		if err != nil {
			_ = signalWorkflowClosure(ctx, "failed")
			return DevPlanExecution{}, fmt.Errorf("failed to get branch diff: %v", err)
		}

		mergeInfo, err := getMergeApproval(dCtx, defaultTarget, gitDiff)
		if err != nil {
			_ = signalWorkflowClosure(ctx, "failed")
			return DevPlanExecution{}, fmt.Errorf("failed to get merge approval: %v", err)
		}

		if mergeInfo.Approved {
			// Perform the merge
			var mergeResult git.MergeActivityResult
			err = workflow.ExecuteActivity(ctx, git.GitMergeActivity, dCtx.EnvContainer, git.GitMergeParams{
				SourceBranch: *input.PlannedDevOptions.StartBranch,
				TargetBranch: mergeInfo.TargetBranch,
			}).Get(ctx, &mergeResult)
			if err != nil {
				_ = signalWorkflowClosure(ctx, "failed")
				return DevPlanExecution{}, fmt.Errorf("failed to merge: %v", err)
			}

			if mergeResult.HasConflicts {
				// Present continue request with Done tag
				actionCtx := dCtx.NewActionContext("user_request.continue")
				err := GetUserContinue(actionCtx, "Merge conflicts detected. Please resolve conflicts and continue when done.", map[string]any{
					"continueTag": "Done",
				})
				if err != nil {
					_ = signalWorkflowClosure(ctx, "failed")
					return DevPlanExecution{}, fmt.Errorf("failed to get continue approval: %v", err)
				}
			}
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
