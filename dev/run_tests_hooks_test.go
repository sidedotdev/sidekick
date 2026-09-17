package dev

import (
	"context"
	"errors"
	"sync"

	"sidekick/coding/git"
	"sidekick/common"
	"sidekick/env"

	"github.com/stretchr/testify/mock"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

func (s *RunTestsTestSuite) TestRunTestsHookFailurePreventsBatch() {
	s.envContainer.Env = &env.ModalEnv{SandboxName: "invocation-sandbox"}
	s.devContext.RepoConfig.Hooks = common.HooksConfig{RunTests: []common.HookRuleConfig{{
		Actions: []common.HookActionConfig{{Type: common.HookActionModalRefreshVolumes}},
	}}}
	s.devContext.RepoConfig.TestCommands = []common.CommandConfig{{Command: "test"}}
	s.env.OnActivity(env.ModalRefreshVolumesActivity, mock.Anything, mock.Anything).
		Return(env.ModalRefreshVolumesOutput{}, errors.New("reload failed")).Once()
	commands := 0
	s.env.OnActivity(env.EnvRunCommandActivity, mock.Anything, mock.Anything).
		Return(func(context.Context, env.EnvRunCommandActivityInput) (env.EnvRunCommandActivityOutput, error) {
			commands++
			return env.EnvRunCommandActivityOutput{}, nil
		}).Maybe()

	s.env.ExecuteWorkflow(s.wrapperWorkflow)
	s.Require().Error(s.env.GetWorkflowError())
	s.Contains(s.env.GetWorkflowError().Error(), "hooks.run_tests[0].actions[0]")
	s.Contains(s.env.GetWorkflowError().Error(), "modal.refresh_volumes")
	s.Equal(0, commands)
}

func (s *RunTestsTestSuite) TestRunTestsHooksBeforePendingHeartbeatRetry() {
	s.envContainer.Env = &env.ModalEnv{SandboxName: "invocation-sandbox"}
	s.devContext.DisableHumanInTheLoop = true
	s.devContext.RepoConfig.Hooks = common.HooksConfig{RunTests: []common.HookRuleConfig{{
		Actions: []common.HookActionConfig{{Type: common.HookActionModalRefreshVolumes}},
	}}}
	s.devContext.RepoConfig.TestCommands = []common.CommandConfig{
		{Command: "stable"}, {Command: "flaky"},
	}
	var mu sync.Mutex
	refreshes := 0
	counts := map[string]int{}
	s.env.OnActivity(env.ModalRefreshVolumesActivity, mock.Anything, mock.Anything).
		Return(func(_ context.Context, input env.ModalRefreshVolumesInput) (env.ModalRefreshVolumesOutput, error) {
			mu.Lock()
			defer mu.Unlock()
			s.Equal("invocation-sandbox", input.EnvContainer.Env.(*env.ModalEnv).SandboxName)
			refreshes++
			if refreshes == 2 {
				s.Equal(1, counts["stable"])
				s.Equal(1, counts["flaky"])
			}
			return env.ModalRefreshVolumesOutput{}, nil
		}).Twice()
	s.env.OnActivity(env.EnvRunCommandActivity, mock.Anything, mock.Anything).
		Return(func(_ context.Context, input env.EnvRunCommandActivityInput) (env.EnvRunCommandActivityOutput, error) {
			mu.Lock()
			defer mu.Unlock()
			name := input.Args[2]
			counts[name]++
			s.Equal(counts[name], refreshes)
			if name == "flaky" && counts[name] == 1 {
				return env.EnvRunCommandActivityOutput{}, temporal.NewTimeoutError(enumspb.TIMEOUT_TYPE_HEARTBEAT, nil)
			}
			return env.EnvRunCommandActivityOutput{}, nil
		})

	s.env.ExecuteWorkflow(s.wrapperWorkflow)
	s.Require().NoError(s.env.GetWorkflowError())
	var result TestResult
	s.Require().NoError(s.env.GetWorkflowResult(&result))
	s.True(result.TestsPassed)
	s.Equal(2, refreshes)
	s.Equal(map[string]int{"stable": 1, "flaky": 2}, counts)
}

func (s *RunTestsTestSuite) TestRunTestsEmptyBatchEmitsNoHooks() {
	s.devContext.RepoConfig.Hooks = common.HooksConfig{RunTests: []common.HookRuleConfig{{
		Actions: []common.HookActionConfig{{Type: common.HookActionModalRefreshVolumes}},
	}}}
	s.env.ExecuteWorkflow(s.wrapperWorkflow)
	s.Require().NoError(s.env.GetWorkflowError())
	var result TestResult
	s.Require().NoError(s.env.GetWorkflowResult(&result))
	s.True(result.TestsSkipped)
}

func (s *RunTestsTestSuite) TestRunTestsLegacyHistoryEmitsNoHooks() {
	s.env.OnGetVersion("run-tests-hooks", workflow.DefaultVersion, 1).Return(workflow.DefaultVersion)
	s.devContext.RepoConfig.Hooks = common.HooksConfig{RunTests: []common.HookRuleConfig{{
		Actions: []common.HookActionConfig{{Type: common.HookActionModalRefreshVolumes}},
	}}}
	s.devContext.RepoConfig.TestCommands = []common.CommandConfig{{Command: "test"}}
	s.env.OnActivity(env.EnvRunCommandActivity, mock.Anything, mock.Anything).
		Return(env.EnvRunCommandActivityOutput{}, nil).Once()
	s.env.ExecuteWorkflow(s.wrapperWorkflow)
	s.Require().NoError(s.env.GetWorkflowError())
}

func (s *RunTestsTestSuite) TestRunTestsReadinessBeforeHook() {
	s.envContainer.Env = &env.ModalEnv{SandboxName: "invocation-sandbox"}
	s.devContext.GlobalState.SetValue(globalStateKeyHibernated, true)
	s.devContext.RepoConfig.Hooks = common.HooksConfig{RunTests: []common.HookRuleConfig{{
		Actions: []common.HookActionConfig{{Type: common.HookActionModalRefreshVolumes}},
	}}}
	s.devContext.RepoConfig.TestCommands = []common.CommandConfig{{Command: "test"}}
	var events []string
	s.env.OnActivity(git.WakeWorktreeActivity, mock.Anything, mock.Anything).
		Return(func(context.Context, git.WakeWorktreeInput) (git.WakeWorktreeOutput, error) {
			events = append(events, "wake")
			return git.WakeWorktreeOutput{}, nil
		}).Once()
	s.env.OnActivity(env.ModalRefreshVolumesActivity, mock.Anything, mock.Anything).
		Return(func(context.Context, env.ModalRefreshVolumesInput) (env.ModalRefreshVolumesOutput, error) {
			events = append(events, "refresh")
			return env.ModalRefreshVolumesOutput{}, nil
		}).Once()
	s.env.OnActivity(env.EnvRunCommandActivity, mock.Anything, mock.Anything).
		Return(func(context.Context, env.EnvRunCommandActivityInput) (env.EnvRunCommandActivityOutput, error) {
			events = append(events, "test")
			return env.EnvRunCommandActivityOutput{}, nil
		}).Once()
	s.env.ExecuteWorkflow(s.wrapperWorkflow)
	s.Require().NoError(s.env.GetWorkflowError())
	s.Equal([]string{"wake", "refresh", "test"}, events)
}
