package dev

import (
	"fmt"
	"time"

	"sidekick/common"
	"sidekick/env"

	"github.com/rs/zerolog/log"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

const runTestsHooksVersion = "run-tests-hooks"

type matchedHookAction struct {
	location string
	config   common.HookActionConfig
}

// matchHookActions selects the actions of every rule matching envType,
// preserving configuration order. A rule without match filters matches all
// environments.
func matchHookActions(event string, rules []common.HookRuleConfig, envType string) []matchedHookAction {
	var actions []matchedHookAction
	for i, rule := range rules {
		matches := rule.Match == nil || rule.Match.EnvTypes == nil
		if rule.Match != nil {
			for _, candidate := range rule.Match.EnvTypes {
				if candidate == envType {
					matches = true
				}
			}
		}
		if matches {
			for j, action := range rule.Actions {
				actions = append(actions, matchedHookAction{
					location: fmt.Sprintf("hooks.%s[%d].actions[%d]", event, i, j),
					config:   action,
				})
			}
		}
	}
	return actions
}

// runCommandHooks matches run_command rules against envType and returns the
// non-secret hook data to place on an EnvRunCommandActivityInput. Call sites
// that skip this helper run no command hooks.
func runCommandHooks(dCtx DevContext, envType env.EnvType) ([]env.CommandHookAction, env.CommandHookContext) {
	matched := matchHookActions(common.HookEventRunCommand, dCtx.RepoConfig.Hooks.RunCommand, string(envType))
	if len(matched) == 0 {
		return nil, env.CommandHookContext{}
	}
	hooks := make([]env.CommandHookAction, 0, len(matched))
	for _, action := range matched {
		hooks = append(hooks, env.CommandHookAction{
			Location:       action.location,
			Type:           action.config.Type,
			Source:         action.config.Source,
			TimeoutSeconds: action.config.TimeoutSeconds,
			OnError:        action.config.OnError,
		})
	}
	return hooks, env.CommandHookContext{
		WorkspaceId: dCtx.WorkspaceId,
		RepoConfig:  dCtx.RepoConfig,
	}
}

func dispatchRunTestsHooks(ctx workflow.Context, config common.HooksConfig, environment env.EnvContainer) error {
	started := workflow.Now(ctx)
	outcome := "success"
	defer func() {
		if !workflow.IsReplaying(ctx) {
			log.Info().Str("event", common.HookEventRunTests).
				Dur("duration", workflow.Now(ctx).Sub(started)).
				Str("outcome", outcome).Msg("Hook dispatch completed")
		}
	}()
	if err := config.Validate(); err != nil {
		outcome = "invalid"
		return err
	}
	if err := ctx.Err(); err != nil {
		outcome = "canceled"
		return err
	}
	if len(config.RunTests) == 0 {
		return nil
	}
	if environment.Env == nil {
		outcome = "failure"
		return fmt.Errorf("hooks.run_tests: missing invocation environment")
	}
	actions := matchHookActions(common.HookEventRunTests, config.RunTests, string(environment.Env.GetType()))
	for _, action := range actions {
		actionStarted := workflow.Now(ctx)
		err := executeHookAction(ctx, action.config, environment)
		actionOutcome := "success"
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		if err != nil {
			err = fmt.Errorf("%s (%s): %w", action.location, action.config.Type, err)
			switch {
			case ctx.Err() != nil || temporal.IsCanceledError(err):
				actionOutcome = "canceled"
			case action.config.ErrorPolicy() == common.HookOnErrorWarn:
				actionOutcome = "warning"
			default:
				actionOutcome = "failure"
			}
		}
		if !workflow.IsReplaying(ctx) {
			entry := log.Info()
			if err != nil {
				entry = log.Warn().Err(err)
			}
			entry.Str("event", common.HookEventRunTests).
				Str("location", action.location).Str("actionType", action.config.Type).
				Dur("duration", workflow.Now(ctx).Sub(actionStarted)).
				Str("outcome", actionOutcome).Msg("Hook action completed")
		}
		if err != nil {
			outcome = actionOutcome
			if actionOutcome != "warning" {
				return err
			}
		}
	}
	return nil
}

func executeHookAction(ctx workflow.Context, action common.HookActionConfig, environment env.EnvContainer) error {
	timeout := action.Timeout()
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout:    timeout,
		ScheduleToCloseTimeout: timeout,
		HeartbeatTimeout:       min(timeout, 3*time.Second),
		WaitForCancellation:    true,
		RetryPolicy:            &temporal.RetryPolicy{MaximumAttempts: 1},
	})
	switch action.Type {
	case common.HookActionModalRefreshVolumes:
		return workflow.ExecuteActivity(ctx, env.ModalRefreshVolumesActivity, env.ModalRefreshVolumesInput{
			EnvContainer: environment,
		}).Get(ctx, nil)
	default:
		return fmt.Errorf("unsupported hook action %q", action.Type)
	}
}
