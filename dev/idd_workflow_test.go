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
	})).Return(env.EnvRunCommandActivityOutput{Stdout: "diff body", ExitStatus: 0}, nil).Once()

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
				LLMConfig: common.LLMConfig{
					Defaults: []common.ModelConfig{{Provider: "openai"}},
				},
				EnvContainer: &env.EnvContainer{
					Env: &env.LocalEnv{WorkingDirectory: "/tmp/test-repo"},
				},
			},
			Worktree:   &domain.Worktree{Name: iddBranch},
			RepoConfig: common.RepoConfig{},
		}
		state := &IddState{}
		runIntentSubtask(dCtx, IddWorkflowInput{
			WorkspaceId: "test-workspace",
			RepoDir:     "/tmp/repo",
			Title:       "My Intent",
			IddOptions: IddOptions{
				EnvType:  env.EnvTypeLocal,
				RepoMode: env.RepoModeWorktree,
			},
		}, StartIntentSubtaskSignal{Update: false}, state)
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
	})).Return(env.EnvRunCommandActivityOutput{Stdout: "diff body", ExitStatus: 0}, nil).Once()

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

func TestIddWorkflowTestSuite(t *testing.T) {
	suite.Run(t, new(IddWorkflowTestSuite))
}
