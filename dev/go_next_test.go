package dev

import (
	"context"
	"testing"
	"time"

	"sidekick/coding"
	"sidekick/common"
	"sidekick/env"
	"sidekick/fflag"
	"sidekick/flow_action"
	"sidekick/persisted_ai"
	"sidekick/utils"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

func TestGoNextCancelsActiveStream(t *testing.T) {
	t.Parallel()
	var suite testsuite.WorkflowTestSuite
	testEnv := suite.NewTestWorkflowEnvironment()
	testEnv.RegisterDelayedCallback(func() {
		testEnv.SignalWorkflow(SignalNameUserAction, string(flow_action.UserActionGoNext))
	}, time.Second)

	testEnv.ExecuteWorkflow(func(ctx workflow.Context) (bool, error) {
		dCtx := DevContext{ExecContext: flow_action.ExecContext{
			Context: ctx, GlobalState: &flow_action.GlobalState{},
		}}
		SetupUserActionHandler(dCtx)
		streamCtx := dCtx.WithCancelOnPause()
		err := workflow.Sleep(streamCtx, time.Hour)
		pending := dCtx.GlobalState.GetPendingUserAction()
		return err != nil && pending != nil && *pending == flow_action.UserActionGoNext, nil
	})
	require.NoError(t, testEnv.GetWorkflowError())
	var interrupted bool
	require.NoError(t, testEnv.GetWorkflowResult(&interrupted))
	require.True(t, interrupted)
}

func TestRunCommandUserCancellation(t *testing.T) {
	t.Parallel()
	for _, signal := range []string{SignalNamePause, SignalNameUserAction} {
		t.Run(signal, func(t *testing.T) {
			t.Parallel()
			var suite testsuite.WorkflowTestSuite
			testEnv := suite.NewTestWorkflowEnvironment()
			testEnv.SetWorkerOptions(utils.TestWorkerOptions())
			devEnv, err := env.NewLocalEnv(context.Background(), env.LocalEnvParams{RepoDir: t.TempDir()})
			require.NoError(t, err)
			container := env.EnvContainer{Env: devEnv}

			testEnv.OnActivity(CheckCommandPermissionActivity, mock.Anything, mock.Anything).
				Return(CheckCommandPermissionOutput{Result: common.PermissionAutoApprove}, nil)
			testEnv.OnActivity(env.EnvRunCommandActivity, mock.Anything, mock.Anything).
				After(time.Hour).Return(env.EnvRunCommandActivityOutput{ExitStatus: 0}, nil)

			testEnv.RegisterDelayedCallback(func() {
				if signal == SignalNamePause {
					testEnv.SignalWorkflow(signal, Pause{})
				} else {
					testEnv.SignalWorkflow(signal, string(flow_action.UserActionGoNext))
				}
			}, time.Second)

			testEnv.ExecuteWorkflow(func(ctx workflow.Context) (bool, error) {
				dCtx := DevContext{ExecContext: flow_action.ExecContext{
					Context: utils.NoRetryCtx(ctx), GlobalState: &flow_action.GlobalState{},
					EnvContainer: &container,
				}}
				SetupPauseHandler(dCtx, "", nil)
				SetupUserActionHandler(dCtx)
				start := workflow.Now(ctx)
				_, err := RunCommand(dCtx, RunCommandParams{Command: "sleep 3600"})
				return err != nil && workflow.Now(ctx).Sub(start) < time.Minute, nil
			})
			require.NoError(t, testEnv.GetWorkflowError())
			var interrupted bool
			require.NoError(t, testEnv.GetWorkflowResult(&interrupted))
			require.True(t, interrupted)
		})
	}
}

func TestGoNextStageOrder(t *testing.T) {
	t.Parallel()
	for _, planned := range []bool{false, true} {
		name := "basic"
		if planned {
			name = "planned"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			for _, skip := range []string{"", "context", "coding", "tests", "integration", "commit", "criteria"} {
				t.Run("skip_"+skip, func(t *testing.T) {
					t.Parallel()
					h := newReviewDiffsFlowHarness(t)
					h.skipCommitWrites = skip == "commit"
					h.repos.work.stage("work.txt", "work to review\n")
					if skip == "criteria" {
						h.unfulfilledRounds = 1
					}
					var stages []string
					sent := false
					h.env.SetOnActivityStartedListener(func(info *activity.Info, _ context.Context, args converter.EncodedValues) {
						stage := ""
						switch info.ActivityType.Name {
						case "Stream":
							var input persisted_ai.StreamInput
							require.NoError(t, args.Get(&input))
							for _, tool := range input.Options.Tools {
								switch tool.Name {
								case doneTool.Name:
									stage = "coding"
								case determineCriteriaFulfillmentTool.Name:
									stage = "criteria"
								}
							}
							if offersCommitDone(input.Options.Tools) {
								stage = "commit"
							}
							if input.Options.ToolChoice.Name == getSymbolDefinitionsTool.Name {
								stage = "context"
							}
						case "EnvRunCommandActivity":
							var input env.EnvRunCommandActivityInput
							require.NoError(t, args.Get(&input))
							if len(input.Args) == 3 && input.Args[2] == "echo stage-test" {
								stage = "tests"
							}
							if len(input.Args) == 3 && input.Args[2] == "echo integration-test" {
								stage = "integration"
							}
						}
						if stage == "" {
							return
						}
						stages = append(stages, stage)
						if !sent && stage == skip {
							sent = true
							h.env.SignalWorkflow(SignalNameUserAction, string(flow_action.UserActionGoNext))
						}
					})
					h.env.ExecuteWorkflow(func(ctx workflow.Context) error {
						dCtx := h.devContext(utils.NoRetryCtx(ctx))
						dCtx.GlobalState.SetValue(common.KeyCurrentTargetBranch, "main")
						dCtx.RepoConfig.TestCommands = []common.CommandConfig{{Command: "echo stage-test"}}
						dCtx.RepoConfig.IntegrationTestCommands = []common.CommandConfig{{Command: "echo integration-test"}}
						SetupUserActionHandler(dCtx)
						if planned {
							step := DevStep{StepNumber: "1", Title: "Work", Definition: "do work", Type: "edit", RunIntegrationTests: true}
							plan := DevPlanExecution{Plan: &DevPlan{Steps: []DevStep{step}}}
							result, err := completeDevStepSubflow(dCtx, "do work", plan, step)
							if err == nil {
								require.True(t, result.Successful)
							}
							return err
						}
						target := "main"
						_, err := codingSubflow(dCtx, "do work", &target, "", coding.ReviewDiffBaseline{})
						return err
					})
					require.NoError(t, h.env.GetWorkflowError())
					expected := []string{"context", "coding", "tests", "integration", "commit", "criteria"}
					if skip == "context" {
						expected = []string{"context", "tests", "integration", "commit", "criteria"}
					}
					if skip == "tests" {
						expected = []string{"context", "coding", "tests", "commit", "criteria"}
					}
					require.Equal(t, expected, stages)
					require.Equal(t, skip != "", sent)
				})
			}
		})
	}
}

