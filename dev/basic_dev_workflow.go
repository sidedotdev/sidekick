package dev

import (
	"errors"
	"fmt"
	"strings"

	"go.temporal.io/sdk/workflow"

	"sidekick/coding/git"
	"sidekick/common"
	"sidekick/domain"
	"sidekick/env"
	"sidekick/flow_action"
	"sidekick/llm"
	"sidekick/utils"
)

// Define the workflow input type.
type BasicDevWorkflowInput struct {
	WorkspaceId  string
	RepoDir      string
	Requirements string
	BasicDevOptions
}

type BasicDevOptions struct {
	DetermineRequirements bool        `json:"determineRequirements"`
	EnvType               env.EnvType `json:"envType,omitempty" default:"local"`
	StartBranch           *string     `json:"startBranch,omitempty"`
}

type MergeWithReviewParams struct {
	Requirements   string
	StartBranch    *string
	GetGitDiff     func(dCtx DevContext, baseBranch string) (string, error) // function to get git diff customized per workflow, nil for default
	SubflowType    string                                                   // for tracking purposes
	SubflowName    string                                                   // for tracking purposes
	CommitRequired bool
}

// formatRequirementsWithReview combines original requirements with review history and work done
// to create comprehensive requirements for the next iteration
func formatRequirementsWithReview(originalReqs string, reviewMsgs []string, workDone string, latestReview string) string {
	var b strings.Builder
	b.WriteString("#START Original Requirements\n\n")
	b.WriteString(originalReqs)
	b.WriteString("#END Original Requirements\n\n")

	if len(reviewMsgs) > 0 {
		b.WriteString("\n\nHere is some historical review feedback that was incorporated into the work done:\n")
		for i, msg := range reviewMsgs {
			b.WriteString(fmt.Sprintf("%d. %s\n", i+1, msg))
		}
	}

	if workDone != "" {
		b.WriteString("\n\nWork Done So Far:\n\n")
		b.WriteString(workDone)
	}

	b.WriteString("\n\nGiven the above context, please address the following latest review feedback:\n\n")
	b.WriteString(latestReview)

	return b.String()
}

func BasicDevWorkflow(ctx workflow.Context, input BasicDevWorkflowInput) (result string, err error) {
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

	dCtx, err := SetupDevContext(ctx, input.WorkspaceId, input.RepoDir, string(input.EnvType), input.BasicDevOptions.StartBranch, input.Requirements)
	if err != nil {
		_ = signalWorkflowClosure(ctx, "failed")
		return "", err
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
		return "", err
	}

	requirements := input.Requirements
	if input.DetermineRequirements {
		devRequirements, err := BuildDevRequirements(dCtx, InitialDevRequirementsInfo{Requirements: requirements})
		if err != nil {
			return "", err
		}
		requirements = devRequirements.String()
	}

	v := workflow.GetVersion(dCtx, "basic-dev-parent-subflow", workflow.DefaultVersion, 1)
	if v == 1 {
		result, err = RunSubflow(dCtx, "coding", "Coding", func(subflow domain.Subflow) (string, error) {
			return codingSubflow(dCtx, requirements, input.BasicDevOptions.StartBranch)
		})
	} else {
		result, err = codingSubflow(dCtx, requirements, input.BasicDevOptions.StartBranch)
	}

	if err != nil {
		return "", err
	}

	worktreeMergeVersion := workflow.GetVersion(dCtx, "worktree-merge", workflow.DefaultVersion, 1)
	if dCtx.EnvContainer.Env.GetType() == env.EnvTypeLocalGitWorktree && worktreeMergeVersion >= 1 {
		params := MergeWithReviewParams{
			CommitRequired: true,
			Requirements:   requirements,
			StartBranch:    input.StartBranch,
			GetGitDiff:     nil,
		}
		err = reviewAndResolve(dCtx, params)
		if err != nil {
			return "", err
		}
	}

	if worktreeMergeVersion >= 1 {
		err = signalWorkflowClosure(dCtx, "completed")
		if err != nil {
			return "", fmt.Errorf("failed to signal workflow closure: %v", err)
		}
	}

	return result, nil
}

