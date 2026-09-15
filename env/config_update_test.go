package env

import (
	"context"
	"errors"
	"testing"

	"sidekick/common"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/temporal"
)

type reconcilingTestEnv struct {
	*LocalEnv
	validateErr  error
	reconcileErr error
	reconciled   Env

	validated  []EnvConfig
	reconciles [][2]EnvConfig
}

func (e *reconcilingTestEnv) GetType() EnvType {
	return EnvTypeModal
}

func (e *reconcilingTestEnv) ValidateConfig(desired EnvConfig) error {
	e.validated = append(e.validated, desired)
	return e.validateErr
}

func (e *reconcilingTestEnv) ReconcileConfig(_ context.Context, current, desired EnvConfig) (Env, error) {
	e.reconciles = append(e.reconciles, [2]EnvConfig{current, desired})
	if e.reconcileErr != nil {
		return nil, e.reconcileErr
	}
	if e.reconciled != nil {
		return e.reconciled, nil
	}
	return e, nil
}

func assertNonRetryable(t *testing.T, err error, errType string) {
	t.Helper()
	var appErr *temporal.ApplicationError
	require.ErrorAs(t, err, &appErr)
	assert.True(t, appErr.NonRetryable(), "expected a non-retryable error, got: %v", err)
	assert.Equal(t, errType, appErr.Type())
}

