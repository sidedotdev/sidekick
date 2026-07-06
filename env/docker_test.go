package env

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"sidekick/coding/unix"

	"github.com/stretchr/testify/assert"
)

func TestIsOpenShellGatewayUnreachable(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		output   unix.RunCommandActivityOutput
		expected bool
	}{
		{
			name:     "empty output",
			output:   unix.RunCommandActivityOutput{},
			expected: false,
		},
		{
			name:     "unrelated failure",
			output:   unix.RunCommandActivityOutput{Stderr: "image not found"},
			expected: false,
		},
		{
			name:     "gateway not reachable",
			output:   unix.RunCommandActivityOutput{Stderr: "Gateway 'openshell' is not reachable."},
			expected: true,
		},
		{
			name:     "connection refused",
			output:   unix.RunCommandActivityOutput{Stderr: "tcp connect error\nConnection refused (os error 61)"},
			expected: true,
		},
		{
			name:     "transport error in stdout",
			output:   unix.RunCommandActivityOutput{Stdout: "Error: × transport error"},
			expected: true,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.expected, isOpenShellGatewayUnreachable(tc.output))
		})
	}
}

func TestEnvFlagEnabled(t *testing.T) {
	const name = "SIDE_TEST_FLAG"
	cases := []struct {
		value    string
		expected bool
	}{
		{value: "", expected: true},
		{value: "1", expected: true},
		{value: "true", expected: true},
		{value: "anything", expected: true},
		{value: "0", expected: false},
		{value: "false", expected: false},
		{value: "FALSE", expected: false},
		{value: "no", expected: false},
		{value: "off", expected: false},
		{value: "  off  ", expected: false},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.value, func(t *testing.T) {
			t.Setenv(name, tc.value)
			assert.Equal(t, tc.expected, envFlagEnabled(name))
		})
	}
}

// withDockerReadyHooks overrides the indirected docker readiness hooks and
// restores them afterwards.
func withDockerReadyHooks(t *testing.T, responsive func(context.Context, time.Duration) bool, processRunning func(context.Context) bool, start func(context.Context) error, restart func(context.Context) error) {
	t.Helper()
	origCheck := checkDockerEngineResponsive
	origProc := dockerProcessRunning
	origStart := startDocker
	origRestart := restartDockerEngineFunc
	checkDockerEngineResponsive = responsive
	dockerProcessRunning = processRunning
	startDocker = start
	restartDockerEngineFunc = restart
	t.Cleanup(func() {
		checkDockerEngineResponsive = origCheck
		dockerProcessRunning = origProc
		startDocker = origStart
		restartDockerEngineFunc = origRestart
	})
}

func TestEnsureDockerReady(t *testing.T) {
	t.Run("responsive does nothing", func(t *testing.T) {
		var starts, restarts atomic.Int32
		withDockerReadyHooks(t,
			func(context.Context, time.Duration) bool { return true },
			func(context.Context) bool { return true },
			func(context.Context) error { starts.Add(1); return nil },
			func(context.Context) error { restarts.Add(1); return nil },
		)
		assert.NoError(t, ensureDockerReady(context.Background()))
		assert.Equal(t, int32(0), starts.Load())
		assert.Equal(t, int32(0), restarts.Load())
	})

	t.Run("stopped daemon is started, not restarted", func(t *testing.T) {
		var starts, restarts atomic.Int32
		var responsive atomic.Bool
		withDockerReadyHooks(t,
			func(context.Context, time.Duration) bool { return responsive.Load() },
			func(context.Context) bool { return false },
			func(context.Context) error { starts.Add(1); responsive.Store(true); return nil },
			func(context.Context) error { restarts.Add(1); return nil },
		)
		assert.NoError(t, ensureDockerReady(context.Background()))
		assert.Equal(t, int32(1), starts.Load())
		assert.Equal(t, int32(0), restarts.Load(), "a stopped daemon must not be restarted")
	})

	t.Run("wedged daemon is restarted, not started", func(t *testing.T) {
		var starts, restarts atomic.Int32
		withDockerReadyHooks(t,
			func(context.Context, time.Duration) bool { return false },
			func(context.Context) bool { return true },
			func(context.Context) error { starts.Add(1); return nil },
			func(context.Context) error { restarts.Add(1); return nil },
		)
		assert.NoError(t, ensureDockerReady(context.Background()))
		assert.Equal(t, int32(0), starts.Load(), "a wedged daemon must not be 'started'")
		assert.Equal(t, int32(1), restarts.Load())
	})

	t.Run("start disabled falls back to restart", func(t *testing.T) {
		t.Setenv(autoStartDockerEnvVar, "false")
		var starts, restarts atomic.Int32
		withDockerReadyHooks(t,
			func(context.Context, time.Duration) bool { return false },
			func(context.Context) bool { return false },
			func(context.Context) error { starts.Add(1); return nil },
			func(context.Context) error { restarts.Add(1); return nil },
		)
		assert.NoError(t, ensureDockerReady(context.Background()))
		assert.Equal(t, int32(0), starts.Load())
		assert.Equal(t, int32(1), restarts.Load())
	})

	t.Run("both disabled returns error", func(t *testing.T) {
		t.Setenv(autoStartDockerEnvVar, "false")
		t.Setenv(autoRestartDockerEnvVar, "false")
		var starts, restarts atomic.Int32
		withDockerReadyHooks(t,
			func(context.Context, time.Duration) bool { return false },
			func(context.Context) bool { return true },
			func(context.Context) error { starts.Add(1); return nil },
			func(context.Context) error { restarts.Add(1); return nil },
		)
		err := ensureDockerReady(context.Background())
		assert.Error(t, err)
		assert.Equal(t, int32(0), starts.Load())
		assert.Equal(t, int32(0), restarts.Load())
	})

	t.Run("restart failure surfaced", func(t *testing.T) {
		restartErr := errors.New("no restart strategy worked")
		withDockerReadyHooks(t,
			func(context.Context, time.Duration) bool { return false },
			func(context.Context) bool { return true },
			func(context.Context) error { return nil },
			func(context.Context) error { return restartErr },
		)
		assert.ErrorIs(t, ensureDockerReady(context.Background()), restartErr)
	})
}