func TestPlannedStepLegacyCommitPrecedesReview(t *testing.T) {
	t.Parallel()
	h := newReviewDiffsFlowHarness(t)
	h.repos.work.stage("work.txt", "work to review\n")
	h.env.OnGetVersion(fflag.VersionChangeId(fflag.LlmCommitMessages), workflow.DefaultVersion, 1).
		Return(workflow.DefaultVersion)
	h.env.SetOnActivityStartedListener(func(info *activity.Info, _ context.Context, args converter.EncodedValues) {
		if info.ActivityType.Name != "Stream" {
			return
		}
		var input persisted_ai.StreamInput
		require.NoError(t, args.Get(&input))
		for _, tool := range input.Options.Tools {
			if tool.Name == determineCriteriaFulfillmentTool.Name {
				require.Contains(t, h.branchCommitSubjects(), "Work")
			}
		}
	})
	h.env.ExecuteWorkflow(func(ctx workflow.Context) error {
		dCtx := h.devContext(utils.NoRetryCtx(ctx))
		dCtx.GlobalState.SetValue(common.KeyCurrentTargetBranch, "main")
		step := DevStep{StepNumber: "1", Title: "Work", Definition: "do work", Type: "edit"}
		plan := DevPlanExecution{Plan: &DevPlan{Steps: []DevStep{step}}}
		_, err := completeDevStepSubflow(dCtx, "do work", plan, step)
		return err
	})
	require.NoError(t, h.env.GetWorkflowError())
	require.Equal(t, 1, h.fulfillmentRounds)
	require.Zero(t, h.commitSubflowRuns)
}

func TestCodingSkipsCleanCommit(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"basic", "planned"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			h := newReviewDiffsFlowHarness(t)
			h.env.ExecuteWorkflow(func(ctx workflow.Context) error {
				dCtx := h.devContext(utils.NoRetryCtx(ctx))
				dCtx.GlobalState.SetValue(common.KeyCurrentTargetBranch, "main")
				if name == "planned" {
					step := DevStep{StepNumber: "1", Title: "Work", Definition: "do work", Type: "edit"}
					plan := DevPlanExecution{Plan: &DevPlan{Steps: []DevStep{step}}}
					_, err := completeDevStepSubflow(dCtx, "do work", plan, step)
					return err
				}
				target := "main"
				_, err := codingSubflow(dCtx, "do work", &target, "", coding.ReviewDiffBaseline{})
				return err
			})
			require.NoError(t, h.env.GetWorkflowError())
			require.Equal(t, 1, h.fulfillmentRounds)
			require.Zero(t, h.commitSubflowRuns)
		})
	}
}
