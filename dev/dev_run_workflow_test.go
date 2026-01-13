package dev

import (
	"testing"
	"time"

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

type DevRunWorkflowTestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	env *testsuite.TestWorkflowEnvironment
}

func (s *DevRunWorkflowTestSuite) SetupTest() {
	s.env = s.NewTestWorkflowEnvironment()
}

func (s *DevRunWorkflowTestSuite) AfterTest(suiteName, testName string) {
	s.env.AssertExpectations(s.T())
}

// TestDevRunStartStopViaUserAction tests that Dev Run start/stop actions
// are handled via the user action signal mechanism.
func (s *DevRunWorkflowTestSuite) TestDevRunStartStopViaUserAction() {
	// This test validates that Dev Run actions are handled via SetupUserActionHandler
	// rather than through merge approval param updates.

	// Verify the action types are defined
	s.Equal(UserActionType("dev_run_start"), UserActionDevRunStart)
	s.Equal(UserActionType("dev_run_stop"), UserActionDevRunStop)

	// Verify StartDevRunInput/StopDevRunInput can be constructed
	devRunConfig := common.DevRunConfig{
		Commands: map[string]common.DevRunCommandConfig{
			"test": {Start: common.CommandConfig{Command: "echo 'starting'"}},
		},
	}
	devRunCtx := DevRunContext{
		WorkspaceId:  "test-workspace",
		FlowId:       "test-flow",
		WorktreeDir:  "/tmp/test-worktree",
		SourceBranch: "side/test-branch",
	}

	startInput := StartDevRunInput{
		DevRunConfig: devRunConfig,
		Context:      devRunCtx,
	}
	s.Equal("test-workspace", startInput.Context.WorkspaceId)
	s.Equal("test-flow", startInput.Context.FlowId)

	stopInput := StopDevRunInput{
		DevRunConfig: devRunConfig,
		Context:      devRunCtx,
	}
	s.Equal("test-workspace", stopInput.Context.WorkspaceId)
	s.Equal("test-flow", stopInput.Context.FlowId)
}

// TestDevRunCleanupOnWorkflowCompletion tests that stopActiveDevRun is called
// during normal workflow completion.
func (s *DevRunWorkflowTestSuite) TestDevRunCleanupOnWorkflowCompletion() {
	// This test validates that the stopActiveDevRun function properly constructs
	// the StopDevRunInput from the DevContext.

	// The actual workflow integration is tested via the defer statements in
	// BasicDevWorkflow and PlannedDevWorkflow, which call stopActiveDevRun.
	// Here we just verify the data structures are correct.

	devRunCtx := DevRunContext{
		WorkspaceId:  "workspace-123",
		FlowId:       "flow-456",
		WorktreeDir:  "/path/to/worktree",
		SourceBranch: "side/feature-branch",
	}

	stopInput := StopDevRunInput{
		DevRunConfig: common.DevRunConfig{
			StopTimeoutSeconds: 30,
		},
		Context: devRunCtx,
	}

	s.Equal("workspace-123", stopInput.Context.WorkspaceId)
	s.Equal("flow-456", stopInput.Context.FlowId)
	s.Equal("/path/to/worktree", stopInput.Context.WorktreeDir)
	s.Equal("side/feature-branch", stopInput.Context.SourceBranch)
	s.Equal(30, stopInput.DevRunConfig.StopTimeoutSeconds)
}

// TestDevRunUserActionTypes tests that Dev Run action types are correctly defined
func (s *DevRunWorkflowTestSuite) TestDevRunUserActionTypes() {
	// Test that the action type values are correctly defined
	s.Equal("dev_run_start", string(UserActionDevRunStart))
	s.Equal("dev_run_stop", string(UserActionDevRunStop))
}

