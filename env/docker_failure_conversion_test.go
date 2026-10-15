package env

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/temporal"
)

func TestAsDockerEngineFailureAfterConversion(t *testing.T) {
	t.Parallel()
	details := DockerEngineFailureDetails{Host: "worker-host"}
	dockerErr := temporal.NewNonRetryableApplicationError(
		"Docker is unavailable", ErrTypeDockerEngineUnavailable, nil, details)
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{name: "direct", err: dockerErr, want: true},
		{
			name: "wrapped preflight",
			err:  fmt.Errorf("docker engine not ready for devpod up: %w", dockerErr),
			want: true,
		},
		{
			name: "nested application errors",
			err: temporal.NewApplicationErrorWithCause("provisioning failed", "ProvisioningFailure",
				fmt.Errorf("docker engine not ready for devpod up: %w", dockerErr)),
			want: true,
		},
		{name: "ordinary error", err: errors.New("Docker is unavailable")},
		{
			name: "unrelated application error",
			err:  temporal.NewNonRetryableApplicationError("failed", "OtherFailure", nil, details),
		},
		{
			name: "missing details",
			err:  temporal.NewNonRetryableApplicationError("failed", ErrTypeDockerEngineUnavailable, nil),
		},
		{
			name: "invalid details",
			err:  temporal.NewNonRetryableApplicationError("failed", ErrTypeDockerEngineUnavailable, nil, "invalid"),
		},
		{
			name: "missing host",
			err: temporal.NewNonRetryableApplicationError("failed", ErrTypeDockerEngineUnavailable,
				nil, DockerEngineFailureDetails{}),
		},
		{name: "nil"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			converter := temporal.GetDefaultFailureConverter()
			roundTripped := converter.FailureToError(converter.ErrorToFailure(tc.err))
			got, ok := AsDockerEngineFailure(roundTripped)
			require.Equal(t, tc.want, ok)
			if tc.want {
				assert.Equal(t, details, got)
			}
		})
	}
}

func TestDevPodDockerFailureConversion(t *testing.T) {
	for _, tc := range []struct {
		name      string
		preflight bool
		up        bool
		run       func(context.Context) error
	}{
		{
			name: "up preflight", preflight: true, up: true,
			run: func(ctx context.Context) error {
				return DevPodUpActivity(ctx, DevPodUpInput{WorkspacePath: "."})
			},
		},
		{
			name: "up watchdog", up: true,
			run: func(ctx context.Context) error {
				return DevPodUpActivity(ctx, DevPodUpInput{WorkspacePath: "."})
			},
		},
		{
			name: "stop watchdog",
			run: func(ctx context.Context) error {
				return devPodStopWorkspace(ctx, "docker-failure-conversion-test")
			},
		},
		{
			name: "delete watchdog",
			run: func(ctx context.Context) error {
				return devPodDeleteWorkspace(ctx, "docker-failure-conversion-test")
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			binDir := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(binDir, "devpod"),
				[]byte("#!/bin/sh\nexec sleep 30\n"), 0o755))
			t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
			withFastDockerHealthTiming(t)
			var probes atomic.Int32
			withDockerReadyHooks(t,
				func(context.Context, time.Duration) bool {
					return tc.up && !tc.preflight && probes.Add(1) == 1
				},
				func(context.Context) bool { return true },
				func(context.Context) error { t.Error("must not start Docker"); return nil },
				func(context.Context) error { t.Error("must not restart Docker"); return nil },
			)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			err := tc.run(ctx)
			require.Error(t, err)

			converter := temporal.GetDefaultFailureConverter()
			failure := converter.ErrorToFailure(err)
			info := failure.GetApplicationFailureInfo()
			require.NotNil(t, info)
			assert.Equal(t, ErrTypeDockerEngineUnavailable, info.GetType())
			assert.True(t, info.GetNonRetryable())

			got, ok := AsDockerEngineFailure(converter.FailureToError(failure))
			require.True(t, ok)
			host, err := os.Hostname()
			require.NoError(t, err)
			assert.Equal(t, host, got.Host)
		})
	}
}

