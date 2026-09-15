package env

import (
	"cmp"
	"context"
	"fmt"
	"slices"

	"sidekick/common"

	"go.temporal.io/sdk/temporal"
)

// samePortForwards reports whether two mapping lists bind the same reverse
// forwards, ignoring order and whether a container port was spelled out or
// left to default to the host port, so that a cosmetic rewrite of the list
// does not disrupt live listeners.
func samePortForwards(a, b []common.PortForwardConfig) bool {
	normalize := func(forwards []common.PortForwardConfig) []common.PortForwardConfig {
		out := make([]common.PortForwardConfig, 0, len(forwards))
		for _, forward := range forwards {
			out = append(out, common.PortForwardConfig{
				HostPort:      forward.HostPort,
				ContainerPort: forward.ContainerPortOrDefault(),
			})
		}
		slices.SortFunc(out, func(x, y common.PortForwardConfig) int {
			return cmp.Or(
				cmp.Compare(x.HostPort, y.HostPort),
				cmp.Compare(x.ContainerPort, y.ContainerPort),
			)
		})
		return out
	}
	return slices.Equal(normalize(a), normalize(b))
}

// EnvConfig is the slice of repository configuration that can be changed on a
// live environment without restarting the workflow that owns it.
type EnvConfig struct {
	PortForwards []common.PortForwardConfig `json:"portForwards,omitempty"`
	Modal        common.ModalEnvConfig      `json:"modal,omitempty"`
}

// ConfigReconcilingEnv is implemented by environments that can move from one
// live configuration to another. Implementations decide how disruptive the
// transition needs to be (no-op, in-place adjustment or full recreation) and
// return the environment that reflects the desired configuration.
type ConfigReconcilingEnv interface {
	Env
	// ValidateConfig rejects configurations the environment cannot apply, so
	// callers can fail before any disruptive reconciliation begins.
	ValidateConfig(desired EnvConfig) error
	// ReconcileConfig transitions the environment from current to desired.
	// The returned Env may be the receiver when nothing about the underlying
	// environment identity changed.
	ReconcileConfig(ctx context.Context, current, desired EnvConfig) (Env, error)
}

type UpdateEnvConfigInput struct {
	EnvContainer EnvContainer `json:"envContainer"`
	Current      EnvConfig    `json:"current"`
	Desired      EnvConfig    `json:"desired"`
}

type UpdateEnvConfigOutput struct {
	// EnvContainer holds the environment that now reflects Config.
	EnvContainer EnvContainer `json:"envContainer"`
	// Config is the configuration in effect after the update.
	Config EnvConfig `json:"config"`
}

// UpdateEnvConfigActivity applies a configuration change to a live
// environment by delegating to its ConfigReconcilingEnv capability. Shared
// validation runs first so that no provider is asked to reconcile a
// configuration that could never work; validation failures and unsupported
// environments are not retryable.
func UpdateEnvConfigActivity(ctx context.Context, input UpdateEnvConfigInput) (UpdateEnvConfigOutput, error) {
	reconciler, ok := input.EnvContainer.Env.(ConfigReconcilingEnv)
	if !ok {
		envType := EnvType("unknown")
		if input.EnvContainer.Env != nil {
			envType = input.EnvContainer.Env.GetType()
		}
		return UpdateEnvConfigOutput{}, temporal.NewNonRetryableApplicationError(
			fmt.Sprintf("environment type %q does not support live configuration updates", envType),
			"UnsupportedEnvConfigUpdate", nil)
	}

	if err := common.ValidatePortForwards(input.Desired.PortForwards); err != nil {
		return UpdateEnvConfigOutput{}, temporal.NewNonRetryableApplicationError(
			"invalid port forwards", "InvalidEnvConfig", err)
	}
	if err := reconciler.ValidateConfig(input.Desired); err != nil {
		return UpdateEnvConfigOutput{}, temporal.NewNonRetryableApplicationError(
			"invalid environment configuration", "InvalidEnvConfig", err)
	}

	updated, err := reconciler.ReconcileConfig(ctx, input.Current, input.Desired)
	if err != nil {
		return UpdateEnvConfigOutput{}, fmt.Errorf("failed to apply environment configuration: %w", err)
	}
	if updated == nil {
		return UpdateEnvConfigOutput{}, temporal.NewNonRetryableApplicationError(
			"environment reconciliation returned no environment", "EnvConfigReconcileBug", nil)
	}

	return UpdateEnvConfigOutput{
		EnvContainer: EnvContainer{Env: updated},
		Config:       input.Desired,
	}, nil
}
