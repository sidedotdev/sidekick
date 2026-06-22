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
	})).Return(env.EnvRunCommandActivityOutput{Stdout: "diff body", ExitStatus: 0}, nil).Once()

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

	s.False(capturedInput.DetermineRequirements)
	s.True(capturedInput.AutoMerge)
	s.True(capturedInput.Idd)
	s.Equal("test-workspace", capturedInput.WorkspaceId)
	s.Equal("/tmp/repo", capturedInput.RepoDir)
	s.Require().NotNil(capturedInput.StartBranch)
	s.Equal(iddBranch, *capturedInput.StartBranch)
	s.Contains(capturedInput.Requirements, "Implement the following initial intent:")
	s.Contains(capturedInput.Requirements, "git show abc123")
	s.Contains(capturedInput.Requirements, "diff body")
}

func TestIddWorkflowTestSuite(t *testing.T) {
	suite.Run(t, new(IddWorkflowTestSuite))
}
