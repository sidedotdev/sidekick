package dev

import (
	"context"
	"testing"

	"sidekick/common"
	"sidekick/env"
	"sidekick/flow_action"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

func TestRunTestsErrorRecoveryVersioning(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		legacy   bool
		docker   bool
		wantKind flow_action.RequestKind
	}{
		{name: "heartbeat auto retry"},
		{name: "legacy heartbeat prompts", legacy: true, wantKind: flow_action.RequestKindContinue},
		{name: "docker recovery prompts", docker: true, wantKind: flow_action.RequestKindApproval},
		{name: "legacy docker recovery prompts", legacy: true, docker: true, wantKind: flow_action.RequestKindApproval},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s := new(RunTestsTestSuite)
			s.SetT(t)
			s.SetupTest()
			t.Cleanup(func() { s.AfterTest("", "") })
			s.devContext.RepoConfig.TestCommands = []common.CommandConfig{{Command: "test command"}}
			if tt.legacy {
				s.env.OnGetVersion("max-parallel-test-commands", workflow.DefaultVersion, workflow.Version(1)).
					Return(workflow.DefaultVersion).Maybe()
			}

			var commandErr error = temporal.NewTimeoutError(enumspb.TIMEOUT_TYPE_HEARTBEAT, nil)
			if tt.docker {
				commandErr = temporal.NewNonRetryableApplicationError(
					"Docker unavailable", env.ErrTypeDockerEngineUnavailable, nil,
					env.DockerEngineFailureDetails{Host: "test-host"},
				)
			}
			attempts := 0
			s.env.OnActivity(env.EnvRunCommandActivity, mock.Anything, mock.Anything).Return(
				func(_ context.Context, _ env.EnvRunCommandActivityInput) (env.EnvRunCommandActivityOutput, error) {
					attempts++
					if attempts == 1 {
						return env.EnvRunCommandActivityOutput{}, commandErr
					}
					return env.EnvRunCommandActivityOutput{ExitStatus: 0}, nil
				},
			)

			var requests []flow_action.RequestForUser
			parent := func(ctx workflow.Context) (TestResult, error) {
				signals := workflow.GetSignalChannel(ctx, flow_action.SignalNameRequestForUser)
				workflow.Go(ctx, func(ctx workflow.Context) {
					for {
						var req flow_action.RequestForUser
						signals.Receive(ctx, &req)
						requests = append(requests, req)
						approved := false
						workflow.SignalExternalWorkflow(ctx, req.OriginWorkflowId, "",
							flow_action.UserResponseSignalName(req.FlowActionId), flow_action.UserResponse{
								FlowActionId: req.FlowActionId,
								Approved:     &approved,
							}).Get(ctx, nil)
					}
				})
				childCtx := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
					WorkflowID: "error-recovery-versioning",
				})
				var result TestResult
				err := workflow.ExecuteChildWorkflow(childCtx, s.wrapperWorkflow).Get(ctx, &result)
				return result, err
			}
			s.env.RegisterWorkflow(parent)
			s.env.ExecuteWorkflow(parent)

			require.NoError(t, s.env.GetWorkflowError())
			var result TestResult
			require.NoError(t, s.env.GetWorkflowResult(&result))
			require.True(t, result.TestsPassed)
			require.Equal(t, 2, attempts)
			if tt.wantKind == "" {
				require.Empty(t, requests)
				return
			}
			require.Len(t, requests, 1)
			require.Equal(t, tt.wantKind, requests[0].RequestKind)
			if tt.docker {
				require.Equal(t, "restart_docker", requests[0].RequestParams["approveTag"])
				require.Equal(t, "retry_without_restart", requests[0].RequestParams["rejectTag"])
				require.Contains(t, requests[0].Content, "test-host")
			} else {
				require.Equal(t, "try_again", requests[0].RequestParams["continueTag"])
			}
		})
	}
}