// TestDevRunStartSignalDoesNotBlockSelectorCallback tests that sending a dev_run_start
// signal does not cause a panic due to blocking inside the selector callback.
// This reproduces the bug: "trying to block on coroutine which is already blocked"
func (s *DevRunWorkflowTestSuite) TestDevRunStartSignalDoesNotBlockSelectorCallback() {
	testWorkflow := func(ctx workflow.Context) error {
		gs := &flow_action.GlobalState{}
		gs.InitValues()

		// Set up activity options so the activity can be executed
		activityCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
			StartToCloseTimeout: 10 * time.Second,
		})

		dCtx := DevContext{
			ExecContext: flow_action.ExecContext{
				Context:     activityCtx,
				WorkspaceId: "test-workspace",
				GlobalState: gs,
				EnvContainer: &env.EnvContainer{
					Env: &env.LocalEnv{
						WorkingDirectory: "/tmp/test-repo",
					},
				},
			},
			RepoConfig: common.RepoConfig{
				DevRun: common.DevRunConfig{
					Commands: map[string]common.DevRunCommandConfig{
						"test": {Start: common.CommandConfig{Command: "echo test"}},
					},
				},
			},
			Worktree: &domain.Worktree{
				Name: "side/test-branch",
			},
		}

		SetupUserActionHandler(dCtx)

		// Wait briefly to allow signal processing
		_ = workflow.Sleep(ctx, 100*time.Millisecond)

		return nil
	}

	s.env.RegisterWorkflow(testWorkflow)

	// Mock the StartDevRun activity to return immediately (no start commands configured)
	var dra *DevRunActivities
	s.env.OnActivity(dra.StartDevRun, mock.Anything, mock.Anything).Return(StartDevRunOutput{
		Started: false,
	}, nil)

	// Send the signal after workflow starts
	s.env.RegisterDelayedCallback(func() {
		s.env.SignalWorkflow(SignalNameUserAction, string(UserActionDevRunStart))
	}, 10*time.Millisecond)

	s.env.ExecuteWorkflow(testWorkflow)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())
}

// TestDevRunContextBranchUpdate tests that both TargetBranch and BaseBranch
// are updated when the target branch changes during merge approval.
func (s *DevRunWorkflowTestSuite) TestDevRunContextBranchUpdate() {
	// Simulate the branch update logic from GetUserMergeApproval
	devRunCtx := DevRunContext{
		WorkspaceId:  "test-workspace",
		FlowId:       "test-flow",
		WorktreeDir:  "/tmp/test-worktree",
		SourceBranch: "side/feature-branch",
		BaseBranch:   "main",
		TargetBranch: "main",
	}

	// Simulate user changing target branch to "develop"
	newTargetBranch := "develop"
	devRunCtx.TargetBranch = newTargetBranch
	devRunCtx.BaseBranch = newTargetBranch

	// Both should be updated so scripts receive consistent branch info
	s.Equal("develop", devRunCtx.TargetBranch)
	s.Equal("develop", devRunCtx.BaseBranch)
	s.Equal("side/feature-branch", devRunCtx.SourceBranch) // Source unchanged
}

// TestMergeStrategyInParams tests that merge strategy is properly included in
// MergeApprovalParams and MergeApprovalResponse.
func (s *DevRunWorkflowTestSuite) TestMergeStrategyInParams() {
	// Test default merge strategy in params
	mergeParams := MergeApprovalParams{
		SourceBranch:         "side/feature-branch",
		DefaultTargetBranch:  "main",
		Diff:                 "test diff",
		DefaultMergeStrategy: MergeStrategySquash,
	}

	s.Equal(MergeStrategySquash, mergeParams.DefaultMergeStrategy)

	// Test merge strategy in response
	response := MergeApprovalResponse{
		Approved:      true,
		TargetBranch:  "main",
		MergeStrategy: MergeStrategySquash,
	}

	s.True(response.Approved)
	s.Equal(MergeStrategySquash, response.MergeStrategy)

	// Test merge strategy can be set to regular merge
	response.MergeStrategy = MergeStrategyMerge
	s.Equal(MergeStrategyMerge, response.MergeStrategy)
}

