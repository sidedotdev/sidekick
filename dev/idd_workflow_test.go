package dev

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"

	"sidekick/coding/git"
	"sidekick/common"
	"sidekick/domain"
	"sidekick/env"
	"sidekick/flow_action"
	"sidekick/llm2"
	"sidekick/persisted_ai"
	"sidekick/secret_manager"
	"sidekick/srv"
	"sidekick/utils"
	"sidekick/workspace"
)

// IddWorkflowTestSuite verifies that an intent sub-task commits the current
// intent state in the IDD worktree and launches a BasicDevWorkflow child wired
// to merge back into that same worktree branch.
type IddWorkflowTestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	env *testsuite.TestWorkflowEnvironment
	ima *DevAgentManagerActivities
	// titleGenerationCalls counts LLM stream calls made by
	// generateIntentSubtaskTitle so tests can pin how many title generations a
	// single sub-task dispatch performs.
	titleGenerationCalls int
	sideTmpSetupCalls    []env.EnvRunCommandActivityInput
}

func (s *IddWorkflowTestSuite) SetupTest() {
	s.env = s.NewTestWorkflowEnvironment()
	s.env.SetWorkerOptions(utils.TestWorkerOptions())
	s.ima = nil
	s.titleGenerationCalls = 0
	s.sideTmpSetupCalls = nil
}

func (s *IddWorkflowTestSuite) AfterTest(suiteName, testName string) {
	s.env.AssertExpectations(s.T())
}

// setupTitleGenerationMocks stubs the chat-history and LLM stream activities used
// by generateIntentSubtaskTitle so the sub-task gets a deterministic title.
func (s *IddWorkflowTestSuite) setupTitleGenerationMocks() {
	var fa *flow_action.FlowActivities
	s.env.OnActivity(fa.PersistFlowAction, mock.Anything, mock.Anything).Return(nil).Maybe()

	var cha *persisted_ai.ChatHistoryActivities
	s.env.OnActivity(cha.AppendMessage, mock.Anything, mock.Anything).Return(
		&persisted_ai.MessageRef{BlockKeys: []string{"mock-block"}, Role: "user"}, nil,
	).Maybe()

	var la *persisted_ai.Llm2Activities
	s.env.OnActivity(la.Stream, mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		s.titleGenerationCalls++
	}).Return(&llm2.MessageResponse{
		Output: llm2.Message{
			Role: "assistant",
			Content: []llm2.ContentBlock{
				{Type: llm2.ContentBlockTypeText, Text: "Generated Sub-task Title"},
			},
		},
	}, nil).Maybe()
}

// TestRunIntentSubtaskCommitsAndStartsChild drives runIntentSubtask directly via
// a wrapper workflow so we can supply a ready DevContext (with the IDD worktree)
// rather than mocking the entire SetupDevContext path. It asserts the intent is
// committed and that the resulting BasicDevWorkflow child targets the IDD
// worktree branch with the expected sub-task options.
func (s *IddWorkflowTestSuite) TestRunIntentSubtaskCommitsAndStartsChild() {
	const iddBranch = "side/idd-worktree"

	maxIterations := 7
	mission := "ship the intent"
	requestedStartBranch := "main"
	selectedOptions := IddOptions{
		EnvType:           env.EnvTypeModal,
		RepoMode:          env.RepoModeInPlace,
		StartBranch:       &requestedStartBranch,
		ContextGatherType: ContextGatherTypeExplore,
		ConfigOverrides: common.ConfigOverrides{
			MaxIterations: &maxIterations,
			Mission:       &mission,
		},
	}

	var capturedInput BasicDevWorkflowInput
	s.env.RegisterWorkflowWithOptions(
		func(ctx workflow.Context, input BasicDevWorkflowInput) (string, error) {
			capturedInput = input
			return "done", nil
		},
		workflow.RegisterOptions{Name: "BasicDevWorkflow"},
	)

	s.env.OnActivity(git.GitAddActivity, mock.Anything, mock.MatchedBy(func(input git.GitAddActivityInput) bool {
		return input.Path == "."
	})).Return(nil).Once()

	s.env.OnActivity(git.GitCommitActivity, mock.Anything, mock.Anything, mock.MatchedBy(func(params git.GitCommitParams) bool {
		return params.CommitAll && params.IgnoreNothingToCommit
	})).Return("commit-sha", nil).Once()

	s.env.OnActivity(env.EnvRunCommandActivity, mock.Anything, mock.MatchedBy(func(in env.EnvRunCommandActivityInput) bool {
		return len(in.Args) > 0 && in.Args[0] == "rev-parse"
	})).Return(env.EnvRunCommandActivityOutput{Stdout: "abc123\n", ExitStatus: 0}, nil).Once()

	s.env.OnActivity(env.EnvRunCommandActivity, mock.Anything, mock.MatchedBy(func(in env.EnvRunCommandActivityInput) bool {
		return len(in.Args) > 0 && in.Args[0] == "show"
	})).Return(env.EnvRunCommandActivityOutput{Stdout: "diff body", ExitStatus: 0}, nil).Twice()

	// The sub-task's flow record must reflect every state the canvas shows:
	// reserved, running (with its generated title) and terminal.
	var mu sync.Mutex
	persistedFlows := map[string]domain.Flow{}
	s.env.OnActivity(
		s.ima.PutWorkflow,
		mock.Anything,
		mock.AnythingOfType("domain.Flow"),
	).Return(func(ctx context.Context, flow domain.Flow) error {
		mu.Lock()
		defer mu.Unlock()
		persistedFlows[flow.Status] = flow
		return nil
	})

	s.setupTitleGenerationMocks()

	wrapper := func(ctx workflow.Context) (IddState, error) {
		ctx = utils.NoRetryCtx(ctx)
		gs := &flow_action.GlobalState{}
		gs.InitValues()
		dCtx := DevContext{
			ExecContext: flow_action.ExecContext{
				WorkspaceId: "test-workspace",
				Context:     ctx,
				FlowScope:   &flow_action.FlowScope{SubflowName: "idd"},
				GlobalState: gs,
				Secrets:     &secret_manager.SecretManagerContainer{SecretManager: &secret_manager.EnvSecretManager{}},
				EnvContainer: &env.EnvContainer{
					Env: &env.LocalEnv{WorkingDirectory: "/tmp/test-repo"},
				},
			},
			Worktree:   &domain.Worktree{Name: iddBranch},
			RepoConfig: common.RepoConfig{},
		}
		dCtx.SetLLMConfig(common.LLMConfig{
			Defaults: []common.ModelConfig{{Provider: "openai"}},
		})
		state := &IddState{}
		iddInput := IddWorkflowInput{
			WorkspaceId: "test-workspace",
			RepoDir:     "/tmp/repo",
			TaskId:      "task-1",
			Title:       "My Intent",
			IddOptions:  selectedOptions,
		}
		flowId := reservePendingSubtask(dCtx, iddInput, state, "")
		runIntentSubtask(dCtx, iddInput, StartIntentSubtaskSignal{Update: false}, state, flowId, nil)
		if len(state.Subtasks) != 1 {
			return IddState{}, fmt.Errorf("expected 1 subtask, got %d", len(state.Subtasks))
		}
		return *state, nil
	}
	s.env.RegisterWorkflow(wrapper)

	s.env.ExecuteWorkflow(wrapper)
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var state IddState
	s.NoError(s.env.GetWorkflowResult(&state))
	s.Require().Len(state.Subtasks, 1)
	s.Equal("abc123", state.Subtasks[0].Commit)
	s.Equal("completed", state.Subtasks[0].Status)
	s.Equal("Generated Sub-task Title", state.Subtasks[0].Title)

	mu.Lock()
	defer mu.Unlock()
	s.Require().Len(persistedFlows, 3)
	pending := persistedFlows["pending"]
	s.Equal("task-1", pending.ParentId)
	s.Equal(domain.FlowTypeBasicDev, pending.Type)
	s.Equal("test-workspace", pending.WorkspaceId)
	s.Equal(state.Subtasks[0].FlowId, pending.Id)
	s.Equal("Generated Sub-task Title", persistedFlows["in_progress"].Title)
	s.Equal("Generated Sub-task Title", persistedFlows["completed"].Title)
	s.False(persistedFlows["completed"].Updated.IsZero())

	s.False(capturedInput.DetermineRequirements)
	s.True(capturedInput.AutoMerge)
	s.True(capturedInput.Idd)
	s.Equal(env.EnvTypeModal, capturedInput.EnvType)
	s.Equal(env.RepoModeInPlace, capturedInput.RepoMode)
	s.Equal(ContextGatherTypeExplore, capturedInput.ContextGatherType)
	s.Require().NotNil(capturedInput.ConfigOverrides.MaxIterations)
	s.Equal(maxIterations, *capturedInput.ConfigOverrides.MaxIterations)
	s.Require().NotNil(capturedInput.ConfigOverrides.Mission)
	s.Equal(mission, *capturedInput.ConfigOverrides.Mission)
	s.Equal("test-workspace", capturedInput.WorkspaceId)
	s.Equal("/tmp/repo", capturedInput.RepoDir)
	s.Require().NotNil(capturedInput.StartBranch)
	s.Equal(iddBranch, *capturedInput.StartBranch,
		"the child starts from the current IDD branch, not the branch requested for the IDD parent")
	s.NotEqual(requestedStartBranch, *capturedInput.StartBranch)
	s.Contains(capturedInput.Requirements, "The following new intent file has already been committed")
	s.Contains(capturedInput.Requirements, "git show abc123")
	s.Contains(capturedInput.Requirements, "diff body")
	s.Equal(1, s.titleGenerationCalls, "a sub-task should generate its title exactly once")
}

// TestRunIntentSubtaskTriggersOrchestratorOnTerminalStatus verifies that once
// the sub-task child workflow reaches a terminal status, runIntentSubtask
// requests an orchestrator turn (exactly once, after the terminal status is
// recorded) so remaining un-dispatched intent is re-evaluated promptly rather
// than waiting for the next intent edit or the edit watcher's backstop.
func (s *IddWorkflowTestSuite) TestRunIntentSubtaskTriggersOrchestratorOnTerminalStatus() {
	const iddBranch = "side/idd-worktree"

	s.env.RegisterWorkflowWithOptions(
		func(ctx workflow.Context, input BasicDevWorkflowInput) (string, error) {
			return "done", nil
		},
		workflow.RegisterOptions{Name: "BasicDevWorkflow"},
	)

	s.env.OnActivity(git.GitAddActivity, mock.Anything, mock.Anything).Return(nil).Once()
	s.env.OnActivity(git.GitCommitActivity, mock.Anything, mock.Anything, mock.Anything).Return("commit-sha", nil).Once()

	s.env.OnActivity(env.EnvRunCommandActivity, mock.Anything, mock.MatchedBy(func(in env.EnvRunCommandActivityInput) bool {
		return len(in.Args) > 0 && in.Args[0] == "rev-parse"
	})).Return(env.EnvRunCommandActivityOutput{Stdout: "abc123\n", ExitStatus: 0}, nil).Once()

	s.env.OnActivity(env.EnvRunCommandActivity, mock.Anything, mock.MatchedBy(func(in env.EnvRunCommandActivityInput) bool {
		return len(in.Args) > 0 && in.Args[0] == "show"
	})).Return(env.EnvRunCommandActivityOutput{Stdout: "diff body", ExitStatus: 0}, nil).Twice()

	s.env.OnActivity(
		s.ima.PutWorkflow,
		mock.Anything,
		mock.AnythingOfType("domain.Flow"),
	).Return(nil)

	s.setupTitleGenerationMocks()

	type triggerResult struct {
		Count           int
		StatusAtTrigger string
		Notices         []string
	}

	wrapper := func(ctx workflow.Context) (triggerResult, error) {
		ctx = utils.NoRetryCtx(ctx)
		gs := &flow_action.GlobalState{}
		gs.InitValues()
		dCtx := DevContext{
			ExecContext: flow_action.ExecContext{
				WorkspaceId: "test-workspace",
				Context:     ctx,
				FlowScope:   &flow_action.FlowScope{SubflowName: "idd"},
				GlobalState: gs,
				Secrets:     &secret_manager.SecretManagerContainer{SecretManager: &secret_manager.EnvSecretManager{}},
				EnvContainer: &env.EnvContainer{
					Env: &env.LocalEnv{WorkingDirectory: "/tmp/test-repo"},
				},
			},
			Worktree:   &domain.Worktree{Name: iddBranch},
			RepoConfig: common.RepoConfig{},
		}
		dCtx.SetLLMConfig(common.LLMConfig{
			Defaults: []common.ModelConfig{{Provider: "openai"}},
		})
		state := &IddState{}
		iddInput := IddWorkflowInput{
			WorkspaceId: "test-workspace",
			RepoDir:     "/tmp/repo",
			TaskId:      "task-1",
			Title:       "My Intent",
			IddOptions: IddOptions{
				EnvType:  env.EnvTypeLocal,
				RepoMode: env.RepoModeWorktree,
			},
		}
		flowId := reservePendingSubtask(dCtx, iddInput, state, "")
		var result triggerResult
		runIntentSubtask(dCtx, iddInput, StartIntentSubtaskSignal{Update: false}, state, flowId, func() {
			result.Count++
			result.StatusAtTrigger = state.Subtasks[0].Status
			result.Notices = append([]string{}, state.PendingSubtaskNotices...)
		})
		return result, nil
	}
	s.env.RegisterWorkflow(wrapper)

	s.env.ExecuteWorkflow(wrapper)
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var result triggerResult
	s.NoError(s.env.GetWorkflowResult(&result))
	s.Equal(1, result.Count, "orchestrator turn should be requested exactly once")
	s.Equal("completed", result.StatusAtTrigger, "trigger should fire after the terminal status is recorded")
	s.Require().Len(result.Notices, 1, "a terminal-status notice should be queued before the trigger fires")
	s.Contains(result.Notices[0], `"Generated Sub-task Title"`)
	s.Contains(result.Notices[0], "complete")
	s.Contains(result.Notices[0], "Result: done", "the child workflow result should be included in the notice")
}

// newIddMergeApprovalDevContext builds a DevContext resembling the one the IDD
// workflow sets up once its worktree exists.
func newIddMergeApprovalDevContext(ctx workflow.Context, iddBranch string) DevContext {
	gs := &flow_action.GlobalState{}
	gs.InitValues()
	dCtx := DevContext{
		ExecContext: flow_action.ExecContext{
			WorkspaceId: "test-workspace",
			Context:     ctx,
			FlowScope:   &flow_action.FlowScope{SubflowName: "idd"},
			GlobalState: gs,
			Secrets:     &secret_manager.SecretManagerContainer{SecretManager: &secret_manager.EnvSecretManager{}},
			EnvContainer: &env.EnvContainer{
				Env: &env.LocalEnv{WorkingDirectory: "/tmp/test-repo"},
			},
		},
		Worktree:   &domain.Worktree{Name: iddBranch},
		RepoConfig: common.RepoConfig{},
	}
	// sub-tasks dispatched from this context generate their own titles
	dCtx.SetLLMConfig(common.LLMConfig{
		Defaults: []common.ModelConfig{{Provider: "openai"}},
	})
	return dCtx
}

