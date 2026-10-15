package env

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/temporal"
)

func TestDockerReadinessReportsTypedFailure(t *testing.T) {
	withDockerReadyHooks(t,
		func(context.Context, time.Duration) bool { return false },
		func(context.Context) bool { return true },
		func(context.Context) error { t.Error("must not start a running daemon"); return nil },
		func(context.Context) error { t.Error("must not restart without confirmation"); return nil },
	)
	err := ensureDockerReady(context.Background())
	var appErr *temporal.ApplicationError
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, "DockerEngineUnavailable", appErr.Type())
	assert.True(t, appErr.NonRetryable())
}

func TestEnsureDockerReadyDoesNotRestartByDefault(t *testing.T) {
	for _, processRunning := range []bool{false, true} {
		name := "failed start"
		if processRunning {
			name = "unresponsive running daemon"
		}
		t.Run(name, func(t *testing.T) {
			t.Setenv(autoStartDockerEnvVar, "true")
			var restarts atomic.Int32
			withDockerReadyHooks(t,
				func(context.Context, time.Duration) bool { return false },
				func(context.Context) bool { return processRunning },
				func(context.Context) error { return errors.New("cannot start") },
				func(context.Context) error { restarts.Add(1); return nil },
			)
			assert.Error(t, ensureDockerReady(context.Background()))
			assert.Zero(t, restarts.Load())
		})
	}
}

func TestDockerWatchdogRequiresConfirmation(t *testing.T) {
	withFastDockerHealthTiming(t)
	var restarts atomic.Int32
	withDockerHealthHooks(t,
		func(context.Context, time.Duration) bool { return false },
		func(context.Context) error { restarts.Add(1); return nil },
	)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := withDockerEngineWatchdog(ctx, func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	})
	var appErr *temporal.ApplicationError
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, "DockerEngineUnavailable", appErr.Type())
	assert.True(t, appErr.NonRetryable())
	assert.Zero(t, restarts.Load())
}
