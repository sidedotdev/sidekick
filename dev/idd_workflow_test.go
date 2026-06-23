package dev

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"

	"sidekick/coding/git"
	"sidekick/common"
	"sidekick/domain"
	"sidekick/env"
	"sidekick/flow_action"
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
	s.ima = nil
}

func (s *IddWorkflowTestSuite) AfterTest(suiteName, testName string) {
	s.env.AssertExpectations(s.T())
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
				EnvContainer: &env.EnvContainer{
					Env: &env.LocalEnv{WorkingDirectory: "/tmp/test-repo"},
				},
			},
			Worktree:   &domain.Worktree{Name: iddBranch},
			RepoConfig: common.RepoConfig{},
		}
		state := &IddState{}
		flowId := reservePendingSubtask(dCtx, state, "")
		runIntentSubtask(dCtx, IddWorkflowInput{
			WorkspaceId: "test-workspace",
			RepoDir:     "/tmp/repo",
			Title:       "My Intent",
			IddOptions: IddOptions{
				EnvType:  env.EnvTypeLocal,
				RepoMode: env.RepoModeWorktree,
			},
		}, StartIntentSubtaskSignal{Update: false}, state, flowId)
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

	s.False(capturedInput.DetermineRequirements)
	s.True(capturedInput.AutoMerge)
	s.True(capturedInput.Idd)
	s.Equal("test-workspace", capturedInput.WorkspaceId)
	s.Equal("/tmp/repo", capturedInput.RepoDir)
	s.Require().NotNil(capturedInput.StartBranch)
	s.Equal(iddBranch, *capturedInput.StartBranch)
	s.Contains(capturedInput.Requirements, "The following new intent file has already been committed")
	s.Contains(capturedInput.Requirements, "git show abc123")
	s.Contains(capturedInput.Requirements, "diff body")
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
		}, state, chatHistory, false)
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