func iddMergeApprovalInput() IddWorkflowInput {
	return IddWorkflowInput{
		WorkspaceId: "test-workspace",
		RepoDir:     "/tmp/repo",
		TaskId:      "task-1",
		Title:       "My Intent",
	}
}

// recordMergeApprovalActions captures every write of the IDD merge-approval
// flow action, in the order the writes reached storage.
func (s *IddWorkflowTestSuite) recordMergeApprovalActions() (*sync.Mutex, *[]domain.FlowAction) {
	var mu sync.Mutex
	actions := &[]domain.FlowAction{}
	var fa *flow_action.FlowActivities
	s.env.OnActivity(fa.PersistFlowAction, mock.Anything, mock.Anything).Return(
		func(ctx context.Context, flowAction domain.FlowAction) error {
			mu.Lock()
			defer mu.Unlock()
			if flowAction.ActionType == iddMergeApprovalActionType {
				*actions = append(*actions, flowAction)
			}
			return nil
		})
	return &mu, actions
}

// mergeApprovalInfo extracts the merge approval params as they arrive at
// storage, i.e. after serialization.
func mergeApprovalInfo(action domain.FlowAction) map[string]interface{} {
	info, _ := action.ActionParams["mergeApprovalInfo"].(map[string]interface{})
	return info
}

// signalMergeApprovalResponse relays a user response to the pending
// merge-approval request, the way the API does when the canvas responds.
func (s *IddWorkflowTestSuite) signalMergeApprovalResponse(mu *sync.Mutex, actions *[]domain.FlowAction, response flow_action.UserResponse) {
	mu.Lock()
	s.Require().NotEmpty(*actions, "the merge approval request should have been raised")
	response.FlowActionId = (*actions)[0].Id
	mu.Unlock()
	s.env.SignalWorkflow(flow_action.UserResponseSignalName(response.FlowActionId), response)
}

// mockIntentCommitActivities stubs the activities commitIntent runs when the
// finish path commits whatever intent is left in the worktree, for the given
// number of finish attempts.
func (s *IddWorkflowTestSuite) mockIntentCommitActivities(attempts int) {
	s.env.OnActivity(git.GitAddActivity, mock.Anything, mock.MatchedBy(func(input git.GitAddActivityInput) bool {
		return input.Path == "."
	})).Return(nil).Times(attempts)

	s.env.OnActivity(git.GitCommitActivity, mock.Anything, mock.Anything, mock.MatchedBy(func(params git.GitCommitParams) bool {
		return params.CommitAll && params.IgnoreNothingToCommit
	})).Return("commit-sha", nil).Times(attempts)

	s.env.OnActivity(env.EnvRunCommandActivity, mock.Anything, mock.MatchedBy(func(in env.EnvRunCommandActivityInput) bool {
		return len(in.Args) == 2 && in.Args[0] == "rev-parse" && in.Args[1] == "HEAD"
	})).Return(env.EnvRunCommandActivityOutput{Stdout: "deadbeef\n", ExitStatus: 0}, nil).Times(attempts)

	s.env.OnActivity(env.EnvRunCommandActivity, mock.Anything, mock.MatchedBy(func(in env.EnvRunCommandActivityInput) bool {
		return len(in.Args) > 0 && in.Args[0] == "show"
	})).Return(env.EnvRunCommandActivityOutput{Stdout: "diff body", ExitStatus: 0}, nil).Times(2 * attempts)
}

// mockTargetCommit stubs resolving a branch tip, which the finish path pins
// before merging so its diff keeps covering everything it merged even once the
// target has moved on.
func (s *IddWorkflowTestSuite) mockTargetCommit(branch, commit string) {
	s.env.OnActivity(env.EnvRunCommandActivity, mock.Anything, mock.MatchedBy(func(in env.EnvRunCommandActivityInput) bool {
		return len(in.Args) == 2 && in.Args[0] == "rev-parse" && in.Args[1] == branch
	})).Return(env.EnvRunCommandActivityOutput{Stdout: commit + "\n", ExitStatus: 0}, nil)
}

// TestIddMergeApprovalReopensOnFinalDiffFailure verifies that failing to
// capture the diff being merged aborts the finish before anything destructive
// happens, re-opening the request with the error rather than completing it with
// no record of what was merged.
func (s *IddWorkflowTestSuite) TestIddMergeApprovalReopensOnFinalDiffFailure() {
	const iddBranch = "side/idd-worktree"
	const mergeBase = "main-tip-sha"

	mu, approvalActions := s.recordMergeApprovalActions()

	s.env.OnActivity(git.GitDiffActivity, mock.Anything, mock.Anything, mock.Anything).Return(
		func(ctx context.Context, envContainer env.EnvContainer, params git.GitDiffParams) (string, error) {
			if params.BaseRef == mergeBase {
				return "", errors.New("diff exploded")
			}
			return "initial diff", nil
		})

	s.mockIntentCommitActivities(1)
	s.mockTargetCommit("main", mergeBase)

	miniIdd := func(ctx workflow.Context) (string, error) {
		ctx = utils.NoRetryCtx(ctx)
		dCtx := newIddMergeApprovalDevContext(ctx, iddBranch)
		// surface the diff failure instead of prompting the user to retry it
		dCtx.ExecContext.DisableHumanInTheLoop = true
		state := &IddState{DefaultTargetBranch: "main"}
		ma := startIddMergeApproval(dCtx, iddMergeApprovalInput(), state)
		if err := workflow.Await(ctx, func() bool { return ma.mergeError != "" }); err != nil {
			return "", err
		}
		// let the re-opened request reach storage before the workflow ends
		if err := workflow.Sleep(ctx, time.Second); err != nil {
			return "", err
		}
		return ma.mergeError, nil
	}
	s.env.RegisterWorkflow(miniIdd)

	approved := true
	s.env.RegisterDelayedCallback(func() {
		s.signalMergeApprovalResponse(mu, approvalActions, flow_action.UserResponse{Approved: &approved})
	}, time.Second)

	s.env.ExecuteWorkflow(miniIdd)
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var finishError string
	s.NoError(s.env.GetWorkflowResult(&finishError))
	s.Contains(finishError, "diff exploded")
	s.env.AssertActivityNumberOfCalls(s.T(), "GitMergeActivity", 0)

	mu.Lock()
	defer mu.Unlock()
	reopened := (*approvalActions)[len(*approvalActions)-1]
	s.Equal(domain.ActionStatusPending, reopened.ActionStatus)
	s.Contains(reopened.ActionParams["mergeError"], "diff exploded")
	s.Equal("initial diff", mergeApprovalInfo(reopened)["diff"], "the request keeps the diff it was already showing")
}

