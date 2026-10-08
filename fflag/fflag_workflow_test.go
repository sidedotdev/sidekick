package fflag_test

import (
	"testing"

	"sidekick/fflag"
	"sidekick/flow_action"
	"sidekick/utils"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

const testFlag = "test-flag"

func newFlagTestEnv() *testsuite.TestWorkflowEnvironment {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.SetWorkerOptions(utils.TestWorkerOptions())
	return env
}

func flowContext(ctx workflow.Context) flow_action.ExecContext {
	return flow_action.ExecContext{Context: utils.NoRetryCtx(ctx), GlobalState: &flow_action.GlobalState{}}
}

func runFlagWorkflow(t *testing.T, env *testsuite.TestWorkflowEnvironment, wf func(ctx workflow.Context) ([]bool, error)) []bool {
	t.Helper()
	env.ExecuteWorkflow(wf)
	require.NoError(t, env.GetWorkflowError())
	var results []bool
	require.NoError(t, env.GetWorkflowResult(&results))
	env.AssertExpectations(t)
	return results
}

func TestIsEnabledOrDefault(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		defaultValue bool
		output       fflag.EvaluateFlagsOutput
		err          error
		want         bool
	}{
		{
			name:   "flag on",
			output: fflag.EvaluateFlagsOutput{BoolValues: map[string]bool{testFlag: true}},
			want:   true,
		},
		{
			name:         "flag off",
			defaultValue: true,
			output:       fflag.EvaluateFlagsOutput{BoolValues: map[string]bool{testFlag: false}},
			want:         false,
		},
		{
			name:         "flag missing from output resolves to the default",
			defaultValue: true,
			want:         true,
		},
		{
			name:         "evaluation failure resolves to the default",
			defaultValue: true,
			err:          temporal.NewNonRetryableApplicationError("unavailable", "test", nil),
			want:         true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			env := newFlagTestEnv()
			var activities *fflag.FFlagActivities
			env.OnActivity(activities.EvaluateFlags, mock.Anything,
				mock.MatchedBy(func(input fflag.EvaluateFlagsInput) bool {
					value, ok := input.BoolFlags[testFlag]
					return input.TargetingKey != "" && input.Attributes["flowType"] != nil && ok && value == tt.defaultValue
				})).Return(tt.output, tt.err).Once()

			results := runFlagWorkflow(t, env, func(ctx workflow.Context) ([]bool, error) {
				eCtx := flowContext(ctx)
				return []bool{
					fflag.IsEnabledOrDefault(eCtx, testFlag, tt.defaultValue),
					fflag.IsEnabledOrDefault(eCtx, testFlag, tt.defaultValue),
				}, nil
			})
			require.Equal(t, []bool{tt.want, tt.want}, results, "the flag is evaluated once per flow")
		})
	}
}

func TestIsEnabledOffForFlowsPredatingTheFlag(t *testing.T) {
	t.Parallel()
	env := newFlagTestEnv()
	env.OnGetVersion(fflag.VersionChangeId(testFlag), workflow.DefaultVersion, 1).Return(workflow.DefaultVersion)
	var activities *fflag.FFlagActivities
	env.OnActivity(activities.EvaluateFlags, mock.Anything, mock.Anything).
		Return(fflag.EvaluateFlagsOutput{BoolValues: map[string]bool{testFlag: true}}, nil).Maybe()

	results := runFlagWorkflow(t, env, func(ctx workflow.Context) ([]bool, error) {
		eCtx := flowContext(ctx)
		return []bool{
			fflag.IsEnabledOrDefault(eCtx, testFlag, true),
			fflag.IsEnabled(eCtx, testFlag),
		}, nil
	})
	require.Equal(t, []bool{false, false}, results)
	env.AssertNotCalled(t, "EvaluateFlags", mock.Anything, mock.Anything)
}

func TestIsEnabledEvaluatesPerCallWithoutFlowCache(t *testing.T) {
	t.Parallel()
	env := newFlagTestEnv()
	var activities *fflag.FFlagActivities
	env.OnActivity(activities.EvaluateFlags, mock.Anything, mock.Anything).
		Return(fflag.EvaluateFlagsOutput{BoolValues: map[string]bool{testFlag: true}}, nil).Times(2)

	results := runFlagWorkflow(t, env, func(ctx workflow.Context) ([]bool, error) {
		ctx = utils.NoRetryCtx(ctx)
		return []bool{fflag.IsEnabled(ctx, testFlag), fflag.IsEnabled(ctx, testFlag)}, nil
	})
	require.Equal(t, []bool{true, true}, results)
}

func TestEvaluateWithoutVersioning(t *testing.T) {
	t.Parallel()
	env := newFlagTestEnv()
	env.OnGetVersion(fflag.VersionChangeId("settings"), workflow.DefaultVersion, 1).Return(workflow.DefaultVersion).Maybe()
	var activities *fflag.FFlagActivities
	env.OnActivity(activities.EvaluateFlags, mock.Anything, mock.Anything).
		Return(fflag.EvaluateFlagsOutput{}, temporal.NewNonRetryableApplicationError("unavailable", "test", nil)).Once()
	env.OnActivity(activities.EvaluateFlags, mock.Anything, mock.Anything).
		Return(fflag.EvaluateFlagsOutput{IntValues: map[string]int{"limit": 7}}, nil).Maybe()

	input := fflag.EvaluateFlagsInput{
		BoolFlags:        map[string]bool{testFlag: true},
		IntFlags:         map[string]int{"limit": 3},
		StringArrayFlags: map[string][]string{"types": {"a"}},
	}
	want := fflag.EvaluateFlagsOutput{
		BoolValues:        map[string]bool{testFlag: true},
		IntValues:         map[string]int{"limit": 3},
		StringArrayValues: map[string][]string{"types": {"a"}},
	}
	env.ExecuteWorkflow(func(ctx workflow.Context) ([]fflag.EvaluateFlagsOutput, error) {
		eCtx := flowContext(ctx)
		return []fflag.EvaluateFlagsOutput{
			fflag.EvaluateWithoutVersioning(eCtx, "settings", input),
			fflag.EvaluateWithoutVersioning(eCtx, "settings", input),
		}, nil
	})
	require.NoError(t, env.GetWorkflowError())
	var results []fflag.EvaluateFlagsOutput
	require.NoError(t, env.GetWorkflowResult(&results))
	require.Equal(t, []fflag.EvaluateFlagsOutput{want, want}, results,
		"defaults apply to flows of any version and are shared across the flow")
	env.AssertExpectations(t)
}
