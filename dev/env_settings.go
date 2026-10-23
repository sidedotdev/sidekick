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
	QueryNameEnv  = "env"
	UpdateNameEnv = "update_env"

	// The Modal-only handlers are registered solely on executions predating
	// update_env, whose histories may hold accepted updates that must replay
	// against them.
	QueryNameModalConfig   = "modal_config"
	UpdateNameModalConfig  = "update_modal_config"
	QueryNamePortForwards  = "port_forwards"
	UpdateNamePortForwards = "update_port_forwards"

	globalStateKeyModalConfig  = "modalConfig"
	globalStateKeyPortForwards = "portForwards"

	// Legacy handlers must remain registered to replay their accepted updates.
	// The unified handlers are available regardless of this version.
	envSettingsHandlersVersionChangeID = "env-settings-handlers"

	// envConfigUpdateVersionChangeID gates routing Modal setting updates
	// through the generic environment configuration activity instead of
	// scheduling sandbox recreation directly.
	envConfigUpdateVersionChangeID = "env-config-update"
)

var errEnvSwitchInProgress = errors.New("an environment switch is already in progress for this flow; wait for it to finish")

// EnvSettings is the full desired environment of a running flow. Any change
// to it moves the flow onto an env matching the settings through the same
// tracked env switch, whether that means another env type, a replacement
// sandbox or an in-place adjustment.
type EnvSettings struct {
	EnvType      string                     `json:"envType"`
	PortForwards []common.PortForwardConfig `json:"portForwards"`
	Modal        common.ModalEnvConfig      `json:"modal"`
}

func (s EnvSettings) Validate() error {
	if !env.EnvType(s.EnvType).IsValid() {
		return fmt.Errorf("invalid environment type %q", s.EnvType)
	}
	if err := s.Modal.Validate(); err != nil {
		return fmt.Errorf("invalid Modal configuration: %w", err)
	}
	return common.ValidatePortForwards(s.PortForwards)
}

// equivalent reports whether applying s to a flow running on current would
// change anything, ignoring cosmetic differences in how settings are spelled.
func (s EnvSettings) equivalent(current EnvSettings) bool {
	return normalizedEnvType(s.EnvType) == normalizedEnvType(current.EnvType) &&
		env.EnvConfig{PortForwards: s.PortForwards, Modal: s.Modal}.Equivalent(
			env.EnvConfig{PortForwards: current.PortForwards, Modal: current.Modal})
}

type EnvUpdateResult struct {
	// FlowActionId identifies the env_switch flow action tracking the change
	// started by the update; empty when the settings already matched.
	FlowActionId string `json:"flowActionId,omitempty"`
	// Settings are those in effect when the update returned, so a started
	// switch is only reflected once its flow action completes.
	Settings EnvSettings `json:"settings"`
}

// PortForwardsConfig is the port mapping section of a live environment's
// configuration, as handled by the legacy port forwards update.
type PortForwardsConfig struct {
	PortForwards []common.PortForwardConfig `json:"portForwards"`
}

