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
	"sidekick/utils"
)

// IddWorkflowTestSuite verifies that an intent sub-task commits the current
// intent state in the IDD worktree and launches a BasicDevWorkflow child wired
// to merge back into that same worktree branch.
type IddWorkflowTestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	env *testsuite.TestWorkflowEnvironment
	ima *DevAgentManagerActivities
}

func (s *IddWorkflowTestSuite) SetupTest() {
	s.env = s.NewTestWorkflowEnvironment()
	s.env.SetWorkerOptions(utils.TestWorkerOptions())
	s.ima = nil
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
	s.env.OnActivity(la.Stream, mock.Anything, mock.Anything).Return(&llm2.MessageResponse{
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
			IddOptions: IddOptions{
				EnvType:           env.EnvTypeLocal,
				RepoMode:          env.RepoModeWorktree,
				ContextGatherType: ContextGatherTypeExplore,
			},
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
	s.Equal(ContextGatherTypeExplore, capturedInput.ContextGatherType)
	s.Equal("test-workspace", capturedInput.WorkspaceId)
	s.Equal("/tmp/repo", capturedInput.RepoDir)
	s.Require().NotNil(capturedInput.StartBranch)
	s.Equal(iddBranch, *capturedInput.StartBranch)
	s.Contains(capturedInput.Requirements, "The following new intent file has already been committed")
	s.Contains(capturedInput.Requirements, "git show abc123")
	s.Contains(capturedInput.Requirements, "diff body")
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

	s.env.OnActivity(env.EnvRunCommandActivity, mock.Anything, mock.MatchedBy(func(in env.EnvRunCommandActivityInput) bool {
		return in.Command == "git" && len(in.Args) >= 2 && in.Args[0] == "diff"
	})).Return(env.EnvRunCommandActivityOutput{Stdout: ""}, nil).Once()
	s.env.OnActivity(git.DiffUntrackedFilesActivity, mock.Anything, mock.Anything, mock.Anything).
		Return("", nil).Once()

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
func (s *IddWorkflowTestSuite) TestRunIntentSubtaskPropagatesContextGatherTypeToPlannedChild() {
	const iddBranch = "side/idd-worktree"

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
			IddOptions: IddOptions{
				EnvType:           env.EnvTypeLocal,
				RepoMode:          env.RepoModeWorktree,
				ContextGatherType: ContextGatherTypeExplore,
			},
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
	s.Equal(ContextGatherTypeExplore, capturedInput.ContextGatherType)
	s.Equal("test-workspace", capturedInput.WorkspaceId)
	s.Equal("/tmp/repo", capturedInput.RepoDir)
	s.Require().NotNil(capturedInput.StartBranch)
	s.Equal(iddBranch, *capturedInput.StartBranch)
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