func TestAsDockerEngineFailureJoinedCauses(t *testing.T) {
	t.Parallel()
	details := DockerEngineFailureDetails{Host: "worker-host"}
	err := errors.Join(
		temporal.NewApplicationError("unrelated failure", "OtherFailure"),
		fmt.Errorf("preflight failed: %w", temporal.NewNonRetryableApplicationError(
			"Docker is unavailable", ErrTypeDockerEngineUnavailable, nil, details)),
	)
	got, ok := AsDockerEngineFailure(err)
	require.True(t, ok)
	assert.Equal(t, details, got)
}

type dockerFailureCommandEnv struct {
	Env
	calls      int
	failOnCall int
	err        error
}

func (e *dockerFailureCommandEnv) RunCommand(context.Context, EnvRunCommandInput) (EnvRunCommandOutput, error) {
	e.calls++
	if e.calls == e.failOnCall {
		return EnvRunCommandOutput{}, e.err
	}
	return EnvRunCommandOutput{}, nil
}

func TestCreateDevPodWorktreeFailureConversion(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name       string
		failOnCall int
		message    string
	}{
		{name: "prepare directory", failOnCall: 1, message: "failed to prepare worktree directory in container"},
		{name: "add worktree", failOnCall: 2, message: "failed to run git worktree add in container"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			for _, dockerFailure := range []bool{false, true} {
				name := "ordinary error"
				cause := errors.New("command failed")
				if dockerFailure {
					name = "Docker failure"
					cause = temporal.NewNonRetryableApplicationError("Docker is unavailable",
						ErrTypeDockerEngineUnavailable, nil, DockerEngineFailureDetails{Host: "worker-host"})
				}
				t.Run(name, func(t *testing.T) {
					t.Parallel()
					e := &dockerFailureCommandEnv{failOnCall: tc.failOnCall, err: cause}
					_, err := CreateDevPodWorktreeActivity(context.Background(), CreateDevPodWorktreeInput{
						EnvContainer: EnvContainer{Env: e},
						RepoDir:      "/workspaces/repo",
						BranchName:   "side/test",
						WorkspaceId:  "ws_test",
					})
					require.ErrorIs(t, err, cause)
					assert.Contains(t, err.Error(), tc.message)
					assert.Equal(t, tc.failOnCall, e.calls)

					converter := temporal.GetDefaultFailureConverter()
					failure := converter.ErrorToFailure(err)
					info := failure.GetApplicationFailureInfo()
					require.NotNil(t, info)
					assert.Equal(t, dockerFailure, info.GetNonRetryable())
					details, ok := AsDockerEngineFailure(converter.FailureToError(failure))
					assert.Equal(t, dockerFailure, ok)
					if dockerFailure {
						assert.Equal(t, ErrTypeDockerEngineUnavailable, info.GetType())
						assert.Equal(t, "worker-host", details.Host)
					}
				})
			}
		})
	}
}

func TestDevPodUpCancellationIncludesOutput(t *testing.T) {
	binDir := t.TempDir()
	markerPath := filepath.Join(binDir, "started")
	require.NoError(t, os.WriteFile(filepath.Join(binDir, "devpod"), []byte(`#!/bin/sh
printf 'starting devcontainer\n'
printf 'waiting for image build\n' >&2
: > "$DEVPOD_TEST_START_MARKER"
exec sleep 30
`), 0o755))
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("DEVPOD_TEST_START_MARKER", markerPath)
	withDockerReadyHooks(t,
		func(context.Context, time.Duration) bool { return true },
		func(context.Context) bool { return true },
		func(context.Context) error { t.Error("must not start Docker"); return nil },
		func(context.Context) error { t.Error("must not restart Docker"); return nil },
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		result <- DevPodUpActivity(ctx, DevPodUpInput{WorkspacePath: "."})
	}()
	require.Eventually(t, func() bool {
		_, err := os.Stat(markerPath)
		return err == nil
	}, 5*time.Second, 10*time.Millisecond)
	cancel()

	select {
	case err := <-result:
		require.ErrorIs(t, err, context.Canceled)
		assert.Contains(t, err.Error(), "starting devcontainer")
		assert.Contains(t, err.Error(), "waiting for image build")
		_, dockerFailure := AsDockerEngineFailure(err)
		assert.False(t, dockerFailure)
	case <-time.After(5 * time.Second):
		t.Fatal("DevPodUpActivity did not return after cancellation")
	}
}
