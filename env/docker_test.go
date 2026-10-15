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
	for _, tc := range []struct {
		name         string
		responsive   bool
		processAlive bool
		autoStart    string
		startErr     error
		wantStarts   int32
		wantErr      bool
	}{
		{name: "responsive", responsive: true, processAlive: true, autoStart: "true"},
		{name: "stopped", autoStart: "true", wantStarts: 1},
		{name: "wedged", processAlive: true, autoStart: "true", wantErr: true},
		{name: "start disabled", autoStart: "false", wantErr: true},
		{name: "failed start", autoStart: "true", startErr: errors.New("start failed"), wantStarts: 1, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(autoStartDockerEnvVar, tc.autoStart)
			var starts, restarts atomic.Int32
			var responsive atomic.Bool
			responsive.Store(tc.responsive)
			withDockerReadyHooks(t,
				func(context.Context, time.Duration) bool { return responsive.Load() },
				func(context.Context) bool { return tc.processAlive },
				func(context.Context) error {
					starts.Add(1)
					if tc.startErr == nil {
						responsive.Store(true)
					}
					return tc.startErr
				},
				func(context.Context) error { restarts.Add(1); return nil },
			)
			err := ensureDockerReady(context.Background())
			if tc.wantErr {
				_, ok := AsDockerEngineFailure(err)
				assert.True(t, ok)
				if tc.startErr != nil {
					assert.ErrorIs(t, err, tc.startErr)
				}
			} else {
				assert.NoError(t, err)
			}
			assert.Equal(t, tc.wantStarts, starts.Load())
			assert.Zero(t, restarts.Load())
		})
	}
}
