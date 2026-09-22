package dev

import (
	"context"
	"errors"
	"testing"
	"time"

	"sidekick/common"
	"sidekick/env"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

func TestRunTestsHookPolicies(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name      string
		policy    string
		actionErr error
		delay     time.Duration
		cancel    bool
		wantCalls int
		wantError bool
	}{
		{name: "success", wantCalls: 2},
		{name: "failure", actionErr: errors.New("reload failed"), wantCalls: 1, wantError: true},
		{name: "warning", policy: "warn", actionErr: errors.New("reload failed"), wantCalls: 2},
		{name: "timeout failure", actionErr: temporal.NewTimeoutError(enumspb.TIMEOUT_TYPE_START_TO_CLOSE, nil), wantCalls: 1, wantError: true},
		{name: "timeout warning", policy: "warn", actionErr: temporal.NewTimeoutError(enumspb.TIMEOUT_TYPE_START_TO_CLOSE, nil), wantCalls: 2},
		{name: "canceled action warning", policy: "warn", actionErr: temporal.NewCanceledError(), wantCalls: 1, wantError: true},
		{name: "parent cancellation warning", policy: "warn", delay: 2 * time.Second, cancel: true, wantCalls: 1, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var suite testsuite.WorkflowTestSuite
			te := suite.NewTestWorkflowEnvironment()
			seconds := 1.0
			action := common.HookActionConfig{
				Type: common.HookActionModalRefreshVolumes, TimeoutSeconds: &seconds,
			}
			if tc.policy != "" {
				action.OnError = &tc.policy
			}
			config := common.HooksConfig{RunTests: []common.HookRuleConfig{{Actions: []common.HookActionConfig{action, action}}}}
			calls := 0
			te.OnActivity(env.ModalRefreshVolumesActivity, mock.Anything, mock.Anything).
				After(tc.delay).
				Return(func(ctx context.Context, input env.ModalRefreshVolumesInput) (env.ModalRefreshVolumesOutput, error) {
					calls++
					require.Equal(t, int32(1), activity.GetInfo(ctx).Attempt)
					require.Equal(t, "original", input.EnvContainer.Env.(*env.ModalEnv).SandboxName)
					return env.ModalRefreshVolumesOutput{}, tc.actionErr
				})
			if tc.cancel {
				te.RegisterDelayedCallback(te.CancelWorkflow, 100*time.Millisecond)
			}
			te.ExecuteWorkflow(func(ctx workflow.Context) error {
				return dispatchRunTestsHooks(ctx, config, env.EnvContainer{Env: &env.ModalEnv{SandboxName: "original"}})
			})
			if tc.wantError {
				require.Error(t, te.GetWorkflowError())
				if !tc.cancel {
					require.Contains(t, te.GetWorkflowError().Error(), "hooks.run_tests[0].actions[0]")
					require.Contains(t, te.GetWorkflowError().Error(), "modal.refresh_volumes")
				}
			} else {
				require.NoError(t, te.GetWorkflowError())
			}
			// Delayed activities can time out before their mock body executes.
			if tc.delay == 0 {
				require.Equal(t, tc.wantCalls, calls)
			}
		})
	}
}

