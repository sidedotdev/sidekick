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
	envType := string(environment.Env.GetType())
	var actions []matchedHookAction
	for i, rule := range config.RunTests {
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
					location: fmt.Sprintf("hooks.run_tests[%d].actions[%d]", i, j),
					config:   action,
				})
			}
		}
	}
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