// TestIddMergeApprovalPreservesMergedDiffAcrossFinishRetry verifies that a
// failure after a successful merge re-opens the request and that retrying the
// finish still shows everything that was merged: the diff is taken against the
// target's pinned pre-merge tip, so it survives the target moving on.
func (s *IddWorkflowTestSuite) TestIddMergeApprovalPreservesMergedDiffAcrossFinishRetry() {
	const iddBranch = "side/idd-worktree"
	const mergeBase = "main-tip-sha"

	mu, approvalActions := s.recordMergeApprovalActions()

	s.env.OnActivity(git.GitDiffActivity, mock.Anything, mock.Anything, mock.Anything).Return(
		func(ctx context.Context, envContainer env.EnvContainer, params git.GitDiffParams) (string, error) {
			if params.BaseRef == mergeBase {
				return "merged diff", nil
			}
			return "initial diff", nil
		})

	s.mockIntentCommitActivities(2)
	s.mockTargetCommit("main", mergeBase)
	s.env.OnActivity(git.GitMergeActivity, mock.Anything, mock.Anything, mock.Anything).
		Return(git.MergeActivityResult{HasConflicts: false}, nil).Twice()

	var cleanupMu sync.Mutex
	cleanupCalls := 0
	s.env.OnActivity(git.CleanupWorktreeActivity, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(
		func(ctx context.Context, envContainer env.EnvContainer, worktreePath, branchName, archiveMessage string) error {
			cleanupMu.Lock()
			defer cleanupMu.Unlock()
			cleanupCalls++
			if cleanupCalls == 1 {
				return errors.New("cleanup exploded")
			}
			return nil
		}).Twice()

	miniIdd := func(ctx workflow.Context) error {
		ctx = utils.NoRetryCtx(ctx)
		dCtx := newIddMergeApprovalDevContext(ctx, iddBranch)
		state := &IddState{DefaultTargetBranch: "main"}
		ma := startIddMergeApproval(dCtx, iddMergeApprovalInput(), state)
		ma.finishedCh.Receive(dCtx, nil)
		return nil
	}
	s.env.RegisterWorkflow(miniIdd)

	approved := true
	s.env.RegisterDelayedCallback(func() {
		s.signalMergeApprovalResponse(mu, approvalActions, flow_action.UserResponse{Approved: &approved})
	}, time.Second)
	s.env.RegisterDelayedCallback(func() {
		s.signalMergeApprovalResponse(mu, approvalActions, flow_action.UserResponse{Approved: &approved})
	}, 3*time.Second)

	s.env.ExecuteWorkflow(miniIdd)
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	mu.Lock()
	defer mu.Unlock()
	var reopenedErrors []string
	for _, action := range *approvalActions {
		if actionErr, ok := action.ActionParams["mergeError"].(string); ok && actionErr != "" {
			s.Equal(domain.ActionStatusPending, action.ActionStatus, "a failed finish must leave the request awaiting the user again")
			s.Equal("merged diff", mergeApprovalInfo(action)["diff"], "the re-opened request must show the diff captured before the merge")
			reopenedErrors = append(reopenedErrors, actionErr)
		}
	}
	s.Require().NotEmpty(reopenedErrors, "a cleanup failure must be surfaced as a finish failure")
	s.Contains(reopenedErrors[0], "cleanup exploded")

	final := (*approvalActions)[len(*approvalActions)-1]
	s.Equal(domain.ActionStatusComplete, final.ActionStatus)
	s.Equal("merged diff", mergeApprovalInfo(final)["diff"], "the diff of what was merged must survive a retried finish")
	// merging again is how a retry picks up anything that landed since, and is
	// a no-op when nothing did
	s.env.AssertActivityNumberOfCalls(s.T(), "GitMergeActivity", 2)
}

// TestIddMergeApprovalMergesWorkLandedAfterCleanupFailure verifies the retry
// TestIddMergeApprovalMergesWorkLandedAfterCleanupFailure verifies the retry
// invariant that matters most: a cleanup failure leaves the worktree in place,
// so intent edits can still be committed to its branch afterwards. Approving
// again must merge the revision that carries them, and the completed request
// must show that work alongside what the first attempt merged.
//
// The git activities are backed by a small model of the two branches — which
// rounds of work the idd branch holds and which of them the target has received
// — so revisions, not call counts, decide what each merge and diff sees.
func (s *IddWorkflowTestSuite) TestIddMergeApprovalMergesWorkLandedAfterCleanupFailure() {
	const iddBranch = "side/idd-worktree"

	mu, approvalActions := s.recordMergeApprovalActions()

	var repoMu sync.Mutex
	sourceRounds := []string{"round one diff"}
	// rounds of the idd branch each recorded target revision contains
	targetRevRounds := map[string]int{"main-rev-0": 0}
	targetRounds := 0
	intentEdited := false
	targetRev := func() string { return fmt.Sprintf("main-rev-%d", targetRounds) }
	sourceRev := func() string { return fmt.Sprintf("source-rev-%d", len(sourceRounds)) }

	s.env.OnActivity(git.GitAddActivity, mock.Anything, mock.Anything).Return(nil).Twice()
	s.env.OnActivity(git.GitCommitActivity, mock.Anything, mock.Anything, mock.Anything).Return(
		func(ctx context.Context, envContainer env.EnvContainer, params git.GitCommitParams) (string, error) {
			repoMu.Lock()
			defer repoMu.Unlock()
			// committing pending intent is what advances the idd branch
			if intentEdited {
				sourceRounds = append(sourceRounds, "round two diff")
				intentEdited = false
			}
			return sourceRev(), nil
		}).Twice()
	s.env.OnActivity(env.EnvRunCommandActivity, mock.Anything, mock.MatchedBy(func(in env.EnvRunCommandActivityInput) bool {
		return len(in.Args) == 2 && in.Args[0] == "rev-parse" && in.Args[1] == "HEAD"
	})).Return(func(ctx context.Context, in env.EnvRunCommandActivityInput) (env.EnvRunCommandActivityOutput, error) {
		repoMu.Lock()
		defer repoMu.Unlock()
		return env.EnvRunCommandActivityOutput{Stdout: sourceRev() + "\n", ExitStatus: 0}, nil
	})
	s.env.OnActivity(env.EnvRunCommandActivity, mock.Anything, mock.MatchedBy(func(in env.EnvRunCommandActivityInput) bool {
		return len(in.Args) > 0 && in.Args[0] == "show"
	})).Return(env.EnvRunCommandActivityOutput{Stdout: "diff body", ExitStatus: 0}, nil)

	s.env.OnActivity(env.EnvRunCommandActivity, mock.Anything, mock.MatchedBy(func(in env.EnvRunCommandActivityInput) bool {
		return len(in.Args) == 2 && in.Args[0] == "rev-parse" && in.Args[1] == "main"
	})).Return(func(ctx context.Context, in env.EnvRunCommandActivityInput) (env.EnvRunCommandActivityOutput, error) {
		repoMu.Lock()
		defer repoMu.Unlock()
		// resolving after the first merge yields the advanced revision, so
		// re-pinning the base would lose the first round
		return env.EnvRunCommandActivityOutput{Stdout: targetRev() + "\n", ExitStatus: 0}, nil
	})

	s.env.OnActivity(git.GitDiffActivity, mock.Anything, mock.Anything, mock.Anything).Return(
		func(ctx context.Context, envContainer env.EnvContainer, params git.GitDiffParams) (string, error) {
			repoMu.Lock()
			defer repoMu.Unlock()
			base, ok := targetRevRounds[params.BaseRef]
			if !ok {
				// diffing against the branch name sees wherever it stands now
				base = targetRounds
			}
			return strings.Join(sourceRounds[base:], "\n"), nil
		})

	var mergedRevs []string
	s.env.OnActivity(git.GitMergeActivity, mock.Anything, mock.Anything, mock.Anything).Return(
		func(ctx context.Context, envContainer env.EnvContainer, params git.GitMergeParams) (git.MergeActivityResult, error) {
			repoMu.Lock()
			defer repoMu.Unlock()
			s.Equal(iddBranch, params.SourceBranch)
			s.Equal("main", params.TargetBranch)
			mergedRevs = append(mergedRevs, sourceRev())
			targetRounds = len(sourceRounds)
			targetRevRounds[targetRev()] = targetRounds
			return git.MergeActivityResult{HasConflicts: false}, nil
		}).Twice()

	var cleanupMu sync.Mutex
	cleanupCalls := 0
	s.env.OnActivity(git.CleanupWorktreeActivity, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(
		func(ctx context.Context, envContainer env.EnvContainer, worktreePath, branchName, archiveMessage string) error {
			cleanupMu.Lock()
			defer cleanupMu.Unlock()
			cleanupCalls++
			if cleanupCalls == 1 {
				return errors.New("cleanup exploded")
			}
			return nil
		}).Twice()

	miniIdd := func(ctx workflow.Context) error {
		ctx = utils.NoRetryCtx(ctx)
		dCtx := newIddMergeApprovalDevContext(ctx, iddBranch)
		state := &IddState{DefaultTargetBranch: "main"}
		ma := startIddMergeApproval(dCtx, iddMergeApprovalInput(), state)
		ma.finishedCh.Receive(dCtx, nil)
		return nil
	}
	s.env.RegisterWorkflow(miniIdd)

	approved := true
	s.env.RegisterDelayedCallback(func() {
		s.signalMergeApprovalResponse(mu, approvalActions, flow_action.UserResponse{Approved: &approved})
	}, time.Second)
	// the worktree outlived the failed cleanup, so the user keeps editing intent
	s.env.RegisterDelayedCallback(func() {
		repoMu.Lock()
		defer repoMu.Unlock()
		intentEdited = true
	}, 2*time.Second)
	s.env.RegisterDelayedCallback(func() {
		s.signalMergeApprovalResponse(mu, approvalActions, flow_action.UserResponse{Approved: &approved})
	}, 3*time.Second)

	s.env.ExecuteWorkflow(miniIdd)
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	repoMu.Lock()
	s.Equal([]string{"source-rev-1", "source-rev-2"}, mergedRevs, "the retry must merge the revision holding the intent committed after the failed cleanup")
	s.Equal(len(sourceRounds), targetRounds, "the target must end up with every round of work from the idd branch")
	repoMu.Unlock()

	mu.Lock()
	defer mu.Unlock()
	final := (*approvalActions)[len(*approvalActions)-1]
	s.Equal(domain.ActionStatusComplete, final.ActionStatus)
	finalDiff, _ := mergeApprovalInfo(final)["diff"].(string)
	s.Contains(finalDiff, "round one diff", "the completed request must still show what the first attempt merged")
	s.Contains(finalDiff, "round two diff", "the completed request must also show the work merged by the retry")
}

// TestIddMergeApprovalFinishesOnApproval verifies the long-lived merge-approval
// request drives the finish: approving it commits pending intent, merges the
// idd worktree branch into the selected target, only then stops sub-tasks still
// in flight, cleans the worktree up and reports the finish to the workflow's
// main loop. The completed request must keep the merged diff, which is the only
// record left once the worktree is gone.
func (s *IddWorkflowTestSuite) TestIddMergeApprovalFinishesOnApproval() {
	const iddBranch = "side/idd-worktree"
	const targetBranch = "main"
	const subtaskFlowId = "flow_subtask"
	const mergeBase = "main-tip-sha"

	mu, approvalActions := s.recordMergeApprovalActions()

	var stepMu sync.Mutex
	var steps []string
	recordStep := func(step string) {
		stepMu.Lock()
		defer stepMu.Unlock()
		steps = append(steps, step)
	}

	s.env.OnActivity(git.GitDiffActivity, mock.Anything, mock.Anything, mock.Anything).Return(
		func(ctx context.Context, envContainer env.EnvContainer, params git.GitDiffParams) (string, error) {
			if params.BaseRef == mergeBase {
				return "merged diff", nil
			}
			return "initial diff", nil
		})

	s.mockIntentCommitActivities(1)
	s.mockTargetCommit(targetBranch, mergeBase)

	var capturedMergeParams git.GitMergeParams
	s.env.OnActivity(git.GitMergeActivity, mock.Anything, mock.Anything, mock.MatchedBy(func(params git.GitMergeParams) bool {
		capturedMergeParams = params
		return true
	})).Return(func(ctx context.Context, envContainer env.EnvContainer, params git.GitMergeParams) (git.MergeActivityResult, error) {
		recordStep("merge")
		return git.MergeActivityResult{HasConflicts: false}, nil
	}).Twice()

	s.env.OnActivity(git.CleanupWorktreeActivity, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(
		func(ctx context.Context, envContainer env.EnvContainer, worktreePath, branchName, archiveMessage string) error {
			recordStep("cleanup")
			return nil
		}).Once()

	s.env.OnActivity(s.ima.PutWorkflow, mock.Anything, mock.AnythingOfType("domain.Flow")).Return(nil)

	// fakeSubtask stands in for an in-flight sub-task child workflow: it runs
	// until canceled. It does not signal its own closure the way real sub-tasks
	// do because the SDK test environment ends a canceled child immediately,
	// without running any of its cleanup.
	fakeSubtask := func(ctx workflow.Context) error {
		return workflow.Await(ctx, func() bool { return false })
	}
	s.env.RegisterWorkflowWithOptions(fakeSubtask, workflow.RegisterOptions{Name: "fakeSubtask"})

	// miniIdd runs the production main loop, so sub-task closures and the merge
	// approval's finish are handled by the same selector IddWorkflow uses.
	miniIdd := func(ctx workflow.Context) error {
		ctx = utils.NoRetryCtx(ctx)
		dCtx := newIddMergeApprovalDevContext(ctx, iddBranch)
		input := iddMergeApprovalInput()
		state := &IddState{
			DefaultTargetBranch: targetBranch,
			Subtasks: []IddSubtask{{
				FlowId: subtaskFlowId,
				Title:  "In-flight sub-task",
				Status: "in_progress",
			}},
		}

		subtaskCtx := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{WorkflowID: subtaskFlowId})
		subtaskFuture := workflow.ExecuteChildWorkflow(subtaskCtx, "fakeSubtask")
		if err := subtaskFuture.GetChildWorkflowExecution().Get(ctx, nil); err != nil {
			return err
		}

		ma := startIddMergeApproval(dCtx, input, state)
		return runIddMainLoop(dCtx, input, state, ma, nil)
	}
	s.env.RegisterWorkflow(miniIdd)

	approved := true
	s.env.RegisterDelayedCallback(func() {
		s.signalMergeApprovalResponse(mu, approvalActions, flow_action.UserResponse{
			Approved: &approved,
			Params:   map[string]interface{}{"targetBranch": targetBranch},
		})
	}, time.Second)
	// real sub-tasks keep working once cancellation is requested, auto-merging
	// and cleaning up before reporting their closure to the IDD workflow
	s.env.RegisterDelayedCallback(func() {
		recordStep("subtask canceled")
		s.env.SignalWorkflow(SignalNameWorkflowClosed, WorkflowClosure{
			FlowId: subtaskFlowId,
			Reason: "canceled",
		})
	}, 3*time.Second)

	s.env.ExecuteWorkflow(miniIdd)
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	s.Equal(iddBranch, capturedMergeParams.SourceBranch)
	s.Equal(targetBranch, capturedMergeParams.TargetBranch)
	s.Equal(git.MergeStrategyMerge, capturedMergeParams.MergeStrategy)

	stepMu.Lock()
	s.Equal([]string{"merge", "subtask canceled", "merge", "cleanup"}, steps, "in-flight sub-tasks must only be stopped once the merge succeeded, then settle before a reconciliation merge carries what they landed and the worktree goes away")
	stepMu.Unlock()

	mu.Lock()
	defer mu.Unlock()
	s.Require().NotEmpty(*approvalActions)
	initial := (*approvalActions)[0]
	s.Equal(domain.ActionStatusPending, initial.ActionStatus)
	s.True(initial.IsHumanAction)
	s.Equal(string(flow_action.RequestKindMergeApproval), initial.ActionParams["requestKind"])
	initialInfo := mergeApprovalInfo(initial)
	s.Equal(iddBranch, initialInfo["sourceBranch"])
	s.Equal(targetBranch, initialInfo["defaultTargetBranch"])
	s.Equal("initial diff", initialInfo["diff"])

	final := (*approvalActions)[len(*approvalActions)-1]
	s.Equal(domain.ActionStatusComplete, final.ActionStatus)
	s.Equal("merged diff", mergeApprovalInfo(final)["diff"], "the completed request must keep the merged diff viewable after worktree cleanup")
}

// TestIddMergeApprovalKeepsRequestPendingOnRejection verifies that rejecting
// the request does not finish the flow: the request is re-opened so the user
// can keep working on intent and approve later.
func (s *IddWorkflowTestSuite) TestIddMergeApprovalKeepsRequestPendingOnRejection() {
	const iddBranch = "side/idd-worktree"

	mu, approvalActions := s.recordMergeApprovalActions()
	s.env.OnActivity(git.GitDiffActivity, mock.Anything, mock.Anything, mock.Anything).Return("some diff", nil)

	miniIdd := func(ctx workflow.Context) (bool, error) {
		ctx = utils.NoRetryCtx(ctx)
		dCtx := newIddMergeApprovalDevContext(ctx, iddBranch)
		state := &IddState{DefaultTargetBranch: "main"}
		ma := startIddMergeApproval(dCtx, iddMergeApprovalInput(), state)

		finished := false
		selector := workflow.NewSelector(ctx)
		selector.AddReceive(ma.finishedCh, func(c workflow.ReceiveChannel, _ bool) {
			c.Receive(ctx, nil)
			finished = true
		})
		selector.AddFuture(workflow.NewTimer(ctx, 5*time.Second), func(workflow.Future) {})
		selector.Select(ctx)
		return finished, nil
	}
	s.env.RegisterWorkflow(miniIdd)

	rejected := false
	s.env.RegisterDelayedCallback(func() {
		s.signalMergeApprovalResponse(mu, approvalActions, flow_action.UserResponse{
			Approved: &rejected,
			Content:  "not yet, the login flow is still missing",
		})
	}, time.Second)

	s.env.ExecuteWorkflow(miniIdd)
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var finished bool
	s.NoError(s.env.GetWorkflowResult(&finished))
	s.False(finished, "a rejection must not finish the flow")
	s.env.AssertActivityNumberOfCalls(s.T(), "GitMergeActivity", 0)

	mu.Lock()
	defer mu.Unlock()
	s.Require().Greater(len(*approvalActions), 1, "the request should have been re-opened")
	reopened := (*approvalActions)[len(*approvalActions)-1]
	s.Equal(domain.ActionStatusPending, reopened.ActionStatus)
	s.Empty(reopened.ActionResult, "the re-opened request must not look answered")
	s.NotContains(reopened.ActionParams, "mergeError")
}

// TestIddMergeApprovalRegeneratesDiffOnOptionChange verifies that changing the
// target branch or the ignore-whitespace option regenerates the diff and
// updates the still-pending request, matching BasicDev's merge approval.
func (s *IddWorkflowTestSuite) TestIddMergeApprovalRegeneratesDiffOnOptionChange() {
	const iddBranch = "side/idd-worktree"

	mu, approvalActions := s.recordMergeApprovalActions()

	var diffMu sync.Mutex
	var diffParams []git.GitDiffParams
	s.env.OnActivity(git.GitDiffActivity, mock.Anything, mock.Anything, mock.Anything).Return(
		func(ctx context.Context, envContainer env.EnvContainer, params git.GitDiffParams) (string, error) {
			diffMu.Lock()
			defer diffMu.Unlock()
			diffParams = append(diffParams, params)
			if params.IgnoreWhitespace {
				return "whitespace-ignored diff", nil
			}
			return "initial diff", nil
		})

	miniIdd := func(ctx workflow.Context) (string, error) {
		ctx = utils.NoRetryCtx(ctx)
		dCtx := newIddMergeApprovalDevContext(ctx, iddBranch)
		state := &IddState{DefaultTargetBranch: "main"}
		ma := startIddMergeApproval(dCtx, iddMergeApprovalInput(), state)
		if err := workflow.Await(ctx, func() bool { return ma.params.Diff == "whitespace-ignored diff" }); err != nil {
			return "", err
		}
		// let the request's update reach storage before the workflow ends
		if err := workflow.Sleep(ctx, time.Second); err != nil {
			return "", err
		}
		return ma.params.Diff, nil
	}
	s.env.RegisterWorkflow(miniIdd)

	s.env.RegisterDelayedCallback(func() {
		s.signalMergeApprovalResponse(mu, approvalActions, flow_action.UserResponse{
			Params: map[string]interface{}{"targetBranch": "release", "ignoreWhitespace": true},
		})
	}, time.Second)

	s.env.ExecuteWorkflow(miniIdd)
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var diff string
	s.NoError(s.env.GetWorkflowResult(&diff))
	s.Equal("whitespace-ignored diff", diff)

	diffMu.Lock()
	lastDiffParams := diffParams[len(diffParams)-1]
	diffMu.Unlock()
	s.Equal("release", lastDiffParams.BaseRef)
	s.True(lastDiffParams.IgnoreWhitespace)

	mu.Lock()
	defer mu.Unlock()
	s.Require().Greater(len(*approvalActions), 1, "the pending request should have been updated")
	updated := (*approvalActions)[len(*approvalActions)-1]
	s.Equal(domain.ActionStatusPending, updated.ActionStatus)
	updatedInfo := mergeApprovalInfo(updated)
	s.Equal("whitespace-ignored diff", updatedInfo["diff"])
	s.Equal("main", updatedInfo["defaultTargetBranch"], "the flow's start branch stays the default target regardless of the user's current selection")
}

// TestIddMergeApprovalRefreshesDiffOnRequest verifies the internal refresh
// trigger, which the workflow fires as work lands on the idd worktree branch,
// regenerates the diff and updates the still-pending request.
func (s *IddWorkflowTestSuite) TestIddMergeApprovalRefreshesDiffOnRequest() {
	const iddBranch = "side/idd-worktree"

	mu, approvalActions := s.recordMergeApprovalActions()

	var diffMu sync.Mutex
	diffCalls := 0
	s.env.OnActivity(git.GitDiffActivity, mock.Anything, mock.Anything, mock.Anything).Return(
		func(ctx context.Context, envContainer env.EnvContainer, params git.GitDiffParams) (string, error) {
			diffMu.Lock()
			defer diffMu.Unlock()
			// GetGitDiff issues a three-dot and a direct diff per call.
			diffCalls++
			if diffCalls <= 2 {
				return "initial diff", nil
			}
			return "refreshed diff", nil
		})

	miniIdd := func(ctx workflow.Context) (string, error) {
		ctx = utils.NoRetryCtx(ctx)
		dCtx := newIddMergeApprovalDevContext(ctx, iddBranch)
		state := &IddState{DefaultTargetBranch: "main"}
		ma := startIddMergeApproval(dCtx, iddMergeApprovalInput(), state)
		ma.requestDiffRefresh()
		if err := workflow.Await(ctx, func() bool { return ma.params.Diff == "refreshed diff" }); err != nil {
			return "", err
		}
		// let the request's update reach storage before the workflow ends
		if err := workflow.Sleep(ctx, time.Second); err != nil {
			return "", err
		}
		return ma.params.Diff, nil
	}
	s.env.RegisterWorkflow(miniIdd)

	s.env.ExecuteWorkflow(miniIdd)
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	mu.Lock()
	defer mu.Unlock()
	s.Require().Greater(len(*approvalActions), 1, "the pending request should have been updated")
	refreshed := (*approvalActions)[len(*approvalActions)-1]
	s.Equal(domain.ActionStatusPending, refreshed.ActionStatus)
	s.Equal("refreshed diff", mergeApprovalInfo(refreshed)["diff"])
}

// TestIntentSubtaskStartSignalIgnoredWhileFinishing verifies the outer half of
// the dispatch guard: a start signal arriving once the finish began is dropped,
// so no sub-task is even reserved for a branch that is about to be archived.
func (s *IddWorkflowTestSuite) TestIntentSubtaskStartSignalIgnoredWhileFinishing() {
	const iddBranch = "side/idd-worktree"

	childStarted := false
	s.env.RegisterWorkflowWithOptions(
		func(ctx workflow.Context, input BasicDevWorkflowInput) (string, error) {
			childStarted = true
			return "done", nil
		},
		workflow.RegisterOptions{Name: "BasicDevWorkflow"},
	)

	persistedFlows := 0
	s.env.OnActivity(s.ima.PutWorkflow, mock.Anything, mock.AnythingOfType("domain.Flow")).Return(
		func(ctx context.Context, flow domain.Flow) error {
			persistedFlows++
			return nil
		}).Maybe()

	state := &IddState{DefaultTargetBranch: "main", Finishing: true}
	wrapper := func(ctx workflow.Context) (IddState, error) {
		ctx = utils.NoRetryCtx(ctx)
		dCtx := newIddMergeApprovalDevContext(ctx, iddBranch)
		input := iddMergeApprovalInput()
		// the loop runs on its own coroutine, as it never returns on its own
		// while the finish it is waiting for is in progress
		ma := &iddMergeApproval{finishedCh: workflow.NewChannel(ctx)}
		workflow.Go(ctx, func(goCtx workflow.Context) {
			_ = runIddMainLoop(dCtx.WithContext(goCtx), input, state, ma, nil)
		})
		if err := workflow.Sleep(ctx, 5*time.Second); err != nil {
			return IddState{}, err
		}
		return *state, nil
	}
	s.env.RegisterWorkflow(wrapper)

	s.env.RegisterDelayedCallback(func() {
		s.env.SignalWorkflow(SignalNameStartIntentSubtask, StartIntentSubtaskSignal{})
	}, time.Second)

	s.env.ExecuteWorkflow(wrapper)
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var finalState IddState
	s.NoError(s.env.GetWorkflowResult(&finalState))
	s.Empty(finalState.Subtasks, "no sub-task may be reserved once the flow is finishing")
	s.Zero(persistedFlows, "a refused start must not leave a flow record behind")
	s.False(childStarted)
	s.Zero(finalState.InFlightSubtaskRunners, "a refused start must not leave the finish's drain waiting")
}

// TestIntentSubtaskDispatchRefusedWhileFinishing verifies the inner half of the
// guard, which covers dispatches that were already under way when the finish
// began — including an orchestrator turn that decided to dispatch while
// suspended in an LLM call: the runner abandons its reservation rather than
// starting a child on a branch that is about to be archived.
func (s *IddWorkflowTestSuite) TestIntentSubtaskDispatchRefusedWhileFinishing() {
	const iddBranch = "side/idd-worktree"

	childStarted := false
	s.env.RegisterWorkflowWithOptions(
		func(ctx workflow.Context, input BasicDevWorkflowInput) (string, error) {
			childStarted = true
			return "done", nil
		},
		workflow.RegisterOptions{Name: "BasicDevWorkflow"},
	)

	s.setupTitleGenerationMocks()
	s.env.OnActivity(s.ima.PutWorkflow, mock.Anything, mock.AnythingOfType("domain.Flow")).Return(nil)
	s.env.OnActivity(git.GitAddActivity, mock.Anything, mock.Anything).Return(nil).Maybe()
	// the finish starts while this runner is committing intent
	s.env.OnActivity(git.GitCommitActivity, mock.Anything, mock.Anything, mock.Anything).
		After(2*time.Second).Return("commit-sha", nil).Maybe()
	s.env.OnActivity(env.EnvRunCommandActivity, mock.Anything, mock.MatchedBy(func(in env.EnvRunCommandActivityInput) bool {
		return len(in.Args) > 0 && in.Args[0] == "rev-parse"
	})).Return(env.EnvRunCommandActivityOutput{Stdout: "deadbeef\n", ExitStatus: 0}, nil).Maybe()
	s.env.OnActivity(env.EnvRunCommandActivity, mock.Anything, mock.MatchedBy(func(in env.EnvRunCommandActivityInput) bool {
		return len(in.Args) > 0 && in.Args[0] == "show"
	})).Return(env.EnvRunCommandActivityOutput{Stdout: "diff body", ExitStatus: 0}, nil).Maybe()

	state := &IddState{DefaultTargetBranch: "main"}
	wrapper := func(ctx workflow.Context) (IddState, error) {
		ctx = utils.NoRetryCtx(ctx)
		dCtx := newIddMergeApprovalDevContext(ctx, iddBranch)
		input := iddMergeApprovalInput()

		state.beginSubtaskRunner()
		flowId := reservePendingSubtask(dCtx, input, state, "")
		workflow.Go(ctx, func(goCtx workflow.Context) {
			defer state.endSubtaskRunner()
			runIntentSubtask(dCtx.WithContext(goCtx), input, StartIntentSubtaskSignal{}, state, flowId, nil)
		})

		if err := workflow.Await(ctx, func() bool { return state.InFlightSubtaskRunners == 0 }); err != nil {
			return IddState{}, err
		}
		return *state, nil
	}
	s.env.RegisterWorkflow(wrapper)

	// the user approves the merge while the runner is mid-commit
	s.env.RegisterDelayedCallback(func() {
		state.Finishing = true
	}, time.Second)

	s.env.ExecuteWorkflow(wrapper)
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var finalState IddState
	s.NoError(s.env.GetWorkflowResult(&finalState))
	s.False(childStarted, "a runner must not start its child once the flow is finishing")
	s.Require().Len(finalState.Subtasks, 1)
	s.Equal("canceled", finalState.Subtasks[0].Status, "the abandoned reservation must reach a terminal status so the finish's drain completes")
}

// TestIddMergeApprovalReopensOnMergeFailure verifies a failed finish leaves the
// request pending again — the API records the approval as completing it — with
// the failure surfaced on its params so the canvas can show it prominently.
func (s *IddWorkflowTestSuite) TestIddMergeApprovalReopensOnMergeFailure() {
	const iddBranch = "side/idd-worktree"

	mu, approvalActions := s.recordMergeApprovalActions()

	s.env.OnActivity(git.GitDiffActivity, mock.Anything, mock.Anything, mock.Anything).Return("some diff", nil)
	s.mockIntentCommitActivities(1)
	s.mockTargetCommit("main", "main-tip-sha")
	s.env.OnActivity(git.GitMergeActivity, mock.Anything, mock.Anything, mock.Anything).
		Return(git.MergeActivityResult{}, errors.New("merge exploded")).Once()

	miniIdd := func(ctx workflow.Context) (string, error) {
		ctx = utils.NoRetryCtx(ctx)
		dCtx := newIddMergeApprovalDevContext(ctx, iddBranch)
		state := &IddState{DefaultTargetBranch: "main"}
		ma := startIddMergeApproval(dCtx, iddMergeApprovalInput(), state)
		if err := workflow.Await(ctx, func() bool { return ma.mergeError != "" }); err != nil {
			return "", err
		}
		// let the re-opened request reach storage before the workflow ends
		if err := workflow.Sleep(ctx, time.Second); err != nil {
			return "", err
		}
		return ma.mergeError, nil
	}
	s.env.RegisterWorkflow(miniIdd)

	approved := true
	s.env.RegisterDelayedCallback(func() {
		s.signalMergeApprovalResponse(mu, approvalActions, flow_action.UserResponse{
			Approved: &approved,
			Params:   map[string]interface{}{"targetBranch": "main"},
		})
	}, time.Second)

	s.env.ExecuteWorkflow(miniIdd)
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var mergeError string
	s.NoError(s.env.GetWorkflowResult(&mergeError))
	s.Contains(mergeError, "merge exploded")

	mu.Lock()
	defer mu.Unlock()
	reopened := (*approvalActions)[len(*approvalActions)-1]
	s.Equal(domain.ActionStatusPending, reopened.ActionStatus, "a failed finish must leave the request awaiting the user again")
	s.Empty(reopened.ActionResult)
	s.Contains(reopened.ActionParams["mergeError"], "merge exploded")
}

// TestRunOrchestratorTurn_EmptyDiffIsNoOp drives runIddOrchestratorTurn
// directly to verify the only true no-op path inside the orchestrator:
// when the pending intent diff is empty there is nothing to reason about,
// so the turn returns before making any LLM call. This matters because
// the canvas may trigger runs eagerly (on every idle tick, on the
// edit-watcher returning, etc.) and we don't want a spurious LLM call per
// trigger when the worktree is clean relative to the start branch. Note
// that this is independent of AutoMode: with AutoMode off and a
// non-empty diff the orchestrator does still call the LLM (nudge-only),
// since the user opted into ambiguity-surfacing nudges by leaving the
// orchestrator enabled at all.
func (s *IddWorkflowTestSuite) TestRunOrchestratorTurn_EmptyDiffIsNoOp() {
	const iddBranch = "side/idd-worktree"

	s.env.OnActivity(env.EnvRunCommandActivity, mock.Anything, mock.Anything).
		Return(env.EnvRunCommandActivityOutput{Stdout: "unchanged-tree\nincremental\noriginal-tree\n\nIDD_SNAPSHOT_COMPLETE\n"}, nil).Once()

	miniIdd := func(ctx workflow.Context) (IddState, error) {
		ctx = utils.NoRetryCtx(ctx)
		gs := &flow_action.GlobalState{}
		gs.InitValues()
		dCtx := DevContext{
			ExecContext: flow_action.ExecContext{
				WorkspaceId: "test-workspace",
				Context:     ctx,
				FlowScope:   &flow_action.FlowScope{SubflowName: "idd"},
				GlobalState: gs,
				EnvContainer: &env.EnvContainer{
					Env: &env.LocalEnv{WorkingDirectory: "/tmp/test-repo"},
				},
			},
			Worktree:   &domain.Worktree{Name: iddBranch},
			RepoConfig: common.RepoConfig{},
		}
		state := &IddState{DefaultTargetBranch: "main"}
		chatHistory := NewVersionedChatHistory(dCtx, dCtx.WorkspaceId)
		runIddOrchestratorTurn(dCtx, IddWorkflowInput{
			WorkspaceId: "test-workspace",
			RepoDir:     "/tmp/repo",
			Title:       "My Intent",
			IddOptions:  IddOptions{EnvType: env.EnvTypeLocal, RepoMode: env.RepoModeWorktree},
		}, state, chatHistory, false, nil)
		s.Equal(0, chatHistory.Len(), "no chat messages should be appended on empty-diff no-op")
		return *state, nil
	}
	s.env.RegisterWorkflow(miniIdd)

	s.env.ExecuteWorkflow(miniIdd)
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var state IddState
	s.NoError(s.env.GetWorkflowResult(&state))
	s.Empty(state.Subtasks)
	s.Empty(state.Nudges)
}

// TestPendingIntentDiffIncludesUntrackedFiles verifies the orchestrator's
// pending-intent diff covers both tracked modifications and brand-new
// (untracked) intent files, reusing the shared untracked-diff activity.
func (s *IddWorkflowTestSuite) TestPendingIntentDiffIncludesUntrackedFiles() {
	const iddBranch = "side/idd-worktree"

	s.env.OnActivity(env.EnvRunCommandActivity, mock.Anything, mock.MatchedBy(func(in env.EnvRunCommandActivityInput) bool {
		return in.Command == "git" && len(in.Args) >= 2 && in.Args[0] == "diff"
	})).Return(env.EnvRunCommandActivityOutput{Stdout: "tracked-intent-change"}, nil).Once()
	s.env.OnActivity(git.DiffUntrackedFilesActivity, mock.Anything, mock.Anything, mock.MatchedBy(func(paths []string) bool {
		return len(paths) == 1 && paths[0] == "intent"
	})).Return("untracked-intent-file", nil).Once()

	miniIdd := func(ctx workflow.Context) (string, error) {
		ctx = utils.NoRetryCtx(ctx)
		gs := &flow_action.GlobalState{}
		gs.InitValues()
		dCtx := DevContext{
			ExecContext: flow_action.ExecContext{
				WorkspaceId: "test-workspace",
				Context:     ctx,
				FlowScope:   &flow_action.FlowScope{SubflowName: "idd"},
				GlobalState: gs,
				EnvContainer: &env.EnvContainer{
					Env: &env.LocalEnv{WorkingDirectory: "/tmp/test-repo"},
				},
			},
			Worktree:   &domain.Worktree{Name: iddBranch},
			RepoConfig: common.RepoConfig{},
		}
		state := &IddState{DefaultTargetBranch: "main"}
		return pendingIntentDiff(dCtx, state)
	}
	s.env.RegisterWorkflow(miniIdd)

	s.env.ExecuteWorkflow(miniIdd)
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var diff string
	s.NoError(s.env.GetWorkflowResult(&diff))
	s.Contains(diff, "tracked-intent-change")
	s.Contains(diff, "untracked-intent-file")
}

// TestSetAutoModeSignalTogglesState verifies the auto-mode toggle signal
// updates IddState.AutoMode so that subsequent orchestrator runs gate on it,
// and that the new value is mirrored onto the IDD flow record's metadata,
// which is where the canvas reads it from.
func (s *IddWorkflowTestSuite) TestSetAutoModeSignalTogglesState() {
	s.env.OnActivity(s.ima.GetWorkflow, mock.Anything, "test-workspace", mock.Anything).Return(
		domain.Flow{
			WorkspaceId: "test-workspace",
			Id:          "flow_idd",
			Type:        domain.FlowTypeIdd,
			ParentId:    "task-1",
			Status:      "in_progress",
		}, nil).Once()

	s.env.OnActivity(
		s.ima.PutWorkflow,
		mock.Anything,
		mock.MatchedBy(func(flow domain.Flow) bool {
			nudges, _ := flow.Metadata[IddMetadataKeyNudges].([]any)
			return flow.Metadata[IddMetadataKeyAutoMode] == true &&
				flow.Type == domain.FlowTypeIdd && len(nudges) == 1
		}),
	).Return(nil).Once()

	miniIdd := func(ctx workflow.Context) (IddState, error) {
		ctx = utils.NoRetryCtx(ctx)
		dCtx := DevContext{
			ExecContext: flow_action.ExecContext{
				WorkspaceId: "test-workspace",
				Context:     ctx,
			},
		}
		input := IddWorkflowInput{WorkspaceId: "test-workspace", TaskId: "task-1"}
		state := &IddState{Nudges: []IddNudge{{Text: "reconsider the scope"}}}
		setAutoCh := workflow.GetSignalChannel(ctx, SignalNameSetIddAutoMode)
		var sig SetIddAutoModeSignal
		setAutoCh.Receive(ctx, &sig)
		state.AutoMode = sig.Enabled
		persistIddFlowMetadata(dCtx, input, state)
		return *state, nil
	}
	s.env.RegisterWorkflow(miniIdd)

	s.env.RegisterDelayedCallback(func() {
		s.env.SignalWorkflow(SignalNameSetIddAutoMode, SetIddAutoModeSignal{Enabled: true})
	}, 0)

	s.env.ExecuteWorkflow(miniIdd)
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var state IddState
	s.NoError(s.env.GetWorkflowResult(&state))
	s.True(state.AutoMode)
}

// recordPersistedFlows captures every flow record written via PutWorkflow, in
// the order the writes actually reached storage.
func (s *IddWorkflowTestSuite) recordPersistedFlows() (*sync.Mutex, *[]domain.Flow) {
	var mu sync.Mutex
	persisted := &[]domain.Flow{}
	s.env.OnActivity(
		s.ima.PutWorkflow,
		mock.Anything,
		mock.AnythingOfType("domain.Flow"),
	).Return(func(ctx context.Context, flow domain.Flow) error {
		mu.Lock()
		defer mu.Unlock()
		*persisted = append(*persisted, flow)
		return nil
	})
	return &mu, persisted
}

// TestReservePendingSubtaskPersistsPendingFlow verifies a sub-task shows up as
// a flow record as soon as it is reserved, before the coroutine that runs it
// has done any work — the canvas lists pending sub-tasks from those records.
func (s *IddWorkflowTestSuite) TestReservePendingSubtaskPersistsPendingFlow() {
	mu, persisted := s.recordPersistedFlows()

	miniIdd := func(ctx workflow.Context) (IddState, error) {
		ctx = utils.NoRetryCtx(ctx)
		dCtx := DevContext{
			ExecContext: flow_action.ExecContext{
				WorkspaceId: "test-workspace",
				Context:     ctx,
			},
		}
		input := IddWorkflowInput{WorkspaceId: "test-workspace", TaskId: "task-1"}
		state := &IddState{}
		reservePendingSubtask(dCtx, input, state, "implement the parser")
		return *state, nil
	}
	s.env.RegisterWorkflow(miniIdd)

	s.env.ExecuteWorkflow(miniIdd)
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var state IddState
	s.NoError(s.env.GetWorkflowResult(&state))
	s.Require().Len(state.Subtasks, 1)

	mu.Lock()
	defer mu.Unlock()
	s.Require().Len(*persisted, 1)
	pending := (*persisted)[0]
	s.Equal("pending", pending.Status)
	s.Equal(state.Subtasks[0].FlowId, pending.Id)
	s.Equal("task-1", pending.ParentId)
	s.Equal(domain.FlowTypeBasicDev, pending.Type)
	s.Equal("test-workspace", pending.WorkspaceId)
	s.False(pending.Created.IsZero())
}

// TestSubtaskStatusTransitionsPersistFlowRecords verifies intermediate
// sub-task status transitions (not just start and terminal ones) are written
// to the sub-task's flow record, in the order they happened, since the canvas
// groups and orders sub-tasks from those records.
func (s *IddWorkflowTestSuite) TestSubtaskStatusTransitionsPersistFlowRecords() {
	mu, persisted := s.recordPersistedFlows()

	miniIdd := func(ctx workflow.Context) (IddState, error) {
		ctx = utils.NoRetryCtx(ctx)
		dCtx := DevContext{
			ExecContext: flow_action.ExecContext{
				WorkspaceId: "test-workspace",
				Context:     ctx,
			},
		}
		input := IddWorkflowInput{WorkspaceId: "test-workspace", TaskId: "task-1"}
		state := &IddState{Subtasks: []IddSubtask{{
			FlowId: "flow_1",
			Title:  "Sub-task One",
			Status: "in_progress",
		}}}
		updateSubtaskStatus(dCtx, input, state, "flow_1", "blocked")
		updateSubtaskStatus(dCtx, input, state, "flow_1", "in_progress")
		updateSubtaskStatus(dCtx, input, state, "flow_1", "canceled")
		return *state, nil
	}
	s.env.RegisterWorkflow(miniIdd)

	s.env.ExecuteWorkflow(miniIdd)
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var state IddState
	s.NoError(s.env.GetWorkflowResult(&state))
	s.Require().Len(state.Subtasks, 1)
	s.Equal("canceled", state.Subtasks[0].Status)
	s.False(state.Subtasks[0].UpdatedAt.IsZero())

	mu.Lock()
	defer mu.Unlock()
	statuses := make([]string, 0, len(*persisted))
	for _, flow := range *persisted {
		s.Equal("flow_1", flow.Id)
		s.Equal("Sub-task One", flow.Title)
		s.Equal("task-1", flow.ParentId)
		statuses = append(statuses, flow.Status)
	}
	s.Equal([]string{"blocked", "in_progress", "canceled"}, statuses)
}

// TestNudgesPersistedToFlowMetadata verifies orchestrator nudges reach the IDD
// flow record's metadata, which is where the canvas reads them from.
func (s *IddWorkflowTestSuite) TestNudgesPersistedToFlowMetadata() {
	s.env.OnActivity(s.ima.GetWorkflow, mock.Anything, "test-workspace", mock.Anything).Return(
		domain.Flow{
			WorkspaceId: "test-workspace",
			Id:          "flow_idd",
			Type:        domain.FlowTypeIdd,
			ParentId:    "task-1",
			Status:      "in_progress",
		}, nil).Twice()
	mu, persisted := s.recordPersistedFlows()

	miniIdd := func(ctx workflow.Context) error {
		ctx = utils.NoRetryCtx(ctx)
		dCtx := DevContext{
			ExecContext: flow_action.ExecContext{
				WorkspaceId: "test-workspace",
				Context:     ctx,
			},
		}
		input := IddWorkflowInput{WorkspaceId: "test-workspace", TaskId: "task-1"}
		state := &IddState{}
		state.Nudges = append(state.Nudges, IddNudge{Text: "first nudge", AnchorText: "some intent"})
		persistIddFlowMetadata(dCtx, input, state)
		state.Nudges = append(state.Nudges, IddNudge{Text: "second nudge"})
		persistIddFlowMetadata(dCtx, input, state)
		return nil
	}
	s.env.RegisterWorkflow(miniIdd)

	s.env.ExecuteWorkflow(miniIdd)
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	mu.Lock()
	defer mu.Unlock()
	s.Require().Len(*persisted, 2)
	firstNudges, _ := (*persisted)[0].Metadata[IddMetadataKeyNudges].([]any)
	s.Require().Len(firstNudges, 1)
	firstNudge, _ := firstNudges[0].(map[string]any)
	s.Equal("first nudge", firstNudge["text"])
	s.Equal("some intent", firstNudge["anchorText"])
	secondNudges, _ := (*persisted)[1].Metadata[IddMetadataKeyNudges].([]any)
	s.Len(secondNudges, 2)
}

func TestIddWorkflowTestSuite(t *testing.T) {
	suite.Run(t, new(IddWorkflowTestSuite))
}

// TestSubtaskTerminalNotice verifies the wording and metadata included in the
// orchestrator-facing notice for each terminal sub-task status.
func TestSubtaskTerminalNotice(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		status      string
		childResult string
		childErr    error
		contains    []string
		notContains []string
	}{
		{
			name:        "completed includes result",
			status:      "completed",
			childResult: "merged 3 files",
			contains:    []string{`"Add login"`, "flow_1", "complete", "merged", "Result: merged 3 files"},
		},
		{
			name:        "completed without result omits result section",
			status:      "completed",
			contains:    []string{"complete"},
			notContains: []string{"Result:"},
		},
		{
			name:     "failed includes error",
			status:   "failed",
			childErr: fmt.Errorf("boom"),
			contains: []string{"failed", "NOT implemented", "Error: boom"},
		},
		{
			name:     "canceled",
			status:   "canceled",
			contains: []string{"canceled", "NOT implemented"},
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			notice := subtaskTerminalNotice("Add login", "flow_1", tt.status, tt.childResult, tt.childErr)
			for _, want := range tt.contains {
				assert.Contains(t, notice, want)
			}
			for _, notWant := range tt.notContains {
				assert.NotContains(t, notice, notWant)
			}
		})
	}
}
func (s *IddWorkflowTestSuite) TestWorkflowGoContextSupportsCancellation() {
	testWorkflow := func(ctx workflow.Context) error {
		gs := &flow_action.GlobalState{}
		gs.InitValues()
		dCtx := DevContext{
			ExecContext: flow_action.ExecContext{
				Context:     ctx,
				GlobalState: gs,
			},
		}

		done := workflow.NewChannel(ctx)
		workflow.Go(dCtx.Context, func(goCtx workflow.Context) {
			cancelCtx, cancel := workflow.WithCancel(goCtx)
			cancel()
			done.Send(goCtx, cancelCtx.Err())
		})

		var err error
		done.Receive(ctx, &err)
		return err
	}
	s.env.RegisterWorkflow(testWorkflow)

	s.env.ExecuteWorkflow(testWorkflow)

	s.True(s.env.IsWorkflowCompleted())
	s.ErrorIs(s.env.GetWorkflowError(), workflow.ErrCanceled)
}
func (s *IddWorkflowTestSuite) TestRunIntentSubtaskPropagatesSelectedOptionsToPlannedChild() {
	const iddBranch = "side/idd-worktree"

	maxIterations := 9
	mission := "plan the intent"
	requestedStartBranch := "main"
	selectedOptions := IddOptions{
		EnvType:           env.EnvTypeModal,
		RepoMode:          env.RepoModeInPlace,
		StartBranch:       &requestedStartBranch,
		ContextGatherType: ContextGatherTypeExplore,
		ConfigOverrides: common.ConfigOverrides{
			MaxIterations: &maxIterations,
			Mission:       &mission,
		},
	}

	var capturedInput PlannedDevInput
	s.env.RegisterWorkflowWithOptions(
		func(ctx workflow.Context, input PlannedDevInput) (DevPlanExecution, error) {
			capturedInput = input
			return DevPlanExecution{}, nil
		},
		workflow.RegisterOptions{Name: "PlannedDevWorkflow"},
	)

	s.env.OnActivity(git.GitAddActivity, mock.Anything, mock.MatchedBy(func(input git.GitAddActivityInput) bool {
		return input.Path == "."
	})).Return(nil).Once()

	s.env.OnActivity(git.GitCommitActivity, mock.Anything, mock.Anything, mock.MatchedBy(func(params git.GitCommitParams) bool {
		return params.CommitAll && params.IgnoreNothingToCommit
	})).Return("commit-sha", nil).Once()

	s.env.OnActivity(env.EnvRunCommandActivity, mock.Anything, mock.MatchedBy(func(in env.EnvRunCommandActivityInput) bool {
		return len(in.Args) > 0 && in.Args[0] == "rev-parse"
	})).Return(env.EnvRunCommandActivityOutput{Stdout: "abc123\n", ExitStatus: 0}, nil).Once()

	s.env.OnActivity(env.EnvRunCommandActivity, mock.Anything, mock.MatchedBy(func(in env.EnvRunCommandActivityInput) bool {
		return len(in.Args) > 0 && in.Args[0] == "show"
	})).Return(env.EnvRunCommandActivityOutput{Stdout: "diff body", ExitStatus: 0}, nil).Twice()

	s.env.OnActivity(
		s.ima.PutWorkflow,
		mock.Anything,
		mock.AnythingOfType("domain.Flow"),
	).Return(nil)

	s.setupTitleGenerationMocks()

	wrapper := func(ctx workflow.Context) (IddState, error) {
		ctx = utils.NoRetryCtx(ctx)
		gs := &flow_action.GlobalState{}
		gs.InitValues()
		dCtx := DevContext{
			ExecContext: flow_action.ExecContext{
				WorkspaceId: "test-workspace",
				Context:     ctx,
				FlowScope:   &flow_action.FlowScope{SubflowName: "idd"},
				GlobalState: gs,
				Secrets:     &secret_manager.SecretManagerContainer{SecretManager: &secret_manager.EnvSecretManager{}},
				EnvContainer: &env.EnvContainer{
					Env: &env.LocalEnv{WorkingDirectory: "/tmp/test-repo"},
				},
			},
			Worktree:   &domain.Worktree{Name: iddBranch},
			RepoConfig: common.RepoConfig{},
		}
		dCtx.SetLLMConfig(common.LLMConfig{
			Defaults: []common.ModelConfig{{Provider: "openai"}},
		})
		state := &IddState{}
		iddInput := IddWorkflowInput{
			WorkspaceId: "test-workspace",
			RepoDir:     "/tmp/repo",
			TaskId:      "task-1",
			Title:       "My Intent",
			IddOptions:  selectedOptions,
		}
		flowId := reservePendingSubtask(dCtx, iddInput, state, "")
		runIntentSubtask(dCtx, iddInput, StartIntentSubtaskSignal{Planned: true}, state, flowId, nil)
		if len(state.Subtasks) != 1 {
			return IddState{}, fmt.Errorf("expected 1 subtask, got %d", len(state.Subtasks))
		}
		return *state, nil
	}
	s.env.RegisterWorkflow(wrapper)

	s.env.ExecuteWorkflow(wrapper)
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	s.False(capturedInput.DetermineRequirements)
	s.True(capturedInput.AutoMerge)
	s.True(capturedInput.Idd)
	s.Equal(env.EnvTypeModal, capturedInput.EnvType)
	s.Equal(env.RepoModeInPlace, capturedInput.RepoMode)
	s.Equal(ContextGatherTypeExplore, capturedInput.ContextGatherType)
	s.Require().NotNil(capturedInput.ConfigOverrides.MaxIterations)
	s.Equal(maxIterations, *capturedInput.ConfigOverrides.MaxIterations)
	s.Require().NotNil(capturedInput.ConfigOverrides.Mission)
	s.Equal(mission, *capturedInput.ConfigOverrides.Mission)
	s.Equal("test-workspace", capturedInput.WorkspaceId)
	s.Equal("/tmp/repo", capturedInput.RepoDir)
	s.Require().NotNil(capturedInput.StartBranch)
	s.Equal(iddBranch, *capturedInput.StartBranch,
		"the child starts from the current IDD branch, not the branch requested for the IDD parent")
	s.NotEqual(requestedStartBranch, *capturedInput.StartBranch)
	s.Equal(1, s.titleGenerationCalls, "a sub-task should generate its title exactly once")
}