func TestRunTestsHooksMatchingAndOrder(t *testing.T) {
	t.Parallel()
	var suite testsuite.WorkflowTestSuite
	te := suite.NewTestWorkflowEnvironment()
	action := func(seconds float64) common.HookActionConfig {
		return common.HookActionConfig{Type: common.HookActionModalRefreshVolumes, TimeoutSeconds: &seconds}
	}
	config := common.HooksConfig{RunTests: []common.HookRuleConfig{
		{Match: &common.HookMatchConfig{EnvTypes: []string{"local"}}, Actions: []common.HookActionConfig{action(10)}},
		{Actions: []common.HookActionConfig{action(11), action(12)}},
		{Match: &common.HookMatchConfig{EnvTypes: []string{"devpod", "modal"}}, Actions: []common.HookActionConfig{action(13)}},
	}}
	var timeouts []time.Duration
	te.OnActivity(env.ModalRefreshVolumesActivity, mock.Anything, mock.Anything).
		Return(func(ctx context.Context, _ env.ModalRefreshVolumesInput) (env.ModalRefreshVolumesOutput, error) {
			timeouts = append(timeouts, activity.GetInfo(ctx).StartToCloseTimeout)
			return env.ModalRefreshVolumesOutput{}, nil
		}).Times(3)
	te.ExecuteWorkflow(func(ctx workflow.Context) error {
		return dispatchRunTestsHooks(ctx, config, env.EnvContainer{Env: &env.ModalEnv{SandboxName: "original"}})
	})
	require.NoError(t, te.GetWorkflowError())
	require.Equal(t, []time.Duration{11 * time.Second, 12 * time.Second, 13 * time.Second}, timeouts)
	te.AssertExpectations(t)
}

func TestRunTestsHooksValidateUnmatchedActionsBeforeExecution(t *testing.T) {
	t.Parallel()
	var suite testsuite.WorkflowTestSuite
	te := suite.NewTestWorkflowEnvironment()
	te.ExecuteWorkflow(func(ctx workflow.Context) error {
		return dispatchRunTestsHooks(ctx, common.HooksConfig{RunTests: []common.HookRuleConfig{
			{Actions: []common.HookActionConfig{{Type: common.HookActionModalRefreshVolumes}}},
			{Match: &common.HookMatchConfig{EnvTypes: []string{"local"}}, Actions: []common.HookActionConfig{{Type: "shell"}}},
		}}, env.EnvContainer{Env: &env.ModalEnv{SandboxName: "original"}})
	})
	require.ErrorContains(t, te.GetWorkflowError(), "hooks.run_tests[1].actions[0] (shell)")
}

func TestRunTestsHooksNonModal(t *testing.T) {
	t.Parallel()
	for _, policy := range []string{"fail", "warn"} {
		t.Run(policy, func(t *testing.T) {
			t.Parallel()
			var suite testsuite.WorkflowTestSuite
			te := suite.NewTestWorkflowEnvironment()
			te.RegisterActivity(env.ModalRefreshVolumesActivity)
			te.ExecuteWorkflow(func(ctx workflow.Context) error {
				return dispatchRunTestsHooks(ctx, common.HooksConfig{RunTests: []common.HookRuleConfig{{
					Actions: []common.HookActionConfig{{Type: common.HookActionModalRefreshVolumes, OnError: &policy}},
				}}}, env.EnvContainer{Env: &env.LocalEnv{WorkingDirectory: "/repo"}})
			})
			if policy == "fail" {
				require.ErrorContains(t, te.GetWorkflowError(), "requires a Modal environment")
			} else {
				require.NoError(t, te.GetWorkflowError())
			}
		})
	}
}

func TestRunTestsHookDeadline(t *testing.T) {
	t.Parallel()
	var suite testsuite.WorkflowTestSuite
	te := suite.NewTestWorkflowEnvironment()
	te.SetTestTimeout(5 * time.Second)
	observed := make(chan error, 1)
	te.RegisterActivityWithOptions(func(ctx context.Context, _ env.ModalRefreshVolumesInput) (env.ModalRefreshVolumesOutput, error) {
		select {
		case <-ctx.Done():
			observed <- ctx.Err()
			return env.ModalRefreshVolumesOutput{}, ctx.Err()
		case <-time.After(3 * time.Second):
			return env.ModalRefreshVolumesOutput{}, errors.New("activity deadline not enforced")
		}
	}, activity.RegisterOptions{Name: "ModalRefreshVolumesActivity"})
	te.ExecuteWorkflow(func(ctx workflow.Context) error {
		seconds := 0.05
		return dispatchRunTestsHooks(ctx, common.HooksConfig{RunTests: []common.HookRuleConfig{{
			Actions: []common.HookActionConfig{{Type: common.HookActionModalRefreshVolumes, TimeoutSeconds: &seconds}},
		}}}, env.EnvContainer{Env: &env.ModalEnv{SandboxName: "original"}})
	})
	require.ErrorContains(t, te.GetWorkflowError(), "hooks.run_tests[0].actions[0]")
	select {
	case err := <-observed:
		require.ErrorIs(t, err, context.DeadlineExceeded)
	case <-time.After(time.Second):
		t.Fatal("action context deadline was not enforced")
	}
}