func TestUpdateEnvConfigActivity(t *testing.T) {
	t.Parallel()

	current := EnvConfig{
		PortForwards: []common.PortForwardConfig{{HostPort: 8080}},
		Modal:        common.ModalEnvConfig{CPU: 1},
	}
	desired := EnvConfig{
		PortForwards: []common.PortForwardConfig{{HostPort: 8080}, {HostPort: 5432, ContainerPort: 15432}},
		Modal:        common.ModalEnvConfig{CPU: 2},
	}

	t.Run("rejects environments without the capability", func(t *testing.T) {
		t.Parallel()
		_, err := UpdateEnvConfigActivity(context.Background(), UpdateEnvConfigInput{
			EnvContainer: EnvContainer{Env: &LocalEnv{WorkingDirectory: t.TempDir()}},
			Current:      current,
			Desired:      desired,
		})
		assertNonRetryable(t, err, "UnsupportedEnvConfigUpdate")
		assert.Contains(t, err.Error(), string(EnvTypeLocal))
	})

	t.Run("rejects a missing environment", func(t *testing.T) {
		t.Parallel()
		_, err := UpdateEnvConfigActivity(context.Background(), UpdateEnvConfigInput{Desired: desired})
		assertNonRetryable(t, err, "UnsupportedEnvConfigUpdate")
	})

	t.Run("rejects invalid port forwards before consulting the environment", func(t *testing.T) {
		t.Parallel()
		environment := &reconcilingTestEnv{LocalEnv: &LocalEnv{WorkingDirectory: t.TempDir()}}
		_, err := UpdateEnvConfigActivity(context.Background(), UpdateEnvConfigInput{
			EnvContainer: EnvContainer{Env: environment},
			Current:      current,
			Desired: EnvConfig{
				PortForwards: []common.PortForwardConfig{{HostPort: 8080}, {HostPort: 8080}},
				Modal:        desired.Modal,
			},
		})
		assertNonRetryable(t, err, "InvalidEnvConfig")
		assert.Contains(t, err.Error(), "container_port 8080 is used by both")
		assert.Empty(t, environment.validated)
		assert.Empty(t, environment.reconciles)
	})

	t.Run("rejects configurations the environment refuses", func(t *testing.T) {
		t.Parallel()
		environment := &reconcilingTestEnv{
			LocalEnv:    &LocalEnv{WorkingDirectory: t.TempDir()},
			validateErr: errors.New("cpu_limit below request"),
		}
		_, err := UpdateEnvConfigActivity(context.Background(), UpdateEnvConfigInput{
			EnvContainer: EnvContainer{Env: environment},
			Current:      current,
			Desired:      desired,
		})
		assertNonRetryable(t, err, "InvalidEnvConfig")
		assert.Contains(t, err.Error(), "cpu_limit below request")
		assert.Equal(t, []EnvConfig{desired}, environment.validated)
		assert.Empty(t, environment.reconciles)
	})

	t.Run("surfaces reconciliation failures as retryable", func(t *testing.T) {
		t.Parallel()
		environment := &reconcilingTestEnv{
			LocalEnv:     &LocalEnv{WorkingDirectory: t.TempDir()},
			reconcileErr: errors.New("transport unavailable"),
		}
		_, err := UpdateEnvConfigActivity(context.Background(), UpdateEnvConfigInput{
			EnvContainer: EnvContainer{Env: environment},
			Current:      current,
			Desired:      desired,
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "transport unavailable")
		var appErr *temporal.ApplicationError
		if errors.As(err, &appErr) {
			assert.False(t, appErr.NonRetryable(), "reconciliation failures must stay retryable")
		}
		assert.Equal(t, [][2]EnvConfig{{current, desired}}, environment.reconciles)
	})

	t.Run("returns the reconciled environment and effective configuration", func(t *testing.T) {
		t.Parallel()
		replacement := &ModalEnv{SandboxName: "sb-replacement", WorkingDirectory: "/repo"}
		environment := &reconcilingTestEnv{
			LocalEnv:   &LocalEnv{WorkingDirectory: t.TempDir()},
			reconciled: replacement,
		}
		output, err := UpdateEnvConfigActivity(context.Background(), UpdateEnvConfigInput{
			EnvContainer: EnvContainer{Env: environment},
			Current:      current,
			Desired:      desired,
		})
		require.NoError(t, err)
		assert.Same(t, replacement, output.EnvContainer.Env)
		assert.Equal(t, desired, output.Config)
		assert.Equal(t, []EnvConfig{desired}, environment.validated)
		assert.Equal(t, [][2]EnvConfig{{current, desired}}, environment.reconciles)
	})

	t.Run("keeps the same environment when reconciliation is in place", func(t *testing.T) {
		t.Parallel()
		environment := &reconcilingTestEnv{LocalEnv: &LocalEnv{WorkingDirectory: t.TempDir()}}
		output, err := UpdateEnvConfigActivity(context.Background(), UpdateEnvConfigInput{
			EnvContainer: EnvContainer{Env: environment},
			Current:      current,
			Desired:      EnvConfig{PortForwards: nil, Modal: current.Modal},
		})
		require.NoError(t, err)
		assert.Same(t, environment, output.EnvContainer.Env)
		assert.Empty(t, output.Config.PortForwards)
		assert.Equal(t, current.Modal, output.Config.Modal)
	})
}

func TestSamePortForwards(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		a, b []common.PortForwardConfig
		same bool
	}{
		{name: "both empty", same: true},
		{name: "nil and empty", b: []common.PortForwardConfig{}, same: true},
		{
			name: "order ignored",
			a:    []common.PortForwardConfig{{HostPort: 1}, {HostPort: 2}},
			b:    []common.PortForwardConfig{{HostPort: 2}, {HostPort: 1}},
			same: true,
		},
		{
			name: "explicit default container port",
			a:    []common.PortForwardConfig{{HostPort: 8080}},
			b:    []common.PortForwardConfig{{HostPort: 8080, ContainerPort: 8080}},
			same: true,
		},
		{
			name: "different container port",
			a:    []common.PortForwardConfig{{HostPort: 8080}},
			b:    []common.PortForwardConfig{{HostPort: 8080, ContainerPort: 8081}},
		},
		{
			name: "added mapping",
			a:    []common.PortForwardConfig{{HostPort: 8080}},
			b:    []common.PortForwardConfig{{HostPort: 8080}, {HostPort: 9090}},
		},
		{
			name: "cleared",
			a:    []common.PortForwardConfig{{HostPort: 8080}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.same, samePortForwards(tc.a, tc.b))
			assert.Equal(t, tc.same, samePortForwards(tc.b, tc.a))
		})
	}
}