// TestIddParentSetupOptions verifies new IDD workflows always provision their
// top-level authoring worktree as a server-local git worktree, regardless of
// the execution environment selected for children, while histories recorded
// before the local-parent change keep provisioning the selected environment so
// their replays stay deterministic.
func (s *IddWorkflowTestSuite) TestIddParentSetupOptions() {
	type parentSetup struct {
		EnvType  string
		RepoMode string
	}
	wrapper := func(ctx workflow.Context, options IddOptions) (parentSetup, error) {
		envType, repoMode := iddParentSetupOptions(ctx, options)
		return parentSetup{EnvType: envType, RepoMode: repoMode}, nil
	}
	remoteSelection := IddOptions{EnvType: env.EnvTypeModal, RepoMode: env.RepoModeInPlace}

	tests := []struct {
		name     string
		options  IddOptions
		legacy   bool
		expected parentSetup
	}{
		{
			name:     "remote selection still sets up a local parent",
			options:  remoteSelection,
			expected: parentSetup{EnvType: "local", RepoMode: "worktree"},
		},
		{
			name:     "unset selection sets up a local parent explicitly",
			expected: parentSetup{EnvType: "local", RepoMode: "worktree"},
		},
		{
			name:     "pre-existing history keeps its selected parent environment",
			options:  remoteSelection,
			legacy:   true,
			expected: parentSetup{EnvType: "modal", RepoMode: "in_place"},
		},
	}
	for _, tt := range tests {
		tt := tt
		s.Run(tt.name, func() {
			testEnv := s.NewTestWorkflowEnvironment()
			testEnv.SetWorkerOptions(utils.TestWorkerOptions())
			testEnv.RegisterWorkflow(wrapper)
			if tt.legacy {
				testEnv.OnGetVersion("idd-local-parent", workflow.DefaultVersion, 1).
					Return(workflow.DefaultVersion)
			}

			testEnv.ExecuteWorkflow(wrapper, tt.options)
			s.True(testEnv.IsWorkflowCompleted())
			s.NoError(testEnv.GetWorkflowError())

			var got parentSetup
			s.NoError(testEnv.GetWorkflowResult(&got))
			s.Equal(tt.expected, got)
		})
	}
}