func TestMatchHookActions(t *testing.T) {
	t.Parallel()
	action := func(source string) common.HookActionConfig {
		return common.HookActionConfig{Type: common.HookActionScript, Source: source}
	}
	rules := []common.HookRuleConfig{
		{Match: &common.HookMatchConfig{EnvTypes: []string{"local"}}, Actions: []common.HookActionConfig{action("local-only")}},
		{Actions: []common.HookActionConfig{action("nil-match-1"), action("nil-match-2")}},
		{Match: &common.HookMatchConfig{}, Actions: []common.HookActionConfig{action("empty-match")}},
		{Match: &common.HookMatchConfig{EnvTypes: []string{"devpod", "modal"}}, Actions: []common.HookActionConfig{action("sandbox")}},
	}
	for _, tc := range []struct {
		name          string
		envType       string
		wantLocations []string
		wantSources   []string
	}{
		{
			name:          "modal",
			envType:       "modal",
			wantLocations: []string{"hooks.run_command[1].actions[0]", "hooks.run_command[1].actions[1]", "hooks.run_command[2].actions[0]", "hooks.run_command[3].actions[0]"},
			wantSources:   []string{"nil-match-1", "nil-match-2", "empty-match", "sandbox"},
		},
		{
			name:          "local",
			envType:       "local",
			wantLocations: []string{"hooks.run_command[0].actions[0]", "hooks.run_command[1].actions[0]", "hooks.run_command[1].actions[1]", "hooks.run_command[2].actions[0]"},
			wantSources:   []string{"local-only", "nil-match-1", "nil-match-2", "empty-match"},
		},
		{
			name:          "openshell matches only unfiltered rules",
			envType:       "openshell",
			wantLocations: []string{"hooks.run_command[1].actions[0]", "hooks.run_command[1].actions[1]", "hooks.run_command[2].actions[0]"},
			wantSources:   []string{"nil-match-1", "nil-match-2", "empty-match"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			matched := matchHookActions(common.HookEventRunCommand, rules, tc.envType)
			var locations, sources []string
			for _, m := range matched {
				locations = append(locations, m.location)
				sources = append(sources, m.config.Source)
			}
			require.Equal(t, tc.wantLocations, locations)
			require.Equal(t, tc.wantSources, sources)
		})
	}
	require.Empty(t, matchHookActions(common.HookEventRunTests, nil, "modal"))
}

func TestRunCommandHooksReturnsZeroContextWhenNothingMatches(t *testing.T) {
	t.Parallel()
	dCtx := DevContext{RepoConfig: common.RepoConfig{Hooks: common.HooksConfig{RunCommand: []common.HookRuleConfig{
		{Match: &common.HookMatchConfig{EnvTypes: []string{"modal"}}, Actions: []common.HookActionConfig{{Type: common.HookActionScript, Source: "1"}}},
	}}}}
	dCtx.WorkspaceId = "ws-1"

	hooks, hookCtx := runCommandHooks(dCtx, env.EnvTypeLocal)
	require.Empty(t, hooks)
	require.Equal(t, env.CommandHookContext{}, hookCtx)

	hooks, hookCtx = runCommandHooks(dCtx, env.EnvTypeModal)
	require.Len(t, hooks, 1)
	require.Equal(t, "hooks.run_command[0].actions[0]", hooks[0].Location)
	require.Equal(t, common.HookActionScript, hooks[0].Type)
	require.Equal(t, "1", hooks[0].Source)
	require.Equal(t, "ws-1", hookCtx.WorkspaceId)
	require.Equal(t, dCtx.RepoConfig, hookCtx.RepoConfig)
}