func codingSubflow(dCtx DevContext, requirements string, startBranch *string) (result string, err error) {
	codeContext, fullCodeContext, err := PrepareInitialCodeContext(dCtx, requirements, nil, nil)
	contextSizeExtension := len(fullCodeContext) - len(codeContext)
	if err != nil {
		return "", fmt.Errorf("failed to prepare code context: %v", err)
	}
	testResult := TestResult{Output: ""}

	// TODO wrap chatHistory in custom struct that has additional metadata about
	// each message for easy manipulation of chat history and determining when
	// we need to re-inject requirements and code context that gets lost
	// TODO store chat history in a way that can be referred to by id, and pass
	// id to the activities to avoid bloating temporal db
	chatHistory := &[]llm.ChatMessage{}

	maxAttempts := 17
	repoConfig := dCtx.RepoConfig
	if repoConfig.MaxIterations > 0 {
		maxAttempts = repoConfig.MaxIterations
	}

	attemptCount := 0
	var promptInfo PromptInfo
	initialCodeInfo := InitialCodeInfo{CodeContext: codeContext, Requirements: requirements}
	promptInfo = initialCodeInfo
	var fulfillment CriteriaFulfillment
	for {
		overallName := "Basic Dev"
		subflowName := fmt.Sprintf("%s (%d)", overallName, attemptCount+1)
		if subflowName == fmt.Sprintf("%s (1)", overallName) {
			subflowName = overallName
		}
		dCtx.FlowScope.SubflowName = subflowName

		// TODO /gen use models slice and modelIndex and modelAttemptCount just like
		// in completeDevStep to switch models when ErrMaxIterationsReached
		modelConfig := dCtx.GetModelConfig(common.CodingKey, attemptCount/3, "default")

		// TODO don't force getting help if it just got help recently already
		if attemptCount > 0 && attemptCount%3 == 0 {
			guidanceContext := "Failing repeatedly to pass tests and/or fulfill requirements, please provide guidance."

			// get the latest git diff, since it could be different from the
			// last time we got it, if we ever did
			gitDiff, diffErr := git.GitDiff(dCtx.ExecContext)
			if diffErr != nil {
				return "", fmt.Errorf("failed to get git diff: %v", diffErr)
			}

			requestParams := map[string]any{
				"gitDiff":     gitDiff,
				"testResult":  testResult,  // always latest
				"fulfillment": fulfillment, // always latest
			}

			promptInfo, err = GetUserFeedback(dCtx, promptInfo, guidanceContext, chatHistory, requestParams)
			if err != nil {
				return "", fmt.Errorf("failed to get user feedback: %v", err)
			}
		}
		if attemptCount >= maxAttempts {
			return "", errors.New("failed to author code passing tests and fulfilling requirements, max attempts reached")
		}

		// Step 2: edit code
		err = EditCode(dCtx, modelConfig, contextSizeExtension, chatHistory, promptInfo)
		if err != nil {
			return "", fmt.Errorf("failed to write edit blocks: %v", err)
		}

		// Step 3: run tests
		testResult, err = RunTests(dCtx, dCtx.RepoConfig.TestCommands)
		if err != nil {
			return "", fmt.Errorf("failed to run tests: %v", err)
		}

		if !testResult.TestsPassed {
			promptInfo = FeedbackInfo{Feedback: testResult.Output}
			attemptCount++
			continue
		}

		// Run integration tests if regular tests passed and integration tests are configured
		if len(dCtx.RepoConfig.IntegrationTestCommands) > 0 {
			integrationTestResult, err := RunTests(dCtx, dCtx.RepoConfig.IntegrationTestCommands)
			if err != nil {
				return "", fmt.Errorf("failed to run integration tests: %v", err)
			}
			if !integrationTestResult.TestsPassed {
				promptInfo = FeedbackInfo{Feedback: integrationTestResult.Output}
				attemptCount++
				continue
			}
		}

		// Step 4: check diff and confirm if requirements have been met
		fulfillment, err = CheckWorkMeetsCriteria(dCtx, CheckWorkInfo{
			Requirements: requirements,
		})
		if err != nil {
			return "", fmt.Errorf("failed to check if requirements are fulfilled: %v", err)
		}
		if fulfillment.IsFulfilled {
			break
		} else {
			// when we get back that requirements are not fulfilled, we often
			// get an immediate call to get_help_or_input trying to understand
			// why requirements aren't fulfilled or what to do. this is usually
			// not necessary.

			// we already have the requirements fulfillment analysis, so the
			// additional feedback is not needed if we include that in the chat
			// history. we skip saying any more from the user or system role in
			// this case and let the assistant "prompt itself". hoping this
			// makes it less likely for the assistant to act confused, since it
			// knows it itself said the requirements weren't fulfilled and why.
			*chatHistory = append(*chatHistory, llm.ChatMessage{
				Role: llm.ChatMessageRoleUser,
				Content: fmt.Sprintf(`
Here is the diff:

  [...] (Omitted for length)

And here are test results:

  Tests Passed: %v
  [...] (Omitted for length)

Please analyze whether the requirements have been fulfilled. If not, continue editing code as needed.
`, testResult.TestsPassed),
			})
			*chatHistory = append(*chatHistory, llm.ChatMessage{
				Role: llm.ChatMessageRoleAssistant,
				Content: fmt.Sprintf(`
The requirements were not fulfilled.

Analysis: %s

Feedback: %s`, fulfillment.Analysis, fulfillment.FeedbackMessage),
			})
			promptInfo = SkipInfo{}
			attemptCount++
			continue
		}
	}

	// Step 5: auto-format code
	err = AutoFormatCode(dCtx)
	if err != nil {
		return "", err
	}

	// NOTE: this version applies when the env type is not git worktree too,
	// since it affects when/where workflow closure occurs (moves it to after
	// coding subflow ends)
	worktreeMergeVersion := workflow.GetVersion(dCtx, "worktree-merge", workflow.DefaultVersion, 1)
	if worktreeMergeVersion >= 1 {
		// signal later. we want to run review iterations *after* coding and not
		// merge here in this version
		return testResult.Output, nil
	}

	if dCtx.EnvContainer.Env.GetType() == env.EnvTypeLocalGitWorktree {
		params := MergeWithReviewParams{
			CommitRequired: true,
			Requirements:   requirements,
			StartBranch:    startBranch,
			GetGitDiff: func(dCtx DevContext, baseBranch string) (string, error) {
				return git.GitDiff(dCtx.ExecContext)
			},
		}
		_, _, err = mergeWorktreeIfApproved(dCtx, params)
		if err != nil {
			return "", fmt.Errorf("failed to merge if approved: %v", err)
		}
	}

	// Emit signal when workflow ends successfully
	err = signalWorkflowClosure(dCtx, "completed")
	if err != nil {
		return "", fmt.Errorf("failed to signal workflow closure: %v", err)
	}

	return testResult.Output, nil
}