// SetupEnvSettingsHandlers exposes the flow's effective env settings and
// lets them be replaced for the lifetime of the current workflow execution,
// including switching to another env type.
func SetupEnvSettingsHandlers(dCtx DevContext) error {
	if dCtx.EnvContainer == nil {
		return nil
	}
	legacyUpdates := &legacyEnvUpdateGate{}
	if workflow.GetVersion(dCtx, envSettingsHandlersVersionChangeID, workflow.DefaultVersion, 1) == workflow.DefaultVersion {
		if err := setupLegacyModalConfigHandlers(dCtx, legacyUpdates); err != nil {
			return err
		}
	}

	dCtx.GlobalState.SetValue(globalStateKeyModalConfig, dCtx.RepoConfig.ModalConfig)
	dCtx.GlobalState.SetValue(globalStateKeyPortForwards, envPortForwards(dCtx.EnvContainer.Env, dCtx.RepoConfig.PortForwards))

	if err := workflow.SetQueryHandler(dCtx, QueryNameEnv, func() (EnvSettings, error) {
		return currentEnvSettings(dCtx), nil
	}); err != nil {
		return fmt.Errorf("failed to register env settings query: %w", err)
	}

	// Config updates and switches are serialized so each one starts from the
	// env the previous one left in effect.
	updateLock := workflow.NewMutex(dCtx)
	state := newFlowEnvState(dCtx.GlobalState)
	if err := workflow.SetUpdateHandlerWithOptions(
		dCtx,
		UpdateNameEnv,
		func(ctx workflow.Context, desired EnvSettings) (EnvUpdateResult, error) {
			if state.SwitchInProgress() {
				return EnvUpdateResult{}, errEnvSwitchInProgress
			}
			desired.EnvType = normalizedEnvType(desired.EnvType)
			requestedFrom := currentEnvType(dCtx)
			if err := updateLock.Lock(ctx); err != nil {
				return EnvUpdateResult{}, err
			}
			legacyUpdates.unifiedActive = true
			defer func() { legacyUpdates.unifiedActive = false }()
			if err := workflow.Await(ctx, func() bool { return legacyUpdates.active == 0 }); err != nil {
				updateLock.Unlock()
				return EnvUpdateResult{}, err
			}
			if err := checkEnvUnchangedSinceRequest(dCtx, state, requestedFrom); err != nil {
				updateLock.Unlock()
				return EnvUpdateResult{}, err
			}
			if current := currentEnvSettings(dCtx); desired.equivalent(current) {
				updateLock.Unlock()
				return EnvUpdateResult{Settings: current}, nil
			}
			return startEnvSwitch(ctx, &dCtx, updateLock, desired)
		},
		workflow.UpdateHandlerOptions{
			// Accepted updates may replace the env or provision a new one,
			// so bad settings must be rejected before that begins.
			Validator: func(_ workflow.Context, desired EnvSettings) error {
				if err := desired.Validate(); err != nil {
					return err
				}
				if state.SwitchInProgress() {
					return errEnvSwitchInProgress
				}
				return nil
			},
		},
	); err != nil {
		return fmt.Errorf("failed to register env settings update: %w", err)
	}
	return nil
}

// checkEnvUnchangedSinceRequest rejects an update that waited behind a switch,
// since its settings were chosen for an env the flow no longer runs on.
func checkEnvUnchangedSinceRequest(dCtx DevContext, state flowEnvState, requestedFrom string) error {
	if state.SwitchInProgress() {
		return errEnvSwitchInProgress
	}
	if current := currentEnvType(dCtx); current != requestedFrom {
		return fmt.Errorf("the flow's environment changed from %s to %s while this update was waiting; retry with the current environment settings", requestedFrom, current)
	}
	return nil
}

// reconcileEnvConfig applies desired to the live env and publishes the
// result only once the env actually reflects it. It serves the legacy
// Modal-only updates; update_env goes through the env switch instead.
func reconcileEnvConfig(ctx workflow.Context, dCtx *DevContext, current, desired env.EnvConfig) (env.EnvConfig, error) {
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
	publishEnvConfig(dCtx, newFlowEnvState(dCtx.GlobalState), output.EnvContainer, output.Config)
	return output.Config, nil
}

// envPortForwards returns the port mappings an env was provisioned with,
// falling back to the configured ones for envs that don't forward ports.
func envPortForwards(e env.Env, fallback []common.PortForwardConfig) []common.PortForwardConfig {
	switch e := e.(type) {
	case *env.ModalEnv:
		return e.PortForwards
	case *env.DevPodEnv:
		return e.PortForwards
	case *env.OpenShellEnv:
		return e.PortForwards
	}
	return fallback
}

func normalizedEnvType(envType string) string {
	normalized, _ := normalizeLegacyEnvType(envType, "")
	return normalized
}

