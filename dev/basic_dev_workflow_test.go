package dev

import (
	"strings"
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
	"sidekick/srv"
	"sidekick/temporalmeta"
	"sidekick/utils"
)

// AutoMergeApprovalTestSuite verifies the AutoMerge option causes getMergeApproval
// to approve and target the configured branch without blocking for human input,
// while still recording a completed flow action.
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
	var meta *temporalmeta.TemporalMetaActivities
	s.env.OnActivity(meta.FetchFlowActionActivities, mock.Anything, mock.Anything).
		Return([]domain.TemporalActivityRef{}, nil).Maybe()
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
			Worktree: &domain.Worktree{
				Name: "side/sub-task",
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

	var persistedActions []domain.FlowAction
	var fa *flow_action.FlowActivities
	s.env.OnActivity(fa.PersistFlowAction, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			persistedActions = append(persistedActions, args.Get(1).(domain.FlowAction))
		}).
		Return(nil)

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

	// The auto-approval must still be recorded as a completed, non-human flow action
	s.Require().NotEmpty(persistedActions)
	last := persistedActions[len(persistedActions)-1]
	s.Equal("user_request.approve.merge", last.ActionType)
	s.Equal(domain.ActionStatusComplete, last.ActionStatus)
	s.False(last.IsHumanAction)
	s.Contains(last.ActionResult, `"Approved":true`)
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

func TestCommitMessageForMerge(t *testing.T) {
	t.Parallel()

	longTitle := strings.Repeat("a", 105)

	cases := []struct {
		name     string
		params   MergeWithReviewParams
		expected string
	}{
		{
			name:     "title preferred over requirements",
			params:   MergeWithReviewParams{Title: "Add retry to merge", Requirements: "Some very long requirements\nwith details"},
			expected: "Add retry to merge",
		},
		{
			name:     "blank title falls back to requirements",
			params:   MergeWithReviewParams{Title: "   ", Requirements: "Some very long requirements\nwith details"},
			expected: "Some very long requirements",
		},
		{
			name:     "empty title falls back to requirements overview",
			params:   MergeWithReviewParams{Requirements: "Preamble\n\nOverview:\nImplement the thing\nmore detail"},
			expected: "Implement the thing",
		},
		{
			name:     "multi-line title keeps only the subject line",
			params:   MergeWithReviewParams{Title: "Subject line\nbody line", Requirements: "requirements"},
			expected: "Subject line",
		},
		{
			name:     "over-long title overflows into the commit body",
			params:   MergeWithReviewParams{Title: longTitle},
			expected: longTitle[:100] + "...\n\n..." + longTitle[100:],
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.expected, commitMessageForMerge(tc.params))
		})
	}
}

func TestCommitTitleForTask(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		title       string
		description string
		expected    string
	}{
		{name: "distinct title is used", title: "Fix flaky test", description: "The test fails sometimes", expected: "Fix flaky test"},
		{name: "title copied from description is dropped", title: "The test fails sometimes", description: "The test fails sometimes ", expected: ""},
		{name: "missing title", title: "", description: "The test fails sometimes", expected: ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.expected, commitTitleForTask(tc.title, tc.description))
		})
	}
}