func getMergeApproval(dCtx DevContext, defaultTarget string, getGitDiff func(dCtx DevContext, baseBranch string) (string, error)) (MergeApprovalResponse, string, error) {
	// Generate initial diff with default target branch
	// This is also the diff used in any followups (we don't use the diff
	// against an updated target branch selection from the user, as that could
	// show work done that was well out of scope of the task being done, thus
	// confusing the LLM)
	gitDiff, err := getGitDiff(dCtx, defaultTarget)
	if err != nil {
		return MergeApprovalResponse{}, "", fmt.Errorf("failed to generate git diff: %w", err)
	}

	// Request merge approval from user
	mergeParams := MergeApprovalParams{
		SourceBranch:        dCtx.Worktree.Name,
		DefaultTargetBranch: defaultTarget,
		Diff:                gitDiff,
	}

	approvalResponse, err := GetUserMergeApproval(dCtx, "Please review these changes", map[string]any{
		"mergeApprovalInfo": mergeParams,
	}, getGitDiff)
	if err != nil {
		return MergeApprovalResponse{}, "", err
	}

	return approvalResponse, gitDiff, nil
}

// try to review and merge if approved. if not approved, iterate by coding some
// more based on review feedback, then review again
func reviewAndResolve(dCtx DevContext, params MergeWithReviewParams) error {
	if params.GetGitDiff == nil {
		params.GetGitDiff = func(dCtx DevContext, baseBranch string) (string, error) {
			// Get diff between branches using three-dot syntax (also including staged for changes made during reviewAndResolve)
			var gitDiff string
			err := workflow.ExecuteActivity(dCtx, git.GitDiffActivity, dCtx.EnvContainer, git.GitDiffParams{
				Staged:       true,
				ThreeDotDiff: true,
				BaseBranch:   baseBranch,
			}).Get(dCtx, &gitDiff)
			return gitDiff, err
		}
	}

	return RunSubflowWithoutResult(dCtx, "review_and_resolve", "Review and resolve", func(subflow domain.Subflow) error {
		// Track review messages for iterative development
		reviewMessages := []string{}
		originalRequirements := params.Requirements

		for {
			// Ensure any auto-formatted changes are staged for new workflow versions
			gitDiff, mergeInfo, err := mergeWorktreeIfApproved(dCtx, params)
			if err != nil {
				return err
			}

			if !mergeInfo.Approved {
				// retain new choice of target branch next iteration, in case it was changed
				params.StartBranch = &mergeInfo.TargetBranch

				// Format new requirements with review history + latest rejection message
				requirements := formatRequirementsWithReview(
					originalRequirements,
					reviewMessages,
					gitDiff,
					mergeInfo.Message,
				)

				// Add rejection message to history for next iteration
				reviewMessages = append(reviewMessages, mergeInfo.Message)

				// must commit before merge at this point, as codingSubflow
				// doesn't do so inherently
				params.CommitRequired = true
				_, err = codingSubflow(dCtx, requirements, params.StartBranch)
				if err != nil {
					return err
				}

				continue
			}

			return nil
		}
	})
}

