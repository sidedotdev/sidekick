package dev

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"

	"sidekick/common"
	"sidekick/domain"
	"sidekick/env"
	"sidekick/flow_action"
	"sidekick/persisted_ai"
	"sidekick/secret_manager"
	"sidekick/srv"
	"sidekick/utils"
)

// ToolNameMapperUserRetryTestSuite verifies that resolveStreamToolNameMapping
// surfaces ResolveToolNameMappingActivity failures through auto-retries plus
// the user-continue retry mechanism instead of failing fatally, and that
// DisableHumanInTheLoop still fails fast after the auto-retries.
type ToolNameMapperUserRetryTestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	env *testsuite.TestWorkflowEnvironment
}

func (s *ToolNameMapperUserRetryTestSuite) SetupTest() {
	s.env = s.NewTestWorkflowEnvironment()
	s.env.SetWorkerOptions(utils.TestWorkerOptions())

	var fa *flow_action.FlowActivities
	s.env.OnActivity(fa.PersistFlowAction, mock.Anything, mock.Anything).Return(nil).Maybe()

	var srvActivities srv.Activities
	s.env.OnActivity(srvActivities.GetFlow, mock.Anything, mock.Anything, mock.Anything).Return(domain.Flow{}, nil).Maybe()
	s.env.OnActivity(srvActivities.PersistFlow, mock.Anything, mock.Anything).Return(nil).Maybe()
}

func (s *ToolNameMapperUserRetryTestSuite) AfterTest(suiteName, testName string) {
	s.env.AssertExpectations(s.T())
}

func (s *ToolNameMapperUserRetryTestSuite) newExecContext(ctx workflow.Context, disableHumanInTheLoop bool) flow_action.ExecContext {
	gs := &flow_action.GlobalState{}
	gs.InitValues()
	return flow_action.ExecContext{
		WorkspaceId:           "test-workspace",
		Context:               ctx,
		DisableHumanInTheLoop: disableHumanInTheLoop,
		FlowScope: &flow_action.FlowScope{
			SubflowName: "test-subflow",
		},
		GlobalState: gs,
		EnvContainer: &env.EnvContainer{
			Env: &env.LocalEnv{WorkingDirectory: "/tmp/test-repo"},
		},
	}
}

// TestResolveToolNameMappingRetriesOnActivityFailure ensures that when
// ResolveToolNameMappingActivity fails through all auto-retry attempts, the
// user-continue prompt is issued and the activity is retried, ultimately
// succeeding without fatally failing the workflow.
func (s *ToolNameMapperUserRetryTestSuite) TestResolveToolNameMappingRetriesOnActivityFailure() {
	callCount := 0
	s.env.OnActivity(ResolveToolNameMappingActivity, mock.Anything, mock.Anything).Return(
		func(ctx context.Context, input ResolveToolNameMappingInput) (ResolveToolNameMappingResult, error) {
			callCount++
			// Fail the first 4 calls so the 3 auto-retries are exhausted and
			// the user prompt is required before eventual success.
			if callCount <= 4 {
				return ResolveToolNameMappingResult{}, errors.New("simulated mapping failure")
			}
			return ResolveToolNameMappingResult{UseMapping: true}, nil
		},
	)

	childWorkflow := func(ctx workflow.Context) (*persisted_ai.ToolNameMappingConfig, error) {
		eCtx := s.newExecContext(ctx, false)
		secrets := secret_manager.SecretManagerContainer{
			SecretManager: secret_manager.MockSecretManager{},
		}
		return resolveStreamToolNameMapping(eCtx, common.ModelConfig{Provider: "anthropic"}, secrets)
	}

	parentWorkflow := func(ctx workflow.Context) (*persisted_ai.ToolNameMappingConfig, error) {
		signalCh := workflow.GetSignalChannel(ctx, flow_action.SignalNameRequestForUser)
		workflow.Go(ctx, func(ctx workflow.Context) {
			for {
				var req flow_action.RequestForUser
				signalCh.Receive(ctx, &req)
				workflow.SignalExternalWorkflow(ctx, req.OriginWorkflowId, "", flow_action.UserResponseSignalName(req.FlowActionId), flow_action.UserResponse{
					FlowActionId: req.FlowActionId,
				}).Get(ctx, nil)
			}
		})

		childCtx := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
			WorkflowID: "child-resolve-tool-name-mapping",
		})
		var mapping *persisted_ai.ToolNameMappingConfig
		err := workflow.ExecuteChildWorkflow(childCtx, childWorkflow).Get(ctx, &mapping)
		return mapping, err
	}

	s.env.RegisterWorkflow(childWorkflow)
	s.env.RegisterWorkflow(parentWorkflow)

	s.env.ExecuteWorkflow(parentWorkflow)
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var mapping *persisted_ai.ToolNameMappingConfig
	s.NoError(s.env.GetWorkflowResult(&mapping))
	s.Require().NotNil(mapping)
	s.Equal(anthropicMCPToolNamePrefix, mapping.Prefix)
	s.GreaterOrEqual(callCount, 5, "activity should be invoked through auto-retries plus a user-prompted retry")
}

// TestResolveToolNameMappingFailsFastWhenHumanInTheLoopDisabled ensures the
// original error is returned after the auto-retries without prompting the
// user when DisableHumanInTheLoop is set.
func (s *ToolNameMapperUserRetryTestSuite) TestResolveToolNameMappingFailsFastWhenHumanInTheLoopDisabled() {
	callCount := 0
	s.env.OnActivity(ResolveToolNameMappingActivity, mock.Anything, mock.Anything).Return(
		func(ctx context.Context, input ResolveToolNameMappingInput) (ResolveToolNameMappingResult, error) {
			callCount++
			return ResolveToolNameMappingResult{}, errors.New("simulated mapping failure")
		},
	)

	requestForUserSeen := false
	testWorkflow := func(ctx workflow.Context) (*persisted_ai.ToolNameMappingConfig, error) {
		signalCh := workflow.GetSignalChannel(ctx, flow_action.SignalNameRequestForUser)
		workflow.Go(ctx, func(ctx workflow.Context) {
			var req flow_action.RequestForUser
			signalCh.Receive(ctx, &req)
			requestForUserSeen = true
		})

		eCtx := s.newExecContext(ctx, true)
		secrets := secret_manager.SecretManagerContainer{
			SecretManager: secret_manager.MockSecretManager{},
		}
		return resolveStreamToolNameMapping(eCtx, common.ModelConfig{Provider: "anthropic"}, secrets)
	}

	s.env.RegisterWorkflow(testWorkflow)
	s.env.ExecuteWorkflow(testWorkflow)

	s.True(s.env.IsWorkflowCompleted())
	s.Error(s.env.GetWorkflowError())
	s.Contains(s.env.GetWorkflowError().Error(), "failed to resolve tool name mapping")
	s.False(requestForUserSeen, "no user-continue prompt should be issued when DisableHumanInTheLoop is set")
	s.Equal(3, callCount, "activity should only be auto-retried when DisableHumanInTheLoop is set")
}

func TestToolNameMapperUserRetryTestSuite(t *testing.T) {
	suite.Run(t, new(ToolNameMapperUserRetryTestSuite))
}
