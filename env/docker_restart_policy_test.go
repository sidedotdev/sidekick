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

func TestDockerWatchdogRequiresExplicitRestart(t *testing.T) {
	for _, setting := range []string{"", "true", "false"} {
		t.Run("legacy auto-restart="+setting, func(t *testing.T) {
			t.Setenv("SIDE_AUTO_RESTART_DOCKER", setting)
			withFastDockerHealthTiming(t)
			var restarts atomic.Int32
			withDockerHealthHooks(t,
				func(context.Context, time.Duration) bool { return false },
				func(context.Context) error {
					restarts.Add(1)
					return nil
				},
			)

			ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
			defer cancel()
			commandErr := errors.New("command interrupted")
			err := withDockerEngineWatchdog(ctx, func(operationCtx context.Context) error {
				<-operationCtx.Done()
				return commandErr
			})

			assert.NoError(t, ctx.Err(), "the watchdog should cancel the operation before its parent deadline")
			assert.Zero(t, restarts.Load(), "detecting an unavailable engine must not authorize a restart")
			assert.ErrorIs(t, err, commandErr)
			var appErr *temporal.ApplicationError
			require.ErrorAs(t, err, &appErr)
			assert.Equal(t, "DockerEngineUnavailable", appErr.Type())
			assert.True(t, appErr.NonRetryable())
		})
	}
}

func TestDockerReadinessRequiresExplicitRestart(t *testing.T) {
	for _, tc := range []struct {
		name           string
		processRunning bool
		autoStart      string
	}{
		{name: "unresponsive running daemon", processRunning: true, autoStart: "true"},
		{name: "stopped daemon with auto-start disabled", autoStart: "false"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("SIDE_AUTO_RESTART_DOCKER", "true")
			t.Setenv(autoStartDockerEnvVar, tc.autoStart)
			var starts, restarts atomic.Int32
			withDockerReadyHooks(t,
				func(context.Context, time.Duration) bool { return false },
				func(context.Context) bool { return tc.processRunning },
				func(context.Context) error {
					starts.Add(1)
					return nil
				},
				func(context.Context) error {
					restarts.Add(1)
					return nil
				},
			)

			err := ensureDockerReady(context.Background())

			assert.Zero(t, starts.Load())
			assert.Zero(t, restarts.Load())
			var appErr *temporal.ApplicationError
			require.ErrorAs(t, err, &appErr)
			assert.Equal(t, "DockerEngineUnavailable", appErr.Type())
			assert.True(t, appErr.NonRetryable())
		})
	}
}
