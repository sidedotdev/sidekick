package dev

import (
	"fmt"
	"testing"

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
		flowId := reservePendingSubtask(dCtx, state, "")
		runIntentSubtask(dCtx, IddWorkflowInput{
			WorkspaceId: "test-workspace",
			RepoDir:     "/tmp/repo",
			Title:       "My Intent",
			IddOptions: IddOptions{
				EnvType:           env.EnvTypeLocal,
				RepoMode:          env.RepoModeWorktree,
				ContextGatherType: ContextGatherTypeExplore,
			},
		}, StartIntentSubtaskSignal{Update: false}, state, flowId, nil)
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
		flowId := reservePendingSubtask(dCtx, state, "")
		var result triggerResult
		runIntentSubtask(dCtx, IddWorkflowInput{
			WorkspaceId: "test-workspace",
			RepoDir:     "/tmp/repo",
			Title:       "My Intent",
			IddOptions: IddOptions{
				EnvType:  env.EnvTypeLocal,
				RepoMode: env.RepoModeWorktree,
			},
		}, StartIntentSubtaskSignal{Update: false}, state, flowId, func() {
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

// TestFinishIddSignalMergesAndCloses drives the finish-path signal end-to-end:
// a parent workflow launches a child that mirrors the IDD workflow's
// finish-related selector branch with a ready DevContext, signals
// SignalNameFinishIdd, and asserts the merge activity ran and a closure signal
// with reason "completed" was delivered back to the parent.
func (s *IddWorkflowTestSuite) TestFinishIddSignalMergesAndCloses() {
	const iddBranch = "side/idd-worktree"
	const targetBranch = "main"

	s.env.OnActivity(git.GitAddActivity, mock.Anything, mock.MatchedBy(func(input git.GitAddActivityInput) bool {
		return input.Path == "."
	})).Return(nil).Once()

	s.env.OnActivity(git.GitCommitActivity, mock.Anything, mock.Anything, mock.MatchedBy(func(params git.GitCommitParams) bool {
		return params.CommitAll && params.IgnoreNothingToCommit
	})).Return("commit-sha", nil).Once()

	s.env.OnActivity(env.EnvRunCommandActivity, mock.Anything, mock.MatchedBy(func(in env.EnvRunCommandActivityInput) bool {
		return len(in.Args) > 0 && in.Args[0] == "rev-parse"
	})).Return(env.EnvRunCommandActivityOutput{Stdout: "deadbeef\n", ExitStatus: 0}, nil).Once()

	s.env.OnActivity(env.EnvRunCommandActivity, mock.Anything, mock.MatchedBy(func(in env.EnvRunCommandActivityInput) bool {
		return len(in.Args) > 0 && in.Args[0] == "show"
	})).Return(env.EnvRunCommandActivityOutput{Stdout: "diff body", ExitStatus: 0}, nil).Twice()

	var capturedMergeParams git.GitMergeParams
	s.env.OnActivity(git.GitMergeActivity, mock.Anything, mock.Anything, mock.MatchedBy(func(params git.GitMergeParams) bool {
		capturedMergeParams = params
		return true
	})).Return(git.MergeActivityResult{HasConflicts: false}, nil).Once()

	s.env.OnActivity(git.CleanupWorktreeActivity, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()

	miniIdd := func(ctx workflow.Context) error {
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
		state := &IddState{}
		finishIddCh := workflow.GetSignalChannel(dCtx, SignalNameFinishIdd)
		finished := false
		for !finished {
			selector := workflow.NewSelector(dCtx)
			selector.AddReceive(finishIddCh, func(c workflow.ReceiveChannel, _ bool) {
				var sig FinishIddSignal
				c.Receive(dCtx, &sig)
				if err := finishIdd(dCtx, IddWorkflowInput{
					WorkspaceId: "test-workspace",
					RepoDir:     "/tmp/repo",
					Title:       "My Intent",
				}, sig, state); err != nil {
					workflow.GetLogger(dCtx).Error("Failed to finish idd flow", "Error", err)
					return
				}
				finished = true
			})
			selector.Select(dCtx)
			if dCtx.Err() != nil {
				return dCtx.Err()
			}
		}
		return signalWorkflowClosure(dCtx, "completed")
	}
	s.env.RegisterWorkflowWithOptions(miniIdd, workflow.RegisterOptions{Name: "miniIdd"})

	parent := func(ctx workflow.Context) (WorkflowClosure, error) {
		closureCh := workflow.GetSignalChannel(ctx, SignalNameWorkflowClosed)
		childCtx := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
			WorkflowID: "child-idd",
		})
		childFuture := workflow.ExecuteChildWorkflow(childCtx, "miniIdd")
		var execution workflow.Execution
		if err := childFuture.GetChildWorkflowExecution().Get(ctx, &execution); err != nil {
			return WorkflowClosure{}, err
		}
		if err := workflow.SignalExternalWorkflow(ctx, execution.ID, "", SignalNameFinishIdd, FinishIddSignal{TargetBranch: targetBranch}).Get(ctx, nil); err != nil {
			return WorkflowClosure{}, err
		}
		var closure WorkflowClosure
		closureCh.Receive(ctx, &closure)
		if err := childFuture.Get(ctx, nil); err != nil {
			return closure, err
		}
		return closure, nil
	}
	s.env.RegisterWorkflow(parent)

	s.env.ExecuteWorkflow(parent)
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var closure WorkflowClosure
	s.NoError(s.env.GetWorkflowResult(&closure))
	s.Equal("completed", closure.Reason)
	s.Equal(iddBranch, capturedMergeParams.SourceBranch)
	s.Equal(targetBranch, capturedMergeParams.TargetBranch)
	s.Equal(git.MergeStrategyMerge, capturedMergeParams.MergeStrategy)
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
// updates IddState.AutoMode so that subsequent orchestrator runs gate on it.
func (s *IddWorkflowTestSuite) TestSetAutoModeSignalTogglesState() {
	miniIdd := func(ctx workflow.Context) (IddState, error) {
		ctx = utils.NoRetryCtx(ctx)
		state := &IddState{}
		setAutoCh := workflow.GetSignalChannel(ctx, SignalNameSetIddAutoMode)
		var sig SetIddAutoModeSignal
		setAutoCh.Receive(ctx, &sig)
		state.AutoMode = sig.Enabled
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
		flowId := reservePendingSubtask(dCtx, state, "")
		runIntentSubtask(dCtx, IddWorkflowInput{
			WorkspaceId: "test-workspace",
			RepoDir:     "/tmp/repo",
			Title:       "My Intent",
			IddOptions: IddOptions{
				EnvType:           env.EnvTypeLocal,
				RepoMode:          env.RepoModeWorktree,
				ContextGatherType: ContextGatherTypeExplore,
			},
		}, StartIntentSubtaskSignal{Planned: true}, state, flowId, nil)
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