// setupLocalParentMocks stubs the activities SetupDevContext needs to build a
// server-local IDD worktree, plus the incidental activities IddWorkflow runs
// once setup succeeds. Remote-environment activities are deliberately left to
// the caller so tests can assert whether they were reached.
func (s *IddWorkflowTestSuite) setupLocalParentMocks() {
	s.env.OnActivity(common.GetLocalConfig).Return(common.LocalPublicConfig{
		LLM: common.LLMConfig{Defaults: []common.ModelConfig{{Provider: "openai"}}},
	}, nil).Maybe()

	var wa *workspace.Activities
	s.env.OnActivity(wa.GetWorkspaceConfig, mock.Anything).Return(domain.WorkspaceConfig{}, nil).Maybe()
	s.env.OnActivity(wa.GetWorkspace, mock.Anything).Return(domain.Workspace{ConfigMode: "local"}, nil).Maybe()

	s.env.OnActivity(GetRepoConfigActivityV2, mock.Anything).Return(GetRepoConfigActivityResult{}, nil).Maybe()
	s.env.OnActivity(GetRepoConfigActivity, mock.Anything).Return(common.RepoConfig{}, nil).Maybe()

	s.env.OnActivity(env.EnvRunCommandActivity, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			input := args.Get(1).(env.EnvRunCommandActivityInput)
			if len(input.Args) == 3 && input.Args[0] == "sh" && input.Args[1] == "-c" && input.Args[2] == env.EnsureSideTmpScript {
				s.sideTmpSetupCalls = append(s.sideTmpSetupCalls, input)
			}
		}).
		Return(env.EnvRunCommandActivityOutput{ExitStatus: 0}, nil).Maybe()
	s.env.OnActivity(common.BaseCommandPermissionsActivity, mock.Anything, mock.Anything).
		Return(common.CommandPermissionConfig{}, nil).Maybe()
	s.env.OnActivity(git.GetGitUserConfigActivity, mock.Anything, mock.Anything).
		Return(git.GitUserConfig{}, nil).Maybe()
	s.env.OnActivity(git.CleanupWorktreeActivity, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil).Maybe()

	s.setupTitleGenerationMocks()
}