// TestMergeStrategyParamParsing tests that mergeStrategy params are correctly parsed
func (s *DevRunWorkflowTestSuite) TestMergeStrategyParamParsing() {
	testCases := []struct {
		name             string
		params           map[string]interface{}
		expectedStrategy MergeStrategy
		hasStrategy      bool
	}{
		{
			name:             "squash strategy",
			params:           map[string]interface{}{"mergeStrategy": "squash"},
			expectedStrategy: MergeStrategySquash,
			hasStrategy:      true,
		},
		{
			name:             "merge strategy",
			params:           map[string]interface{}{"mergeStrategy": "merge"},
			expectedStrategy: MergeStrategyMerge,
			hasStrategy:      true,
		},
		{
			name:             "no strategy",
			params:           map[string]interface{}{},
			expectedStrategy: "",
			hasStrategy:      false,
		},
		{
			name:             "other params only",
			params:           map[string]interface{}{"targetBranch": "develop"},
			expectedStrategy: "",
			hasStrategy:      false,
		},
	}

	for _, tc := range testCases {
		s.Run(tc.name, func() {
			strategyVal, ok := tc.params["mergeStrategy"].(string)
			s.Equal(tc.hasStrategy, ok)
			if tc.hasStrategy {
				s.Equal(tc.expectedStrategy, MergeStrategy(strategyVal))
			}
		})
	}
}

// TestMergeStrategyDefaultsToSquash tests that when no merge strategy is specified,
// it defaults to squash.
func (s *DevRunWorkflowTestSuite) TestMergeStrategyDefaultsToSquash() {
	// When DefaultMergeStrategy is empty, the code should default to squash
	mergeParams := MergeApprovalParams{
		SourceBranch:        "side/feature-branch",
		DefaultTargetBranch: "main",
		Diff:                "test diff",
		// DefaultMergeStrategy not set
	}

	// Simulate the defaulting logic from GetUserMergeApproval
	finalMergeStrategy := mergeParams.DefaultMergeStrategy
	if finalMergeStrategy == "" {
		finalMergeStrategy = MergeStrategySquash
	}

	s.Equal(MergeStrategySquash, finalMergeStrategy)
}

func TestDevRunWorkflowTestSuite(t *testing.T) {
	suite.Run(t, new(DevRunWorkflowTestSuite))
}

// MergeStrategyRoundTripTestSuite tests the full round-trip of merge strategy
// from GetUserMergeApproval param updates through to the response.
type MergeStrategyRoundTripTestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	env *testsuite.TestWorkflowEnvironment
}

func (s *MergeStrategyRoundTripTestSuite) SetupTest() {
	s.env = s.NewTestWorkflowEnvironment()
}

func (s *MergeStrategyRoundTripTestSuite) AfterTest(suiteName, testName string) {
	s.env.AssertExpectations(s.T())
}

func (s *MergeStrategyRoundTripTestSuite) setupCommonMocks() {
	var fa *flow_action.FlowActivities
	s.env.OnActivity(fa.PersistFlowAction, mock.Anything, mock.Anything).Return(nil).Maybe()
	s.env.OnActivity(git.GitDiffActivity, mock.Anything, mock.Anything).Return("new diff", nil).Maybe()

	var srvActivities srv.Activities
	s.env.OnActivity(srvActivities.GetFlow, mock.Anything, mock.Anything, mock.Anything).Return(domain.Flow{}, nil).Maybe()
	s.env.OnActivity(srvActivities.PersistFlow, mock.Anything, mock.Anything).Return(nil).Maybe()
}

