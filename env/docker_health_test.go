package env

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// withFastDockerHealthTiming shrinks the watchdog timings so tests run quickly
// and restores the originals afterwards.
func withFastDockerHealthTiming(t *testing.T) {
	t.Helper()
	origInterval := dockerHealthCheckInterval
	origTimeout := dockerHealthCheckTimeout
	origThreshold := dockerHealthUnresponsiveThreshold
	dockerHealthCheckInterval = time.Millisecond
	dockerHealthCheckTimeout = 10 * time.Millisecond
	dockerHealthUnresponsiveThreshold = 2
	t.Cleanup(func() {
		dockerHealthCheckInterval = origInterval
		dockerHealthCheckTimeout = origTimeout
		dockerHealthUnresponsiveThreshold = origThreshold
	})
}

// withFastDockerReadyTiming shrinks the post-restart readiness poll timings so
// tests exercising waitForDockerEngine/RestartDockerEngine run quickly.
func withFastDockerReadyTiming(t *testing.T) {
	t.Helper()
	origTimeout := dockerReadyPollTimeout
	origInterval := dockerReadyPollInterval
	origCheckTimeout := dockerHealthCheckTimeout
	dockerReadyPollTimeout = 50 * time.Millisecond
	dockerReadyPollInterval = time.Millisecond
	dockerHealthCheckTimeout = 5 * time.Millisecond
	t.Cleanup(func() {
		dockerReadyPollTimeout = origTimeout
		dockerReadyPollInterval = origInterval
		dockerHealthCheckTimeout = origCheckTimeout
	})
}

// withDockerHealthHooks overrides the indirected health check and restart hooks
// and restores them afterwards.
func withDockerHealthHooks(t *testing.T, check func(context.Context, time.Duration) bool, restart func(context.Context) error) {
	t.Helper()
	origCheck := checkDockerEngineResponsive
	origRestart := restartDockerEngineFunc
	checkDockerEngineResponsive = check
	restartDockerEngineFunc = restart
	t.Cleanup(func() {
		checkDockerEngineResponsive = origCheck
		restartDockerEngineFunc = origRestart
	})
}

func TestWaitForDockerEngine_UsesIndirectedHook(t *testing.T) {
	t.Run("returns true once the hook reports responsive", func(t *testing.T) {
		withFastDockerReadyTiming(t)
		var probes atomic.Int32
		origCheck := checkDockerEngineResponsive
		checkDockerEngineResponsive = func(context.Context, time.Duration) bool {
			// Become responsive only after a few polls to prove we loop on the hook.
			return probes.Add(1) >= 3
		}
		t.Cleanup(func() { checkDockerEngineResponsive = origCheck })

		assert.True(t, waitForDockerEngine(context.Background(), dockerReadyPollTimeout))
		assert.GreaterOrEqual(t, probes.Load(), int32(3))
	})

	t.Run("returns false when the hook never reports responsive", func(t *testing.T) {
		withFastDockerReadyTiming(t)
		var probes atomic.Int32
		origCheck := checkDockerEngineResponsive
		checkDockerEngineResponsive = func(context.Context, time.Duration) bool {
			probes.Add(1)
			return false
		}
		t.Cleanup(func() { checkDockerEngineResponsive = origCheck })

		assert.False(t, waitForDockerEngine(context.Background(), dockerReadyPollTimeout))
		assert.Positive(t, probes.Load(), "the readiness hook must be polled")
	})
}

// withNoopDockerRestartStrategy replaces the real restart strategies with a
// single no-op so tests never launch or kill a real docker engine. The provided
// counter is incremented each time the strategy runs.
func withNoopDockerRestartStrategy(t *testing.T, ran *atomic.Int32) {
	t.Helper()
	orig := dockerRestartStrategiesFunc
	dockerRestartStrategiesFunc = func() []dockerRestartStrategy {
		return []dockerRestartStrategy{{
			name: "noop-test-strategy",
			run:  func(context.Context) error { ran.Add(1); return nil },
		}}
	}
	t.Cleanup(func() { dockerRestartStrategiesFunc = orig })
}

