package dev

import (
	"errors"
	"fmt"
	"sidekick/common"
	"sidekick/env"
	"sidekick/utils"

	"go.temporal.io/sdk/workflow"
)

const (
	QueryNameModalConfig   = "modal_config"
	UpdateNameModalConfig  = "update_modal_config"
	QueryNamePortForwards  = "port_forwards"
	UpdateNamePortForwards = "update_port_forwards"

	globalStateKeyModalConfig  = "modalConfig"
	globalStateKeyPortForwards = "portForwards"

	// envConfigUpdateVersionChangeID gates routing Modal setting updates
	// through the generic environment configuration activity instead of
	// scheduling sandbox recreation directly.
	envConfigUpdateVersionChangeID = "env-config-update"
)

// PortForwardsConfig is the port mapping section of a live environment's
// configuration, as queried from and applied to a running workflow.
type PortForwardsConfig struct {
	PortForwards []common.PortForwardConfig `json:"portForwards"`
}

// SetupModalConfigHandlers exposes the effective Modal environment
// configuration (sandbox settings and port mappings) and allows either
// section to be replaced for the lifetime of the current workflow execution.
// It is a no-op for non-Modal environments.
func SetupModalConfigHandlers(dCtx DevContext) error {
	if dCtx.EnvContainer == nil {
		return nil
	}
	modalEnv, ok := dCtx.EnvContainer.Env.(*env.ModalEnv)
	if !ok {
		return nil
	}

	dCtx.GlobalState.SetValue(globalStateKeyModalConfig, dCtx.RepoConfig.ModalConfig)
	dCtx.GlobalState.SetValue(globalStateKeyPortForwards, modalEnv.PortForwards)

	if err := workflow.SetQueryHandler(dCtx, QueryNameModalConfig, func() (common.ModalEnvConfig, error) {
		return currentEnvConfig(dCtx).Modal, nil
	}); err != nil {
		return fmt.Errorf("failed to register Modal configuration query: %w", err)
	}
	if err := workflow.SetQueryHandler(dCtx, QueryNamePortForwards, func() (PortForwardsConfig, error) {
		return PortForwardsConfig{PortForwards: currentEnvConfig(dCtx).PortForwards}, nil
	}); err != nil {
		return fmt.Errorf("failed to register port forwards query: %w", err)
	}

	// Updates to either section are serialized so each one reconciles from
	// the configuration the previous one left in effect, and the effective
	// state is only published once the environment actually reflects it.
	updateLock := workflow.NewMutex(dCtx)
	applyEnvConfig := func(ctx workflow.Context, change func(desired *env.EnvConfig)) (env.EnvConfig, error) {
		if err := updateLock.Lock(ctx); err != nil {
			return env.EnvConfig{}, err
		}
		defer updateLock.Unlock()

		current := currentEnvConfig(dCtx)
		desired := current
		change(&desired)

		var output env.UpdateEnvConfigOutput
		err := workflow.ExecuteActivity(
			utils.ProvisioningRetryCtx(ctx),
			env.UpdateEnvConfigActivity,
			env.UpdateEnvConfigInput{
				EnvContainer: *dCtx.EnvContainer,
				Current:      current,
				Desired:      desired,
			},
		).Get(ctx, &output)
		if err != nil {
			return env.EnvConfig{}, err
		}
		*dCtx.EnvContainer = output.EnvContainer
		dCtx.RepoConfig.ModalConfig = output.Config.Modal
		dCtx.RepoConfig.PortForwards = output.Config.PortForwards
		dCtx.GlobalState.SetValue(globalStateKeyModalConfig, output.Config.Modal)
		dCtx.GlobalState.SetValue(globalStateKeyPortForwards, output.Config.PortForwards)
		return output.Config, nil
	}

	if err := workflow.SetUpdateHandlerWithOptions(
		dCtx,
		UpdateNameModalConfig,
		func(ctx workflow.Context, config common.ModalEnvConfig) (common.ModalEnvConfig, error) {
			if workflow.GetVersion(ctx, envConfigUpdateVersionChangeID, workflow.DefaultVersion, 1) == workflow.DefaultVersion {
				return recreateModalSandbox(ctx, dCtx, config)
			}
			applied, err := applyEnvConfig(ctx, func(desired *env.EnvConfig) {
				desired.Modal = config
			})
			if err != nil {
				return common.ModalEnvConfig{}, err
			}
			return applied.Modal, nil
		},
		workflow.UpdateHandlerOptions{
			// The accepted update may destroy and recreate the sandbox, so
			// bad configurations must be rejected before that destructive
			// sequence begins.
			Validator: func(_ workflow.Context, config common.ModalEnvConfig) error {
				return config.Validate()
			},
		},
	); err != nil {
		return fmt.Errorf("failed to register Modal configuration update: %w", err)
	}

	if err := workflow.SetUpdateHandlerWithOptions(
		dCtx,
		UpdateNamePortForwards,
		func(ctx workflow.Context, config PortForwardsConfig) (PortForwardsConfig, error) {
			// An execution pinned to the legacy path recreates its sandbox
			// outside the update lock, so a concurrent port update could
			// publish an environment the recreation just replaced.
			if workflow.GetVersion(ctx, envConfigUpdateVersionChangeID, workflow.DefaultVersion, 1) == workflow.DefaultVersion {
				return PortForwardsConfig{}, errors.New("port forwards cannot be changed on this flow because its sandbox settings were changed before live port mapping updates were supported; start a new flow to change them")
			}
			applied, err := applyEnvConfig(ctx, func(desired *env.EnvConfig) {
				desired.PortForwards = config.PortForwards
			})
			if err != nil {
				return PortForwardsConfig{}, err
			}
			return PortForwardsConfig{PortForwards: applied.PortForwards}, nil
		},
		workflow.UpdateHandlerOptions{
			Validator: func(_ workflow.Context, config PortForwardsConfig) error {
				return common.ValidatePortForwards(config.PortForwards)
			},
		},
	); err != nil {
		return fmt.Errorf("failed to register port forwards update: %w", err)
	}

	return nil
}

// currentEnvConfig returns the configuration the environment currently
// reflects, as last published by a successful update.
func currentEnvConfig(dCtx DevContext) env.EnvConfig {
	modalConfig, _ := dCtx.GlobalState.GetValue(globalStateKeyModalConfig).(common.ModalEnvConfig)
	portForwards, _ := dCtx.GlobalState.GetValue(globalStateKeyPortForwards).([]common.PortForwardConfig)
	return env.EnvConfig{PortForwards: portForwards, Modal: modalConfig}
}

// recreateModalSandbox is the pre-versioning update path, retained so
// executions that already recorded a recreation replay deterministically.
func recreateModalSandbox(ctx workflow.Context, dCtx DevContext, config common.ModalEnvConfig) (common.ModalEnvConfig, error) {
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
}