func mergeWorktreeIfApproved(dCtx DevContext, params MergeWithReviewParams) (string, MergeApprovalResponse, error) {
	defaultTarget := "main"
	if params.StartBranch != nil {
		defaultTarget = *params.StartBranch
	}

	gitAddVersion := workflow.GetVersion(dCtx, "git-add-before-diff", workflow.DefaultVersion, 1)
	if gitAddVersion == 1 {
		if err := git.GitAddAll(dCtx.ExecContext); err != nil {
			return "", MergeApprovalResponse{}, fmt.Errorf("failed to git add all: %v", err)
		}
	}

	mergeInfo, gitDiff, err := getMergeApproval(dCtx, defaultTarget, params.GetGitDiff)
	if err != nil {
		return "", MergeApprovalResponse{}, fmt.Errorf("failed to get merge approval: %v", err)
	}

	if !mergeInfo.Approved {
		return gitDiff, mergeInfo, err
	}

	// Perform merge
	actionCtx := dCtx.NewActionContext("merge")
	actionCtx.ActionParams = map[string]interface{}{
		"sourceBranch": dCtx.Worktree.Name,
		"targetBranch": mergeInfo.TargetBranch,
	}

	// Commit any pending changes first
	commitMessage := strings.TrimSpace(params.Requirements)
	if strings.Contains(commitMessage, "Overview:\n") {
		commitMessage = strings.Split(commitMessage, "Overview:\n")[1]
		commitMessage = strings.TrimSpace(commitMessage)
	}
	commitMessage = strings.Split(commitMessage, "\n")[0]
	if len(commitMessage) > 100 {
		commitMessage = commitMessage[:100] + "...\n\n..." + commitMessage[100:]
	}

	gitCommitVersion := workflow.GetVersion(dCtx, "git-commit-in-flow-action", workflow.DefaultVersion, 1)
	if gitCommitVersion < 1 {
		err = workflow.ExecuteActivity(dCtx, git.GitCommitActivity, dCtx.EnvContainer, git.GitCommitParams{
			CommitMessage: commitMessage,
		}).Get(dCtx, nil)
		if err != nil {
			return "", MergeApprovalResponse{}, fmt.Errorf("failed to commit changes: %v", err)
		}
	}

	mergeResult, err := Track(actionCtx, func(flowAction domain.FlowAction) (git.MergeActivityResult, error) {
		var mergeResult git.MergeActivityResult

		if gitCommitVersion >= 1 && params.CommitRequired {
			err = workflow.ExecuteActivity(dCtx, git.GitCommitActivity, dCtx.EnvContainer, git.GitCommitParams{
				CommitMessage: commitMessage,
			}).Get(dCtx, nil)
			if err != nil {
				return mergeResult, fmt.Errorf("failed to commit changes: %v", err)
			}
		}

		future := workflow.ExecuteActivity(dCtx, git.GitMergeActivity, dCtx.EnvContainer, git.GitMergeParams{
			SourceBranch: dCtx.Worktree.Name,
			TargetBranch: mergeInfo.TargetBranch,
		})
		err := future.Get(dCtx, &mergeResult)
		if err != nil {
			return mergeResult, fmt.Errorf("failed to merge branches: %v", err)
		}
		return mergeResult, nil
	})
	if err != nil {
		return "", MergeApprovalResponse{}, err
	}

	if mergeResult.HasConflicts {
		// Present continue request with enhanced conflict message
		actionCtx := dCtx.NewActionContext("user_request.continue")
		var conflictMessage string
		if mergeResult.ConflictOnTargetBranch {
			conflictMessage = fmt.Sprintf("Merge conflicts detected at %s. Please resolve conflicts and commit the merge, then continue.", mergeResult.ConflictDirPath)
		} else {
			conflictMessage = fmt.Sprintf("Merge conflicts detected at %s. Conflicts are from merging %s into %s. Please resolve conflicts, commit the merge, then continue.", mergeResult.ConflictDirPath, mergeInfo.TargetBranch, dCtx.Worktree.Name)
		}

		err := GetUserContinue(actionCtx, conflictMessage, map[string]any{
			"continueTag": "done",
		})
		if err != nil {
			return "", MergeApprovalResponse{}, fmt.Errorf("failed to get continue approval: %v", err)
		}

		// Handle reverse conflict scenario - need final merge from source to target
		if !mergeResult.ConflictOnTargetBranch {
			for {
				// Verify conflicts are resolved by checking git status
				// FIXME let's check if MERGE_HEAD exists instead as better way to confirm conflicts are resolved
				var statusOutput env.EnvRunCommandActivityOutput
				statusFuture := workflow.ExecuteActivity(dCtx, env.EnvRunCommandActivity, env.EnvRunCommandActivityInput{
					EnvContainer: *dCtx.EnvContainer,
					Command:      "git",
					Args:         []string{"status", "--porcelain"},
				})
				statusErr := statusFuture.Get(dCtx, &statusOutput)
				if statusErr != nil {
					return "", MergeApprovalResponse{}, fmt.Errorf("failed to check git status: %v", statusErr)
				}

				// Check if there are still unmerged files
				if strings.Contains(statusOutput.Stdout, "UU ") || strings.Contains(statusOutput.Stdout, "AA ") || strings.Contains(statusOutput.Stdout, "DD ") {
					message := "Merge conflicts are not fully resolved, please resolve all conflicts and commit. Git status:\n\n" + statusOutput.Stdout
					err := GetUserContinue(actionCtx, message, map[string]any{
						"continueTag": "done",
					})
					if err != nil {
						return "", MergeApprovalResponse{}, fmt.Errorf("failed to get continue approval: %v", err)
					}
				}
				break
			}

			mergeInfo, gitDiff, err = getMergeApproval(dCtx, mergeInfo.TargetBranch, params.GetGitDiff)
			if err != nil {
				return "", MergeApprovalResponse{}, fmt.Errorf("failed to get final merge approval: %v", err)
			}

			if mergeInfo.Approved {
				// Perform final merge from source to target
				finalActionCtx := dCtx.NewActionContext("merge")
				finalActionCtx.ActionParams = map[string]interface{}{
					"sourceBranch": dCtx.Worktree.Name,
					"targetBranch": mergeInfo.TargetBranch,
				}

				finalMergeResult, err := Track(finalActionCtx, func(flowAction domain.FlowAction) (git.MergeActivityResult, error) {
					var finalResult git.MergeActivityResult
					future := workflow.ExecuteActivity(dCtx, git.GitMergeActivity, dCtx.EnvContainer, git.GitMergeParams{
						SourceBranch: dCtx.Worktree.Name,
						TargetBranch: mergeInfo.TargetBranch,
					})
					err := future.Get(dCtx, &finalResult)
					if err != nil {
						return finalResult, fmt.Errorf("failed to perform final merge: %v", err)
					}
					return finalResult, nil
				})
				if err != nil {
					return "", MergeApprovalResponse{}, err
				}

				// Update mergeResult to reflect final merge result
				mergeResult = finalMergeResult
			}
		}
	}

	// After successful merge, cleanup the worktree
	if !mergeResult.HasConflicts && dCtx.Worktree != nil {
		actionCtx := dCtx.NewActionContext("cleanup_worktree")
		_, err := flow_action.TrackFailureOnly(actionCtx.FlowActionContext(), func(flowAction domain.FlowAction) (interface{}, error) {
			future := workflow.ExecuteActivity(dCtx, git.CleanupWorktreeActivity, dCtx.EnvContainer, dCtx.EnvContainer.Env.GetWorkingDirectory(), dCtx.Worktree.Name, "Sidekick task completed and merged")
			return nil, future.Get(dCtx, nil)
		})
		if err != nil {
			// Log the error but don't fail the workflow since merge was successful
			workflow.GetLogger(dCtx).Error("Failed to cleanup worktree", "error", err)
		}
	}

	return gitDiff, mergeInfo, err
}