// remoteActivityRecorder mocks every activity that would provision, sync with,
// or otherwise reach into a remote sandbox, counting invocations so tests can
// assert a local IDD parent never wakes the selected remote environment.
type remoteActivityRecorder struct {
	calls map[string]int
}

func (s *IddWorkflowTestSuite) recordRemoteActivities() *remoteActivityRecorder {
	rec := &remoteActivityRecorder{calls: map[string]int{}}
	record := func(name string) func(mock.Arguments) {
		return func(mock.Arguments) { rec.calls[name]++ }
	}
	s.env.OnActivity(env.CheckSandboxActivity, mock.Anything, mock.Anything).
		Run(record("CheckSandboxActivity")).Return(env.CheckSandboxOutput{}, nil).Maybe()
	s.env.OnActivity(env.CreateSandboxActivity, mock.Anything, mock.Anything).
		Run(record("CreateSandboxActivity")).Return(env.CreateSandboxOutput{}, nil).Maybe()
	s.env.OnActivity(env.SyncRepoToRemoteActivity, mock.Anything, mock.Anything).
		Run(record("SyncRepoToRemoteActivity")).Return(env.SyncRepoToRemoteOutput{}, nil).Maybe()
	s.env.OnActivity(env.CreateRemoteWorktreeActivity, mock.Anything, mock.Anything).
		Run(record("CreateRemoteWorktreeActivity")).Return(env.CreateRemoteWorktreeOutput{}, nil).Maybe()
	s.env.OnActivity(env.DeepenRepoActivity, mock.Anything, mock.Anything).
		Run(record("DeepenRepoActivity")).Return(env.DeepenRepoOutput{}, nil).Maybe()
	s.env.OnActivity(env.DevPodUpActivity, mock.Anything, mock.Anything).
		Run(record("DevPodUpActivity")).Return(nil).Maybe()
	return rec
}

// TestIddWorkflowSetsUpLocalParentForRemoteSelection runs IddWorkflow with
// Modal selected as the execution environment and asserts the top-level flow is
// entirely server-local: the worktree is created via the local git worktree
// activity off the requested start branch, its host path is persisted and
// watched for direct intent edits, the approval records the default target,
// and no Modal activity is ever invoked.
func (s *IddWorkflowTestSuite) TestIddWorkflowSetsUpLocalParentForRemoteSelection() {
	repoDir := s.T().TempDir()
	worktreeDir := s.T().TempDir()
	startBranch := "main"

	mu, approvalActions := s.recordMergeApprovalActions()
	s.setupLocalParentMocks()
	s.env.OnGetVersion("ensure-side-tmp-dir", workflow.DefaultVersion, 2).Return(workflow.Version(2))
	remote := s.recordRemoteActivities()

	var localParams env.LocalEnvParams
	s.env.OnActivity(env.NewLocalGitWorktreeActivity, mock.Anything, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			localParams = args.Get(1).(env.LocalEnvParams)
		}).
		Return(env.EnvContainer{Env: &env.LocalEnv{WorkingDirectory: worktreeDir}}, nil).Once()

	var persistedWorktree domain.Worktree
	var srvActivities srv.Activities
	s.env.OnActivity(srvActivities.PersistWorktree, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			persistedWorktree = args.Get(1).(domain.Worktree)
		}).Return(nil).Maybe()

	var watchInput IddWatchEditIdleInput
	// Failing the watcher parks its loop in the retry sleep, so the test can
	// inspect the input it was launched with without driving orchestrator turns.
	s.env.OnActivity(IddWatchEditIdleActivity, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			watchInput = args.Get(1).(IddWatchEditIdleInput)
		}).Return(IddWatchEditIdleResult{}, errors.New("watcher stopped for test")).Maybe()

	s.env.OnActivity(s.ima.GetWorkflow, mock.Anything, mock.Anything, mock.Anything).
		Return(domain.Flow{Id: "flow_idd", Type: domain.FlowTypeIdd}, nil).Maybe()
	s.env.OnActivity(s.ima.PutWorkflow, mock.Anything, mock.Anything).Return(nil).Maybe()

	// The delay is in skipped workflow time, and is long enough that parent
	// setup, including its retrying LLM branch-name calls, has finished.
	s.env.RegisterDelayedCallback(func() {
		s.env.CancelWorkflow()
	}, 2*time.Minute)

	s.env.ExecuteWorkflow(IddWorkflow, IddWorkflowInput{
		WorkspaceId: "test-workspace",
		RepoDir:     repoDir,
		TaskId:      "task_1",
		Title:       "Remote intent",
		IddOptions: IddOptions{
			EnvType:           env.EnvTypeModal,
			RepoMode:          env.RepoModeInPlace,
			StartBranch:       &startBranch,
			ContextGatherType: ContextGatherTypeExplore,
		},
	})

	s.True(s.env.IsWorkflowCompleted())

	s.Equal(repoDir, localParams.RepoDir)
	s.Require().NotNil(localParams.StartBranch)
	s.Equal(startBranch, *localParams.StartBranch)
	s.Equal(worktreeDir, persistedWorktree.WorkingDirectory)

	mu.Lock()
	defer mu.Unlock()
	s.Require().NotEmpty(*approvalActions)
	s.Equal(startBranch, mergeApprovalInfo((*approvalActions)[0])["defaultTargetBranch"])

	s.Equal(worktreeDir, watchInput.WorktreeDir)
	s.Equal("intent", watchInput.WatchSubdir)

	s.Empty(s.sideTmpSetupCalls, "worktree provisioning must avoid redundant scratch-directory setup")
	s.Empty(remote.calls, "a local IDD parent must not reach the selected remote environment")
}

// TestIddWorkflowLegacyHistoryUsesSelectedParentEnv verifies histories recorded
// before the local-parent change still provision the selected environment for
// the top-level worktree, so their replays keep the original activity sequence.
func (s *IddWorkflowTestSuite) TestIddWorkflowLegacyHistoryUsesSelectedParentEnv() {
	repoDir := s.T().TempDir()

	s.setupLocalParentMocks()
	s.env.OnGetVersion("idd-local-parent", workflow.DefaultVersion, 1).Return(workflow.DefaultVersion)

	localWorktreeCalls := 0
	s.env.OnActivity(env.NewLocalGitWorktreeActivity, mock.Anything, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { localWorktreeCalls++ }).
		Return(env.EnvContainer{Env: &env.LocalEnv{WorkingDirectory: repoDir}}, nil).Maybe()

	var sandboxInput env.CreateSandboxInput
	// The sandbox creation failing keeps the legacy path short: reaching it at
	// all is what distinguishes it from the forced-local parent.
	s.env.OnActivity(env.CreateSandboxActivity, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			sandboxInput = args.Get(1).(env.CreateSandboxInput)
		}).Return(env.CreateSandboxOutput{}, errors.New("modal unavailable in test")).Maybe()

	s.env.ExecuteWorkflow(IddWorkflow, IddWorkflowInput{
		WorkspaceId: "test-workspace",
		RepoDir:     repoDir,
		TaskId:      "task_1",
		Title:       "Remote intent",
		IddOptions: IddOptions{
			EnvType:  env.EnvTypeModal,
			RepoMode: env.RepoModeInPlace,
		},
	})

	s.True(s.env.IsWorkflowCompleted())
	s.Error(s.env.GetWorkflowError())
	s.Equal(env.EnvTypeModal, sandboxInput.EnvType)
	s.Equal(0, localWorktreeCalls, "legacy histories must not switch to a local parent worktree")
}

// commitIntentWorkflow drives commitIntent directly with a ready DevContext so
// commit failure handling can be exercised without the full IDD signal loop.
func (s *IddWorkflowTestSuite) commitIntentWorkflow(disableHumanInTheLoop bool) func(ctx workflow.Context) (IntentRequirementsInfo, error) {
	return func(ctx workflow.Context) (IntentRequirementsInfo, error) {
		ctx = utils.NoRetryCtx(ctx)
		gs := &flow_action.GlobalState{}
		gs.InitValues()
		dCtx := DevContext{
			ExecContext: flow_action.ExecContext{
				WorkspaceId: "test-workspace",
				Context:     ctx,
				FlowScope:   &flow_action.FlowScope{SubflowName: "idd"},
				GlobalState: gs,
				Secrets:     &secret_manager.SecretManagerContainer{SecretManager: &secret_manager.EnvSecretManager{}},
				EnvContainer: &env.EnvContainer{
					Env: &env.LocalEnv{WorkingDirectory: "/tmp/test-repo"},
				},
				DisableHumanInTheLoop: disableHumanInTheLoop,
			},
			Worktree:   &domain.Worktree{Name: "side/idd-worktree"},
			RepoConfig: common.RepoConfig{},
		}
		return commitIntent(dCtx, "My Intent", false)
	}
}

// setupCommitIntentUserRetryMocks stubs the persistence activities used when a
// failed activity prompts the user to retry.
func (s *IddWorkflowTestSuite) setupCommitIntentUserRetryMocks() {
	var fa *flow_action.FlowActivities
	s.env.OnActivity(fa.PersistFlowAction, mock.Anything, mock.Anything).Return(nil).Maybe()

	var srvActivities srv.Activities
	s.env.OnActivity(srvActivities.GetFlow, mock.Anything, mock.Anything, mock.Anything).Return(domain.Flow{}, nil).Maybe()
	s.env.OnActivity(srvActivities.PersistFlow, mock.Anything, mock.Anything).Return(nil).Maybe()
}