func TestRestartDockerEngine_RequiresPostRestartProbe(t *testing.T) {
	t.Run("strategy succeeding but engine staying unresponsive fails", func(t *testing.T) {
		withFastDockerReadyTiming(t)
		var ran atomic.Int32
		withNoopDockerRestartStrategy(t, &ran)
		origCheck := checkDockerEngineResponsive
		// The strategy's command succeeds; the post-restart probe is the sole
		// signal of success, so a probe that never reports responsive must fail.
		checkDockerEngineResponsive = func(context.Context, time.Duration) bool { return false }
		t.Cleanup(func() { checkDockerEngineResponsive = origCheck })

		err := RestartDockerEngine(context.Background())
		assert.Error(t, err, "restart must fail when the post-restart probe never reports responsive")
		assert.Positive(t, ran.Load(), "the restart strategy should have been attempted")
	})

	t.Run("strategy succeeding and engine becoming responsive succeeds", func(t *testing.T) {
		withFastDockerReadyTiming(t)
		var ran atomic.Int32
		withNoopDockerRestartStrategy(t, &ran)
		origCheck := checkDockerEngineResponsive
		// Report responsive only after the strategy runs, proving success is
		// gated on the post-restart probe rather than the strategy's own result.
		checkDockerEngineResponsive = func(context.Context, time.Duration) bool { return ran.Load() > 0 }
		t.Cleanup(func() { checkDockerEngineResponsive = origCheck })

		assert.NoError(t, RestartDockerEngine(context.Background()))
		assert.Positive(t, ran.Load())
	})
}

func TestWithDockerEngineWatchdog_ResponsiveReturnsResult(t *testing.T) {
	withFastDockerHealthTiming(t)
	var restarts atomic.Int32
	withDockerHealthHooks(t,
		func(context.Context, time.Duration) bool { return true },
		func(context.Context) error { restarts.Add(1); return nil },
	)

	sentinel := errors.New("fn result")
	err := withDockerEngineWatchdog(context.Background(), func(ctx context.Context) error {
		return sentinel
	})

	assert.ErrorIs(t, err, sentinel)
	assert.Equal(t, int32(0), restarts.Load(), "engine should not be restarted while responsive")
}

func TestWithDockerEngineWatchdog_UnresponsiveRestartsAndCancels(t *testing.T) {
	withFastDockerHealthTiming(t)
	var restarts atomic.Int32
	withDockerHealthHooks(t,
		func(context.Context, time.Duration) bool { return false },
		func(context.Context) error { restarts.Add(1); return nil },
	)

	err := withDockerEngineWatchdog(context.Background(), func(ctx context.Context) error {
		<-ctx.Done() // simulate an operation hung on the engine
		return ctx.Err()
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "must be retried")
	assert.Equal(t, int32(1), restarts.Load())
}

func TestWithDockerEngineWatchdog_RestartFailureSurfaced(t *testing.T) {
	withFastDockerHealthTiming(t)
	restartErr := errors.New("no restart strategy worked")
	withDockerHealthHooks(t,
		func(context.Context, time.Duration) bool { return false },
		func(context.Context) error { return restartErr },
	)

	err := withDockerEngineWatchdog(context.Background(), func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	})

	require.Error(t, err)
	assert.ErrorIs(t, err, restartErr)
}

func TestWithDockerEngineWatchdog_DisabledRunsWithoutMonitoring(t *testing.T) {
	withFastDockerHealthTiming(t)
	t.Setenv(autoRestartDockerEnvVar, "false")
	var restarts atomic.Int32
	withDockerHealthHooks(t,
		func(context.Context, time.Duration) bool { return false },
		func(context.Context) error { restarts.Add(1); return nil },
	)

	sentinel := errors.New("fn result")
	err := withDockerEngineWatchdog(context.Background(), func(ctx context.Context) error {
		return sentinel
	})

	assert.ErrorIs(t, err, sentinel)
	assert.Equal(t, int32(0), restarts.Load(), "watchdog must not restart when auto-restart is disabled")
}
