package dev

import (
	"fmt"
	"sidekick/common"
	"sidekick/env"
	"sidekick/utils"

	"go.temporal.io/sdk/workflow"
)

const (
	QueryNameModalConfig  = "modal_config"
	UpdateNameModalConfig = "update_modal_config"

	globalStateKeyModalConfig = "modalConfig"
)

// SetupModalConfigHandlers exposes the effective Modal environment
// configuration and allows it to be replaced for the lifetime of the current
// workflow execution by recreating the sandbox. It is a no-op for non-Modal
// environments.
func SetupModalConfigHandlers(dCtx DevContext) error {
	if dCtx.EnvContainer == nil {
		return nil
	}
	if _, ok := dCtx.EnvContainer.Env.(*env.ModalEnv); !ok {
		return nil
	}

	dCtx.GlobalState.SetValue(globalStateKeyModalConfig, dCtx.RepoConfig.ModalConfig)

	if err := workflow.SetQueryHandler(dCtx, QueryNameModalConfig, func() (common.ModalEnvConfig, error) {
		value := dCtx.GlobalState.GetValue(globalStateKeyModalConfig)
		config, _ := value.(common.ModalEnvConfig)
		return config, nil
	}); err != nil {
		return fmt.Errorf("failed to register Modal configuration query: %w", err)
	}

	if err := workflow.SetUpdateHandlerWithOptions(
		dCtx,
		UpdateNameModalConfig,
		func(ctx workflow.Context, config common.ModalEnvConfig) (common.ModalEnvConfig, error) {
			var output env.ModalRecreateSandboxOutput
			err := workflow.ExecuteActivity(
				utils.ProvisioningRetryCtx(ctx),
				env.ModalRecreateSandboxActivity,
				env.ModalRecreateSandboxInput{
					EnvContainer: *dCtx.EnvContainer,
					Config:       config,
				},
			).Get(ctx, &output)
			if err != nil {
				return common.ModalEnvConfig{}, err
			}
			*dCtx.EnvContainer = output.EnvContainer
			dCtx.RepoConfig.ModalConfig = config
			dCtx.GlobalState.SetValue(globalStateKeyModalConfig, config)
			return config, nil
		},
		workflow.UpdateHandlerOptions{
			// The accepted update destroys and recreates the sandbox, so bad
			// configurations must be rejected before that destructive
			// sequence begins.
			Validator: func(_ workflow.Context, config common.ModalEnvConfig) error {
				return config.Validate()
			},
		},
	); err != nil {
		return fmt.Errorf("failed to register Modal configuration update: %w", err)
	}

	return nil
}