// TestCommitIntentRetriesCommitAfterUserContinue verifies a failing intent
// commit (e.g. a failed flow-branch backup sync) is surfaced through the
// user-controlled retry mechanism and re-executed when the user continues,
// rather than aborting the sub-task outright.
func (s *IddWorkflowTestSuite) TestCommitIntentRetriesCommitAfterUserContinue() {
	s.setupCommitIntentUserRetryMocks()

	s.env.OnActivity(git.GitAddActivity, mock.Anything, mock.Anything).Return(nil).Once()

	commitCalls := 0
	s.env.OnActivity(git.GitCommitActivity, mock.Anything, mock.Anything, mock.Anything).Return(
		func(ctx context.Context, envContainer env.EnvContainer, params git.GitCommitParams) (string, error) {
			commitCalls++
			if commitCalls == 1 {
				return "", errors.New("commit succeeded but failed to sync flow branch to local repo")
			}
			return "commit-sha", nil
		},
	)

	s.env.OnActivity(env.EnvRunCommandActivity, mock.Anything, mock.MatchedBy(func(in env.EnvRunCommandActivityInput) bool {
		return len(in.Args) > 0 && in.Args[0] == "rev-parse"
	})).Return(env.EnvRunCommandActivityOutput{Stdout: "abc123\n", ExitStatus: 0}, nil).Once()

	s.env.OnActivity(env.EnvRunCommandActivity, mock.Anything, mock.MatchedBy(func(in env.EnvRunCommandActivityInput) bool {
		return len(in.Args) > 0 && in.Args[0] == "show"
	})).Return(env.EnvRunCommandActivityOutput{Stdout: "diff body", ExitStatus: 0}, nil).Twice()

	child := s.commitIntentWorkflow(false)
	s.env.RegisterWorkflowWithOptions(child, workflow.RegisterOptions{Name: "commitIntentChild"})

	parent := func(ctx workflow.Context) (IntentRequirementsInfo, error) {
		signalCh := workflow.GetSignalChannel(ctx, flow_action.SignalNameRequestForUser)
		workflow.Go(ctx, func(ctx workflow.Context) {
			var req flow_action.RequestForUser
			signalCh.Receive(ctx, &req)
			workflow.SignalExternalWorkflow(ctx, req.OriginWorkflowId, "", flow_action.UserResponseSignalName(req.FlowActionId), flow_action.UserResponse{
				FlowActionId: req.FlowActionId,
			}).Get(ctx, nil)
		})

		childCtx := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
			WorkflowID: "commit-intent-child",
		})
		var info IntentRequirementsInfo
		err := workflow.ExecuteChildWorkflow(childCtx, "commitIntentChild").Get(ctx, &info)
		return info, err
	}
	s.env.RegisterWorkflow(parent)

	s.env.ExecuteWorkflow(parent)
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var info IntentRequirementsInfo
	s.NoError(s.env.GetWorkflowResult(&info))
	s.Equal("abc123", info.Commit)
	s.Equal(2, commitCalls, "the commit activity should be retried after the user continues")
}

// TestCommitIntentSurfacesCommitFailureWithoutHumanInTheLoop verifies commit
// failures are never swallowed: with human-in-the-loop disabled the error
// propagates instead of prompting for retry.
func (s *IddWorkflowTestSuite) TestCommitIntentSurfacesCommitFailureWithoutHumanInTheLoop() {
	s.setupCommitIntentUserRetryMocks()

	s.env.OnActivity(git.GitAddActivity, mock.Anything, mock.Anything).Return(nil).Once()
	s.env.OnActivity(git.GitCommitActivity, mock.Anything, mock.Anything, mock.Anything).Return(
		"", errors.New("commit succeeded but failed to sync flow branch to local repo"),
	).Once()

	wrapper := s.commitIntentWorkflow(true)
	s.env.RegisterWorkflow(wrapper)

	s.env.ExecuteWorkflow(wrapper)
	s.True(s.env.IsWorkflowCompleted())
	s.Require().Error(s.env.GetWorkflowError())
	s.Contains(s.env.GetWorkflowError().Error(), "failed to sync flow branch to local repo")
}

// TestCancelPendingSubtasksWaitsForRunnersPastClosure pins the barrier the
// finish's drain relies on. A sub-task reports its closure before running its
// own cleanup, so terminal statuses alone do not mean its work has landed on
// the idd branch; only the runner supervising it returning proves that.
func (s *IddWorkflowTestSuite) TestCancelPendingSubtasksWaitsForRunnersPastClosure() {
	type drainObservation struct {
		DrainedWhileRunnerInFlight bool
		DrainedAfterRunnerSettled  bool
	}

	// the sub-task already reported itself canceled, while its runner winds down
	state := &IddState{Subtasks: []IddSubtask{{FlowId: "flow_1", Status: "canceled"}}}
	state.beginSubtaskRunner()

	wrapper := func(ctx workflow.Context) (drainObservation, error) {
		ctx = utils.NoRetryCtx(ctx)
		dCtx := newIddMergeApprovalDevContext(ctx, "side/idd-worktree")

		drained := false
		workflow.Go(ctx, func(goCtx workflow.Context) {
			if err := cancelPendingSubtasks(dCtx.WithContext(goCtx), state); err != nil {
				return
			}
			drained = true
		})

		if err := workflow.Sleep(ctx, time.Second); err != nil {
			return drainObservation{}, err
		}
		obs := drainObservation{DrainedWhileRunnerInFlight: drained}

		state.endSubtaskRunner()
		if err := workflow.Sleep(ctx, time.Second); err != nil {
			return drainObservation{}, err
		}
		obs.DrainedAfterRunnerSettled = drained
		return obs, nil
	}
	s.env.RegisterWorkflow(wrapper)

	s.env.ExecuteWorkflow(wrapper)
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var obs drainObservation
	s.NoError(s.env.GetWorkflowResult(&obs))
	s.False(obs.DrainedWhileRunnerInFlight, "the drain must keep waiting while a sub-task runner is still winding down, however the sub-task reported itself")
	s.True(obs.DrainedAfterRunnerSettled, "the drain must release once every sub-task runner has settled")
}

// iddLateWorkFinish reports what a finish did when a sub-task landed work on
// the idd branch after the finish had already taken the diff it merged.
type iddLateWorkFinish struct {
	MergeCalls int
	FinalDiff  string
}