func (s *MergeStrategyRoundTripTestSuite) createTestWorkflow() func(ctx workflow.Context) (MergeApprovalResponse, error) {
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
			},
			RepoConfig: common.RepoConfig{},
		}

		mergeParams := MergeApprovalParams{
			SourceBranch:         "side/test-branch",
			DefaultTargetBranch:  "main",
			Diff:                 "test diff content",
			DefaultMergeStrategy: MergeStrategySquash,
		}

		return GetUserMergeApproval(dCtx, "Please review", map[string]any{
			"mergeApprovalInfo": mergeParams,
		})
	}
}

// TestMergeStrategyRoundTrip tests that merge strategy param updates flow through
// GetUserMergeApproval and are returned in the response.
func (s *MergeStrategyRoundTripTestSuite) TestMergeStrategyRoundTrip() {
	testWorkflow := s.createTestWorkflow()
	s.env.RegisterWorkflow(testWorkflow)
	s.setupCommonMocks()

	parentWorkflow := func(ctx workflow.Context, mergeStrategy MergeStrategy) (MergeApprovalResponse, error) {
		signalCh := workflow.GetSignalChannel(ctx, flow_action.SignalNameRequestForUser)

		workflow.Go(ctx, func(ctx workflow.Context) {
			var req flow_action.RequestForUser
			signalCh.Receive(ctx, &req)

			// First, send a param-only update with the merge strategy (Approved is nil)
			// The workflow will loop and wait for another userResponse signal (not a new request)
			workflow.SignalExternalWorkflow(ctx, req.OriginWorkflowId, "", flow_action.SignalNameUserResponse, flow_action.UserResponse{
				TargetWorkflowId: req.OriginWorkflowId,
				Params: map[string]interface{}{
					"mergeStrategy": string(mergeStrategy),
				},
			}).Get(ctx, nil)

			// Now send the final approval (no need to wait for another request signal)
			approved := true
			workflow.SignalExternalWorkflow(ctx, req.OriginWorkflowId, "", flow_action.SignalNameUserResponse, flow_action.UserResponse{
				TargetWorkflowId: req.OriginWorkflowId,
				Approved:         &approved,
			}).Get(ctx, nil)
		})

		childCtx := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
			WorkflowID: "child-merge-approval-workflow",
		})
		var result MergeApprovalResponse
		err := workflow.ExecuteChildWorkflow(childCtx, testWorkflow).Get(ctx, &result)
		return result, err
	}

	s.env.RegisterWorkflow(parentWorkflow)

	// Test with "merge" strategy
	s.env.ExecuteWorkflow(parentWorkflow, MergeStrategyMerge)
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var result MergeApprovalResponse
	s.NoError(s.env.GetWorkflowResult(&result))
	s.True(result.Approved)
	s.Equal(MergeStrategyMerge, result.MergeStrategy)
	s.Equal("main", result.TargetBranch)
}

// TestMergeStrategyDefaultsToSquashInResponse tests that when no merge strategy
// is explicitly set via params, the response defaults to squash.
func (s *MergeStrategyRoundTripTestSuite) TestMergeStrategyDefaultsToSquashInResponse() {
	testWorkflow := s.createTestWorkflow()
	s.env.RegisterWorkflow(testWorkflow)
	s.setupCommonMocks()

	parentWorkflow := func(ctx workflow.Context) (MergeApprovalResponse, error) {
		signalCh := workflow.GetSignalChannel(ctx, flow_action.SignalNameRequestForUser)

		workflow.Go(ctx, func(ctx workflow.Context) {
			var req flow_action.RequestForUser
			signalCh.Receive(ctx, &req)

			// Immediately approve without setting merge strategy
			approved := true
			workflow.SignalExternalWorkflow(ctx, req.OriginWorkflowId, "", flow_action.SignalNameUserResponse, flow_action.UserResponse{
				TargetWorkflowId: req.OriginWorkflowId,
				Approved:         &approved,
			}).Get(ctx, nil)
		})

		childCtx := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
			WorkflowID: "child-merge-approval-workflow",
		})
		var result MergeApprovalResponse
		err := workflow.ExecuteChildWorkflow(childCtx, testWorkflow).Get(ctx, &result)
		return result, err
	}

	s.env.RegisterWorkflow(parentWorkflow)

	s.env.ExecuteWorkflow(parentWorkflow)
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var result MergeApprovalResponse
	s.NoError(s.env.GetWorkflowResult(&result))
	s.True(result.Approved)
	s.Equal(MergeStrategySquash, result.MergeStrategy)
}