func currentEnvType(dCtx DevContext) string {
	return normalizedEnvType(string(dCtx.EnvContainer.Env.GetType()))
}

func currentEnvSettings(dCtx DevContext) EnvSettings {
	config := currentEnvConfig(dCtx)
	return EnvSettings{
		EnvType:      currentEnvType(dCtx),
		PortForwards: config.PortForwards,
		Modal:        config.Modal,
	}
}

// currentEnvConfig returns the configuration the environment currently
// reflects, as last published by a successful update or switch.
func currentEnvConfig(dCtx DevContext) env.EnvConfig {
	modalConfig, _ := dCtx.GlobalState.GetValue(globalStateKeyModalConfig).(common.ModalEnvConfig)
	portForwards, _ := dCtx.GlobalState.GetValue(globalStateKeyPortForwards).([]common.PortForwardConfig)
	return env.EnvConfig{PortForwards: portForwards, Modal: modalConfig}
}

// setupLegacyModalConfigHandlers registers the Modal-only configuration
// handlers that executions predating update_env were started with. It is a
// no-op for non-Modal environments.
func setupLegacyModalConfigHandlers(dCtx DevContext, gate *legacyEnvUpdateGate) error {
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

	updateLock := workflow.NewMutex(dCtx)
	applyEnvConfig := func(ctx workflow.Context, change func(desired *env.EnvConfig)) (env.EnvConfig, error) {
		if err := updateLock.Lock(ctx); err != nil {
			return env.EnvConfig{}, err
		}
		defer updateLock.Unlock()

		current := currentEnvConfig(dCtx)
		desired := current
		change(&desired)
		return reconcileEnvConfig(ctx, &dCtx, current, desired)
	}

	if err := workflow.SetUpdateHandlerWithOptions(
		dCtx,
		UpdateNameModalConfig,
		func(ctx workflow.Context, config common.ModalEnvConfig) (common.ModalEnvConfig, error) {
			if err := gate.begin(ctx, dCtx); err != nil {
				return common.ModalEnvConfig{}, err
			}
			defer func() { gate.active-- }()
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
			Validator: func(_ workflow.Context, config common.ModalEnvConfig) error {
				if err := requireLegacyModalEnv(dCtx); err != nil {
					return err
				}
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
			if err := gate.begin(ctx, dCtx); err != nil {
				return PortForwardsConfig{}, err
			}
			defer func() { gate.active-- }()
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
				if err := requireLegacyModalEnv(dCtx); err != nil {
					return err
				}
				return common.ValidatePortForwards(config.PortForwards)
			},
		},
	); err != nil {
		return fmt.Errorf("failed to register port forwards update: %w", err)
	}

	return nil
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
	newFlowEnvState(dCtx.GlobalState).publish(dCtx.EnvContainer)
	dCtx.RepoConfig.ModalConfig = config
	dCtx.GlobalState.SetValue(globalStateKeyModalConfig, config)
	return config, nil
}

// Legacy-only concurrency must remain unchanged during replay. Unified updates
// exclude all legacy updates, including recreations outside the legacy mutex.
type legacyEnvUpdateGate struct {
	active        int
	unifiedActive bool
}

func (g *legacyEnvUpdateGate) begin(ctx workflow.Context, dCtx DevContext) error {
	if err := requireLegacyModalEnv(dCtx); err != nil {
		return err
	}
	state := newFlowEnvState(dCtx.GlobalState)
	if err := workflow.Await(ctx, func() bool {
		return !g.unifiedActive && !state.SwitchInProgress()
	}); err != nil {
		return err
	}
	if err := requireLegacyModalEnv(dCtx); err != nil {
		return err
	}
	g.active++
	return nil
}

func requireLegacyModalEnv(dCtx DevContext) error {
	if currentEnvType(dCtx) != string(env.EnvTypeModal) {
		return fmt.Errorf("legacy environment configuration requires a Modal environment; current environment is %s", currentEnvType(dCtx))
	}
	return nil
}