// runIddFinishWithLateSubtaskWork finishes an IDD flow with one sub-task in
// flight, which auto-merges its work into the idd branch and only then settles.
// That happens either while the first merge is still running or afterwards,
// while the finish waits for sub-tasks to settle — the two sides of the window
// in which a sub-task can still change the branch being merged.
func (s *IddWorkflowTestSuite) runIddFinishWithLateSubtaskWork(settleDuringMerge bool) iddLateWorkFinish {
	const iddBranch = "side/idd-worktree"
	const targetBranch = "main"
	const mergeBase = "main-tip-sha"

	mu, approvalActions := s.recordMergeApprovalActions()

	var repoMu sync.Mutex
	rounds := []string{"intent diff"}
	state := &IddState{DefaultTargetBranch: targetBranch}
	landSubtaskWork := func() {
		repoMu.Lock()
		rounds = append(rounds, "sub-task diff")
		repoMu.Unlock()
		state.endSubtaskRunner()
	}

	s.env.OnActivity(git.GitDiffActivity, mock.Anything, mock.Anything, mock.Anything).Return(
		func(ctx context.Context, envContainer env.EnvContainer, params git.GitDiffParams) (string, error) {
			repoMu.Lock()
			defer repoMu.Unlock()
			if params.BaseRef != mergeBase {
				return "initial diff", nil
			}
			return strings.Join(rounds, "\n"), nil
		})

	s.mockIntentCommitActivities(1)
	s.mockTargetCommit(targetBranch, mergeBase)

	mergeCalls := 0
	s.env.OnActivity(git.GitMergeActivity, mock.Anything, mock.Anything, mock.Anything).Return(
		func(ctx context.Context, envContainer env.EnvContainer, params git.GitMergeParams) (git.MergeActivityResult, error) {
			repoMu.Lock()
			first := mergeCalls == 0
			mergeCalls++
			repoMu.Unlock()
			// The workflow is blocked on this activity, which is the only
			// moment at which a sub-task can settle between the diff this merge
			// carries and the finish checking whether anything was in flight.
			if first && settleDuringMerge {
				landSubtaskWork()
			}
			return git.MergeActivityResult{HasConflicts: false}, nil
		})

	s.env.OnActivity(git.CleanupWorktreeActivity, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()
	s.env.OnActivity(s.ima.PutWorkflow, mock.Anything, mock.AnythingOfType("domain.Flow")).Return(nil).Maybe()

	miniIdd := func(ctx workflow.Context) error {
		ctx = utils.NoRetryCtx(ctx)
		dCtx := newIddMergeApprovalDevContext(ctx, iddBranch)
		input := iddMergeApprovalInput()
		// a sub-task is running when the user approves the merge
		state.beginSubtaskRunner()
		ma := startIddMergeApproval(dCtx, input, state)
		return runIddMainLoop(dCtx, input, state, ma, nil)
	}
	s.env.RegisterWorkflow(miniIdd)

	approved := true
	s.env.RegisterDelayedCallback(func() {
		s.signalMergeApprovalResponse(mu, approvalActions, flow_action.UserResponse{Approved: &approved})
	}, time.Second)
	if !settleDuringMerge {
		s.env.RegisterDelayedCallback(landSubtaskWork, 3*time.Second)
	}

	s.env.ExecuteWorkflow(miniIdd)
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	mu.Lock()
	defer mu.Unlock()
	final := (*approvalActions)[len(*approvalActions)-1]
	s.Equal(domain.ActionStatusComplete, final.ActionStatus)
	finalDiff, _ := mergeApprovalInfo(final)["diff"].(string)

	repoMu.Lock()
	defer repoMu.Unlock()
	return iddLateWorkFinish{MergeCalls: mergeCalls, FinalDiff: finalDiff}
}

// TestIddMergeApprovalMergesWorkLandedWhileSubtasksSettle verifies the finish
// picks up work that reaches the idd branch after it merged: a sub-task
// auto-merges as it winds down, which is after the finish took the diff it
// merged. That work must reach the target and the completed request rather than
// being archived with the worktree.
func (s *IddWorkflowTestSuite) TestIddMergeApprovalMergesWorkLandedWhileSubtasksSettle() {
	result := s.runIddFinishWithLateSubtaskWork(false)
	s.Equal(2, result.MergeCalls, "work landing while sub-tasks settle must be merged after the drain")
	s.Contains(result.FinalDiff, "sub-task diff", "the completed request must show the work that landed while sub-tasks settled")
}

// TestIddMergeApprovalMergesWorkLandedDuringTheMerge covers the narrow end of
// that window: the sub-task lands its work and settles while the first merge is
// still running, so nothing looks in flight by the time that merge returns.
// Whether to merge again must therefore be decided when the finish begins.
func (s *IddWorkflowTestSuite) TestIddMergeApprovalMergesWorkLandedDuringTheMerge() {
	result := s.runIddFinishWithLateSubtaskWork(true)
	s.Equal(2, result.MergeCalls, "a sub-task that settled during the merge must still be reconciled afterwards")
	s.Contains(result.FinalDiff, "sub-task diff", "the completed request must show the work that landed during the merge")
}

func (s *IddWorkflowTestSuite) TestMarkReadyForReviewRejectsIncompleteIntent() {
	wrapper := func(ctx workflow.Context) error {
		dCtx := newIddMergeApprovalDevContext(utils.NoRetryCtx(ctx), "side/idd")
		return markIddReadyForReview(dCtx, &IddState{}, MarkReadyForReviewArgs{
			RemainingWork: "Implement the remaining authentication paths.",
		})
	}
	s.env.ExecuteWorkflow(wrapper)
	s.Require().Error(s.env.GetWorkflowError())
	s.Contains(s.env.GetWorkflowError().Error(), "Implement the remaining authentication paths")
}

func (s *IddWorkflowTestSuite) TestMarkReadyForReviewRejectsDirtyIntent() {
	s.env.OnActivity(env.EnvRunCommandActivity, mock.Anything, mock.MatchedBy(func(in env.EnvRunCommandActivityInput) bool {
		return strings.Join(in.Args, " ") == "status --porcelain --untracked-files=all -- intent"
	})).Return(env.EnvRunCommandActivityOutput{Stdout: " M intent/a.md\nA  intent/b.md\n?? intent/c.md\n"}, nil).Once()

	wrapper := func(ctx workflow.Context) error {
		dCtx := newIddMergeApprovalDevContext(utils.NoRetryCtx(ctx), "side/idd")
		return markIddReadyForReview(dCtx, &IddState{}, MarkReadyForReviewArgs{
			AllIntentChangesFullySatisfied: true,
			RemainingWork:                  "Nothing remains.",
		})
	}
	s.env.ExecuteWorkflow(wrapper)
	s.Require().Error(s.env.GetWorkflowError())
	s.Contains(s.env.GetWorkflowError().Error(), "uncommitted intent")
	s.Contains(s.env.GetWorkflowError().Error(), "intent/c.md")
}

func (s *IddWorkflowTestSuite) TestMarkReadyForReviewRechecksConcurrentDispatch() {
	s.env.OnActivity(env.EnvRunCommandActivity, mock.Anything, mock.Anything).
		Return(env.EnvRunCommandActivityOutput{}, nil).After(2 * time.Second).Once()

	wrapper := func(ctx workflow.Context) error {
		dCtx := newIddMergeApprovalDevContext(utils.NoRetryCtx(ctx), "side/idd")
		state := &IddState{}
		workflow.Go(ctx, func(ctx workflow.Context) {
			_ = workflow.Sleep(ctx, time.Second)
			state.Subtasks = append(state.Subtasks, IddSubtask{FlowId: "new-subtask", Status: "pending"})
		})
		return markIddReadyForReview(dCtx, state, MarkReadyForReviewArgs{
			AllIntentChangesFullySatisfied: true,
			RemainingWork:                  "Nothing remains.",
		})
	}
	s.env.ExecuteWorkflow(wrapper)
	s.Require().Error(s.env.GetWorkflowError())
	s.Contains(s.env.GetWorkflowError().Error(), "new-subtask")
}

func (s *IddWorkflowTestSuite) TestMarkReadyForReviewSignalsOnceAndRearms() {
	s.env.OnActivity(env.EnvRunCommandActivity, mock.Anything, mock.Anything).
		Return(env.EnvRunCommandActivityOutput{}, nil)
	s.env.OnActivity(s.ima.PutWorkflow, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(s.ima.UpdateTaskByTaskId, mock.Anything, "test-workspace", "task-1", TaskUpdate{
		Status: domain.TaskStatusInProgress, AgentType: domain.AgentTypeLLM,
	}).Return(nil).Once()

	child := func(ctx workflow.Context) error {
		dCtx := newIddMergeApprovalDevContext(utils.NoRetryCtx(ctx), "side/idd")
		state := &IddState{}
		state.mergeApproval = &iddMergeApproval{
			input: iddMergeApprovalInput(),
			req: flow_action.RequestForUser{
				FlowActionId: "approval", RequestKind: flow_action.RequestKindMergeApproval,
			},
		}
		args := MarkReadyForReviewArgs{AllIntentChangesFullySatisfied: true, RemainingWork: "Nothing remains."}
		for i := 0; i < 2; i++ {
			if err := markIddReadyForReview(dCtx, state, args); err != nil {
				return err
			}
		}
		flowID := reservePendingSubtask(dCtx, iddMergeApprovalInput(), state, "")
		if err := markIddReadyForReview(dCtx, state, args); err == nil {
			return errors.New("pending work was marked ready")
		}
		updateSubtaskStatus(dCtx, iddMergeApprovalInput(), state, flowID, "completed")
		return markIddReadyForReview(dCtx, state, args)
	}
	s.env.RegisterWorkflowWithOptions(child, workflow.RegisterOptions{Name: "review-child"})
	parent := func(ctx workflow.Context) ([]flow_action.RequestForUser, error) {
		var requests []flow_action.RequestForUser
		ch := workflow.GetSignalChannel(ctx, flow_action.SignalNameRequestForUser)
		workflow.Go(ctx, func(ctx workflow.Context) {
			for {
				var req flow_action.RequestForUser
				ch.Receive(ctx, &req)
				requests = append(requests, req)
			}
		})
		err := workflow.ExecuteChildWorkflow(ctx, "review-child").Get(ctx, nil)
		return requests, err
	}
	s.env.ExecuteWorkflow(parent)
	s.Require().NoError(s.env.GetWorkflowError())
	var requests []flow_action.RequestForUser
	s.Require().NoError(s.env.GetWorkflowResult(&requests))
	s.Require().Len(requests, 2)
	for _, req := range requests {
		s.Equal("approval", req.FlowActionId)
		s.Equal(flow_action.RequestKindMergeApproval, req.RequestKind)
	}
}

func (s *IddWorkflowTestSuite) TestMarkReadyForReviewRejectsWorkCompletedDuringCheck() {
	s.env.OnActivity(env.EnvRunCommandActivity, mock.Anything, mock.Anything).
		Return(env.EnvRunCommandActivityOutput{}, nil).After(2 * time.Second).Once()
	s.env.OnActivity(s.ima.PutWorkflow, mock.Anything, mock.Anything).Return(nil)

	wrapper := func(ctx workflow.Context) error {
		dCtx := newIddMergeApprovalDevContext(utils.NoRetryCtx(ctx), "side/idd")
		state := &IddState{}
		workflow.Go(ctx, func(ctx workflow.Context) {
			_ = workflow.Sleep(ctx, time.Second)
			goCtx := dCtx.WithContext(ctx)
			id := reservePendingSubtask(goCtx, iddMergeApprovalInput(), state, "")
			updateSubtaskStatus(goCtx, iddMergeApprovalInput(), state, id, "completed")
		})
		return markIddReadyForReview(dCtx, state, MarkReadyForReviewArgs{
			AllIntentChangesFullySatisfied: true, RemainingWork: "Nothing remains.",
		})
	}
	s.env.ExecuteWorkflow(wrapper)
	s.Require().Error(s.env.GetWorkflowError())
	s.Contains(s.env.GetWorkflowError().Error(), "reassess")
}

func (s *IddWorkflowTestSuite) TestIntentSubtaskRefreshesPersistedApproval() {
	mu, actions := s.recordMergeApprovalActions()
	s.mockIntentCommitActivities(1)
	s.setupTitleGenerationMocks()
	s.env.OnActivity(s.ima.PutWorkflow, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(s.ima.UpdateTaskByTaskId, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	var diffMu sync.Mutex
	calls := 0
	s.env.OnActivity(git.GitDiffActivity, mock.Anything, mock.Anything, mock.Anything).
		Return(func(context.Context, env.EnvContainer, git.GitDiffParams) (string, error) {
			diffMu.Lock()
			defer diffMu.Unlock()
			calls++
			switch {
			case calls <= 2:
				return "initial", nil
			case calls <= 4:
				return "intent committed", nil
			default:
				return "child merged", nil
			}
		})
	hasPersisted := func(diff string) bool {
		mu.Lock()
		defer mu.Unlock()
		for _, action := range *actions {
			if action.ActionStatus == domain.ActionStatusPending && mergeApprovalInfo(action)["diff"] == diff {
				return true
			}
		}
		return false
	}
	s.env.RegisterWorkflowWithOptions(func(ctx workflow.Context, input BasicDevWorkflowInput) (string, error) {
		s.True(input.AutoMerge)
		for !hasPersisted("intent committed") {
			if err := workflow.Sleep(ctx, time.Millisecond); err != nil {
				return "", err
			}
		}
		return "merged", nil
	}, workflow.RegisterOptions{Name: "BasicDevWorkflow"})

	wrapper := func(ctx workflow.Context) error {
		dCtx := newIddMergeApprovalDevContext(utils.NoRetryCtx(ctx), "side/idd")
		state := &IddState{DefaultTargetBranch: "main"}
		startIddMergeApproval(dCtx, iddMergeApprovalInput(), state)
		for !hasPersisted("initial") {
			if err := workflow.Sleep(ctx, time.Millisecond); err != nil {
				return err
			}
		}
		id := reservePendingSubtask(dCtx, iddMergeApprovalInput(), state, "")
		runIntentSubtask(dCtx, iddMergeApprovalInput(), StartIntentSubtaskSignal{}, state, id, nil)
		for !hasPersisted("child merged") {
			if err := workflow.Sleep(ctx, time.Millisecond); err != nil {
				return err
			}
		}
		return nil
	}
	s.env.ExecuteWorkflow(wrapper)
	s.Require().NoError(s.env.GetWorkflowError())
	s.True(hasPersisted("intent committed"))
	s.True(hasPersisted("child merged"))
}

func (s *IddWorkflowTestSuite) TestOrchestratorRetainsOnlyIncrementalIntentUpdates() {
	var cha *persisted_ai.ChatHistoryActivities
	s.env.OnActivity(cha.ManageV4, mock.Anything, mock.Anything).
		Return(func(_ context.Context, input persisted_ai.ManageInput) (*persisted_ai.ManageOutput, error) {
			return &persisted_ai.ManageOutput{ChatHistory: input.ChatHistory}, nil
		})
	var mu sync.Mutex
	var updates []string
	failed := false
	s.env.OnActivity(cha.AppendMessage, mock.Anything, mock.Anything).
		Return(func(_ context.Context, input persisted_ai.AppendMessageInput) (*persisted_ai.MessageRef, error) {
			mu.Lock()
			defer mu.Unlock()
			for _, block := range input.Message.Content {
				if persisted_ai.GetContextType(block) != persisted_ai.ContextTypeIntentUpdate {
					continue
				}
				if strings.Contains(block.Text, "+second") && !failed {
					failed = true
					return nil, temporal.NewNonRetryableApplicationError("append failed", "test", nil)
				}
				updates = append(updates, block.Text)
			}
			return &persisted_ai.MessageRef{BlockKeys: []string{"block"}, Role: string(input.Message.Role)}, nil
		})
	s.setupTitleGenerationMocks()
	snapshots := []struct {
		base string
		tree string
		diff string
	}{
		{"main", "one", "+first\n"},
		{"one", "one", ""},
		{"one", "two", "+second\n"},
		{"one", "two", "+second\n"},
		{"two", "empty", "-first\n-second\n"},
	}
	for i, snapshot := range snapshots {
		base := snapshot.base
		original := "original-tree"
		if i == 0 {
			base = ""
			original = "main"
		}
		s.env.OnActivity(env.EnvRunCommandActivity, mock.Anything, mock.MatchedBy(func(input env.EnvRunCommandActivityInput) bool {
			return len(input.Args) == 5 && input.Args[3] == base && input.Args[4] == original
		})).Return(env.EnvRunCommandActivityOutput{Stdout: snapshot.tree + "\nincremental\noriginal-tree\n" + snapshot.diff + "\nIDD_SNAPSHOT_COMPLETE\n"}, nil).Once()
	}
	wrapper := func(ctx workflow.Context) error {
		dCtx := newIddMergeApprovalDevContext(utils.NoRetryCtx(ctx), "side/idd")
		state := &IddState{DefaultTargetBranch: "main"}
		history := NewVersionedChatHistory(dCtx, dCtx.WorkspaceId)
		for i := range snapshots {
			runIddOrchestratorTurn(dCtx, iddMergeApprovalInput(), state, history, true, nil)
			if i == 2 && state.intentTree != "one" {
				return fmt.Errorf("failed append advanced snapshot to %q", state.intentTree)
			}
		}
		if state.intentTree != "empty" {
			return fmt.Errorf("deletion was not recorded: %q", state.intentTree)
		}
		return nil
	}
	s.env.ExecuteWorkflow(wrapper)
	s.Require().NoError(s.env.GetWorkflowError())
	mu.Lock()
	defer mu.Unlock()
	s.Require().Len(updates, 3)
	s.Contains(updates[0], "+first")
	s.Contains(updates[1], "+second")
	s.NotContains(updates[1], "+first")
	s.Contains(updates[2], "-first\n-second")
}

func (s *IddWorkflowTestSuite) TestMarkReadyForReviewRejectsDispatchDuringNotification() {
	s.env.OnActivity(env.EnvRunCommandActivity, mock.Anything, mock.Anything).
		Return(env.EnvRunCommandActivityOutput{}, nil)
	s.env.OnActivity(s.ima.PutWorkflow, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(s.ima.UpdateTaskByTaskId, mock.Anything, mock.Anything, mock.Anything, TaskUpdate{
		Status: domain.TaskStatusInProgress, AgentType: domain.AgentTypeLLM,
	}).Return(nil).Twice()
	s.env.OnSignalExternalWorkflow(mock.Anything, "review-parent", "", flow_action.SignalNameRequestForUser, mock.Anything).
		Return(nil).After(2 * time.Second).Once()
	s.env.OnSignalExternalWorkflow(mock.Anything, "review-parent", "", flow_action.SignalNameRequestForUser, mock.Anything).
		Return(nil).Once()

	wrapper := func(ctx workflow.Context) error {
		workflow.GetInfo(ctx).ParentWorkflowExecution = &workflow.Execution{ID: "review-parent"}
		dCtx := newIddMergeApprovalDevContext(utils.NoRetryCtx(ctx), "side/idd")
		state := &IddState{}
		state.mergeApproval = &iddMergeApproval{
			input: iddMergeApprovalInput(),
			req: flow_action.RequestForUser{
				FlowActionId: "approval", RequestKind: flow_action.RequestKindMergeApproval,
			},
		}
		workflow.Go(ctx, func(ctx workflow.Context) {
			_ = workflow.Sleep(ctx, time.Second)
			goCtx := dCtx.WithContext(utils.NoRetryCtx(ctx))
			id := reservePendingSubtask(goCtx, iddMergeApprovalInput(), state, "")
			updateSubtaskStatus(goCtx, iddMergeApprovalInput(), state, id, "completed")
		})
		args := MarkReadyForReviewArgs{AllIntentChangesFullySatisfied: true, RemainingWork: "Nothing remains."}
		err := markIddReadyForReview(dCtx, state, args)
		if err == nil || !strings.Contains(err.Error(), "reassess") {
			return fmt.Errorf("expected stale notification rejection, got %v", err)
		}
		if state.reviewReady {
			return errors.New("stale notification latched readiness")
		}
		return markIddReadyForReview(dCtx, state, args)
	}
	s.env.ExecuteWorkflow(wrapper)
	s.Require().NoError(s.env.GetWorkflowError())
}

func (s *IddWorkflowTestSuite) TestOrchestratorSnapshotRecovery() {
	var cha *persisted_ai.ChatHistoryActivities
	var mu sync.Mutex
	var updates []string
	failAppend := true
	s.env.OnActivity(cha.AppendMessage, mock.Anything, mock.Anything).
		Return(func(_ context.Context, input persisted_ai.AppendMessageInput) (*persisted_ai.MessageRef, error) {
			mu.Lock()
			defer mu.Unlock()
			for _, block := range input.Message.Content {
				if persisted_ai.GetContextType(block) == persisted_ai.ContextTypeIntentUpdate {
					if failAppend {
						failAppend = false
						return nil, temporal.NewNonRetryableApplicationError("append failed", "test", nil)
					}
					updates = append(updates, block.Text)
				}
			}
			return &persisted_ai.MessageRef{BlockKeys: []string{"block"}, Role: string(input.Message.Role)}, nil
		})
	s.env.OnActivity(cha.ManageV4, mock.Anything, mock.Anything).
		Return(func(_ context.Context, input persisted_ai.ManageInput) (*persisted_ai.ManageOutput, error) {
			return &persisted_ai.ManageOutput{ChatHistory: input.ChatHistory}, nil
		})
	s.setupTitleGenerationMocks()
	for _, snapshot := range []struct{ base, tree, diff string }{
		{"lost-tree", "restored", "+restored intent\n"},
		{"lost-tree", "restored", "+restored intent\n"},
		{"restored", "empty", ""},
	} {
		s.env.OnActivity(env.EnvRunCommandActivity, mock.Anything, mock.MatchedBy(func(input env.EnvRunCommandActivityInput) bool {
			return len(input.Args) == 5 && input.Args[3] == snapshot.base && input.Args[4] == "original-tree"
		})).Return(env.EnvRunCommandActivityOutput{
			Stdout: snapshot.tree + "\nfull\noriginal-tree\n" + snapshot.diff + "\nIDD_SNAPSHOT_COMPLETE\n",
		}, nil).Once()
	}
	wrapper := func(ctx workflow.Context) error {
		dCtx := newIddMergeApprovalDevContext(utils.NoRetryCtx(ctx), "side/idd")
		state := &IddState{
			DefaultTargetBranch: "moved-branch", intentBaseTree: "original-tree", intentTree: "lost-tree",
		}
		history := NewVersionedChatHistory(dCtx, dCtx.WorkspaceId)
		runIddOrchestratorTurn(dCtx, iddMergeApprovalInput(), state, history, true, nil)
		if state.intentTree != "lost-tree" {
			return errors.New("failed recovery append advanced baseline")
		}
		runIddOrchestratorTurn(dCtx, iddMergeApprovalInput(), state, history, true, nil)
		if state.intentTree != "restored" {
			return errors.New("successful recovery did not advance baseline")
		}
		runIddOrchestratorTurn(dCtx, iddMergeApprovalInput(), state, history, true, nil)
		if state.intentTree != "empty" {
			return errors.New("empty recovery did not advance baseline")
		}
		return nil
	}
	s.env.ExecuteWorkflow(wrapper)
	s.Require().NoError(s.env.GetWorkflowError())
	mu.Lock()
	defer mu.Unlock()
	s.Require().Len(updates, 2)
	for _, update := range updates {
		s.Contains(update, "full intent diff")
		s.Contains(update, "supersedes earlier intent diffs")
	}
	s.Contains(updates[0], "+restored intent")
	s.NotContains(updates[1], "+restored intent")
}
