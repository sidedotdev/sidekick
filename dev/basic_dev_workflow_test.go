package dev

import (
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
	"sidekick/srv"
	"sidekick/utils"
)

// AutoMergeApprovalTestSuite verifies the AutoMerge option causes getMergeApproval
// to approve and target the configured branch without a human-in-the-loop request.
type AutoMergeApprovalTestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	env *testsuite.TestWorkflowEnvironment
}

func (s *AutoMergeApprovalTestSuite) SetupTest() {
	s.env = s.NewTestWorkflowEnvironment()
	s.env.SetWorkerOptions(utils.TestWorkerOptions())
}

func (s *AutoMergeApprovalTestSuite) AfterTest(suiteName, testName string) {
	s.env.AssertExpectations(s.T())
}

func (s *AutoMergeApprovalTestSuite) setupCommonMocks() {
	s.env.OnActivity(git.GitDiffActivity, mock.Anything, mock.Anything, mock.Anything).Return("diff content", nil).Maybe()
	s.env.OnActivity(git.WriteTreeActivity, mock.Anything, mock.Anything).Return("tree-hash", nil).Maybe()
}

func (s *AutoMergeApprovalTestSuite) approvalWorkflow(target string, autoMerge bool) func(ctx workflow.Context) (MergeApprovalResponse, error) {
	return func(ctx workflow.Context) (MergeApprovalResponse, error) {
		ctx = utils.NoRetryCtx(ctx)
		dCtx := DevContext{
			ExecContext: flow_action.ExecContext{
				WorkspaceId: "test-workspace",
				Context:     ctx,
				FlowScope: &flow_action.FlowScope{
					SubflowName: "test-subflow",
				},
				GlobalState: &flow_action.GlobalState{},
				EnvContainer: &env.EnvContainer{
					Env: &env.LocalEnv{WorkingDirectory: "/tmp/test-repo"},
				},
			},
			RepoConfig: common.RepoConfig{},
		}
		response, _, _, err := getMergeApproval(dCtx, target, true, "", autoMerge)
		return response, err
	}
}

// TestAutoMergeApprovesWithoutUserRequest ensures that when autoMerge is true,
// the merge is approved automatically against the given target branch. The
// workflow completes without blocking, which would not happen if a human merge
// approval request were issued.
func (s *AutoMergeApprovalTestSuite) TestAutoMergeApprovesWithoutUserRequest() {
	s.setupCommonMocks()

	testWorkflow := s.approvalWorkflow("side/idd-worktree", true)
	s.env.RegisterWorkflow(testWorkflow)

	s.env.ExecuteWorkflow(testWorkflow)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var result MergeApprovalResponse
	s.NoError(s.env.GetWorkflowResult(&result))
	s.True(result.Approved)
	s.Equal("side/idd-worktree", result.TargetBranch)
	s.Equal(MergeStrategySquash, result.MergeStrategy)
}

func (s *AutoMergeApprovalTestSuite) setupMergeMocks() {
	var fa *flow_action.FlowActivities
	s.env.OnActivity(fa.PersistFlowAction, mock.Anything, mock.Anything).Return(nil).Maybe()

	var srvActivities srv.Activities
	s.env.OnActivity(srvActivities.GetFlow, mock.Anything, mock.Anything, mock.Anything).Return(domain.Flow{}, nil).Maybe()
	s.env.OnActivity(srvActivities.PersistFlow, mock.Anything, mock.Anything).Return(nil).Maybe()

	s.env.OnActivity(git.GitAddActivity, mock.Anything, mock.Anything).Return(nil).Maybe()
	s.env.OnActivity(git.GitDiffActivity, mock.Anything, mock.Anything, mock.Anything).Return("diff content", nil).Maybe()
	s.env.OnActivity(git.WriteTreeActivity, mock.Anything, mock.Anything).Return("tree-hash", nil).Maybe()
	s.env.OnActivity(git.GitCommitActivity, mock.Anything, mock.Anything, mock.Anything).Return("commit-sha", nil).Maybe()
	s.env.OnActivity(git.CleanupWorktreeActivity, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
}

func (s *AutoMergeApprovalTestSuite) mergeWorkflow(autoMerge bool, startBranch *string) func(ctx workflow.Context) (MergeApprovalResponse, error) {
	return func(ctx workflow.Context) (MergeApprovalResponse, error) {
		ctx = utils.NoRetryCtx(ctx)
		gs := &flow_action.GlobalState{}
		gs.InitValues()
		dCtx := DevContext{
			ExecContext: flow_action.ExecContext{
				WorkspaceId: "test-workspace",
				Context:     ctx,
				FlowScope: &flow_action.FlowScope{
					SubflowName: "test-subflow",
				},
				GlobalState: gs,
				EnvContainer: &env.EnvContainer{
					Env: &env.LocalEnv{WorkingDirectory: "/tmp/test-repo"},
				},
			},
			Worktree: &domain.Worktree{
				Name: "side/sub-task",
			},
			RepoConfig: common.RepoConfig{},
		}
		_, mergeInfo, _, err := mergeWorktreeIfApproved(dCtx, MergeWithReviewParams{
			CommitRequired: true,
			Requirements:   "Implement the thing",
			StartBranch:    startBranch,
			AutoMerge:      autoMerge,
		}, "")
		return mergeInfo, err
	}
}

// TestAutoMergeMergesIntoConfiguredBranch verifies that when AutoMerge is set,
// mergeWorktreeIfApproved performs the merge against the start branch without
// requesting human approval.
func (s *AutoMergeApprovalTestSuite) TestAutoMergeMergesIntoConfiguredBranch() {
	s.setupMergeMocks()

	var mergedTarget string
	var mergedSource string
	s.env.OnActivity(git.GitMergeActivity, mock.Anything, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			params := args.Get(2).(git.GitMergeParams)
			mergedTarget = params.TargetBranch
			mergedSource = params.SourceBranch
		}).
		Return(git.MergeActivityResult{HasConflicts: false}, nil)

	startBranch := "side/idd-worktree"
	testWorkflow := s.mergeWorkflow(true, &startBranch)
	s.env.RegisterWorkflow(testWorkflow)

	s.env.ExecuteWorkflow(testWorkflow)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var result MergeApprovalResponse
	s.NoError(s.env.GetWorkflowResult(&result))
	s.True(result.Approved)
	s.Equal("side/idd-worktree", result.TargetBranch)
	s.Equal("side/idd-worktree", mergedTarget)
	s.Equal("side/sub-task", mergedSource)
}

func TestAutoMergeApprovalTestSuite(t *testing.T) {
	suite.Run(t, new(AutoMergeApprovalTestSuite))
}