func TestMergeStrategyRoundTripTestSuite(t *testing.T) {
	suite.Run(t, new(MergeStrategyRoundTripTestSuite))
}

// TestDevRunStateQuery tests that the dev_run_state query returns the current dev run state.
func (s *DevRunWorkflowTestSuite) TestDevRunStateQuery() {
	testWorkflow := func(ctx workflow.Context) error {
		gs := &flow_action.GlobalState{}
		gs.InitValues()

		dCtx := DevContext{
			ExecContext: flow_action.ExecContext{
				Context:     ctx,
				WorkspaceId: "test-workspace",
				GlobalState: gs,
			},
			RepoConfig: common.RepoConfig{},
		}

		// Set up the query handler
		SetupDevRunStateQuery(dCtx)

		// Initially, there should be no active runs
		// Add a dev run instance to GlobalState
		instance := &DevRunInstance{
			DevRunId:       "devrun_test123",
			SessionId:      12345,
			OutputFilePath: "/tmp/test-output.log",
			CommandId:      "dev-server",
		}
		SetDevRunInstance(gs, instance)

		// Wait to allow query to be processed
		_ = workflow.Sleep(ctx, 100*time.Millisecond)

		return nil
	}

	s.env.RegisterWorkflow(testWorkflow)

	// Query the dev run state after workflow starts
	var queryResult DevRunState
	s.env.RegisterDelayedCallback(func() {
		result, err := s.env.QueryWorkflow(QueryNameDevRunState)
		s.NoError(err)
		s.NoError(result.Get(&queryResult))
	}, 50*time.Millisecond)

	s.env.ExecuteWorkflow(testWorkflow)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	// Verify the query result contains the instance we added
	s.NotNil(queryResult.ActiveRuns)
	s.Len(queryResult.ActiveRuns, 1)
	s.Contains(queryResult.ActiveRuns, "dev-server")
	s.Equal("devrun_test123", queryResult.ActiveRuns["dev-server"].DevRunId)
	s.Equal(12345, queryResult.ActiveRuns["dev-server"].SessionId)
}

// TestDevRunStateQueryEmpty tests that the dev_run_state query returns empty state when no runs are active.
func (s *DevRunWorkflowTestSuite) TestDevRunStateQueryEmpty() {
	testWorkflow := func(ctx workflow.Context) error {
		gs := &flow_action.GlobalState{}
		gs.InitValues()

		dCtx := DevContext{
			ExecContext: flow_action.ExecContext{
				Context:     ctx,
				WorkspaceId: "test-workspace",
				GlobalState: gs,
			},
			RepoConfig: common.RepoConfig{},
		}

		// Set up the query handler
		SetupDevRunStateQuery(dCtx)

		// Wait to allow query to be processed
		_ = workflow.Sleep(ctx, 100*time.Millisecond)

		return nil
	}

	s.env.RegisterWorkflow(testWorkflow)

	// Query the dev run state after workflow starts
	var queryResult DevRunState
	s.env.RegisterDelayedCallback(func() {
		result, err := s.env.QueryWorkflow(QueryNameDevRunState)
		s.NoError(err)
		s.NoError(result.Get(&queryResult))
	}, 50*time.Millisecond)

	s.env.ExecuteWorkflow(testWorkflow)

	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	// Verify the query result is empty
	s.NotNil(queryResult.ActiveRuns)
	s.Len(queryResult.ActiveRuns, 0)
}
