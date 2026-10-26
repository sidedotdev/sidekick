package env

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestModalSnapshotUsesSandboxProcessEnvironment(t *testing.T) {
	t.Parallel()

	apiErr := errors.New("snapshot exec unavailable")
	for _, tt := range []struct {
		name   string
		output EnvRunCommandOutput
		err    error
	}{
		{
			name:   "snapshot confirmed",
			output: EnvRunCommandOutput{Stdout: "guard snapshot request confirmed: status=201"},
		},
		{
			name:   "guard rejects snapshot",
			output: EnvRunCommandOutput{ExitStatus: 1, Stderr: "guard snapshot HTTP failure"},
		},
		{
			name: "exec fails",
			err:  apiErr,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			sshCalls, apiCalls := 0, 0
			ctx := context.Background()
			e := &ModalEnv{
				SandboxName: "snapshot-sandbox",
				runModalCommand: func(context.Context, EnvRunCommandInput) (EnvRunCommandOutput, string, error) {
					sshCalls++
					return EnvRunCommandOutput{
						ExitStatus: 2,
						Stdout:     "guard snapshot missing required env: SIDE_GUARD_URL SIDE_GUARD_TOKEN SIDE_SANDBOX_NAME",
					}, "", nil
				},
				runModalAPICommand: func(gotCtx context.Context, input EnvRunCommandInput) (EnvRunCommandOutput, error) {
					apiCalls++
					assert.Equal(t, ctx, gotCtx)
					assert.Equal(t, "/usr/local/bin/sidekick-snapshot", input.Command)
					return tt.output, tt.err
				},
			}

			output, err := e.Snapshot(ctx)
			if tt.err != nil {
				require.ErrorIs(t, err, tt.err)
			} else {
				require.NoError(t, err)
			}
			assert.Equal(t, tt.output, output)
			assert.Zero(t, sshCalls)
			assert.Equal(t, 1, apiCalls)
		})
	}
}

func TestModalSnapshotRecoversStoppedSandbox(t *testing.T) {
	t.Parallel()

	apiErr := errors.New("snapshot exec unavailable")
	refreshErr := errors.New("no snapshot to restore")
	stoppedErr := errors.Join(errors.New("snapshot sandbox"), errModalSandboxNotRunning)
	for _, tt := range []struct {
		name          string
		stoppedCalls  int
		commandErr    error
		refreshErr    error
		output        EnvRunCommandOutput
		cancel        bool
		wantErr       error
		wantCalls     int
		wantRefreshes int
	}{
		{
			name:          "stopped sandbox",
			stoppedCalls:  1,
			output:        EnvRunCommandOutput{Stdout: "snapshot confirmed"},
			wantCalls:     2,
			wantRefreshes: 1,
		},
		{
			name:          "stops again after restoration",
			stoppedCalls:  2,
			wantCalls:     3,
			wantRefreshes: 2,
		},
		{
			name:          "bounded recovery",
			stoppedCalls:  100,
			wantErr:       errModalSandboxNotRunning,
			wantCalls:     3,
			wantRefreshes: 2,
		},
		{
			name:          "restore fails",
			stoppedCalls:  1,
			refreshErr:    refreshErr,
			wantErr:       refreshErr,
			wantCalls:     1,
			wantRefreshes: 1,
		},
		{
			name:       "unrelated API error",
			commandErr: apiErr,
			wantErr:    apiErr,
			wantCalls:  1,
		},
		{
			name:      "snapshot command failure",
			output:    EnvRunCommandOutput{ExitStatus: 1, Stderr: "guard rejected snapshot"},
			wantCalls: 1,
		},
		{
			name:          "API error after restoration",
			stoppedCalls:  1,
			commandErr:    apiErr,
			wantErr:       apiErr,
			wantCalls:     2,
			wantRefreshes: 1,
		},
		{
			name:         "canceled before recovery",
			stoppedCalls: 1,
			cancel:       true,
			wantErr:      context.Canceled,
			wantCalls:    1,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls, refreshes := 0, 0
			e := &ModalEnv{
				SandboxName: "snapshot-sandbox",
				SSHHost:     "old-host",
				SSHPort:     1234,
				runModalAPICommand: func(gotCtx context.Context, input EnvRunCommandInput) (EnvRunCommandOutput, error) {
					calls++
					assert.Equal(t, ctx, gotCtx)
					assert.Equal(t, "/usr/local/bin/sidekick-snapshot", input.Command)
					if tt.cancel {
						cancel()
					}
					if calls <= tt.stoppedCalls {
						return EnvRunCommandOutput{}, stoppedErr
					}
					return tt.output, tt.commandErr
				},
				refreshModalEndpoint: func(gotCtx context.Context, name string) (string, int, error) {
					refreshes++
					assert.Equal(t, ctx, gotCtx)
					assert.Equal(t, "snapshot-sandbox", name)
					return "restored-host", 5678, tt.refreshErr
				},
			}

			output, err := e.Snapshot(ctx)
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.output, output)
			}
			assert.Equal(t, tt.wantCalls, calls)
			assert.Equal(t, tt.wantRefreshes, refreshes)
			if refreshes > 0 && tt.refreshErr == nil {
				assert.Equal(t, "restored-host", e.SSHHost)
				assert.Equal(t, 5678, e.SSHPort)
			} else {
				assert.Equal(t, "old-host", e.SSHHost)
				assert.Equal(t, 1234, e.SSHPort)
			}
		})
	}
}

func TestModalSnapshotReportsRestore(t *testing.T) {
	original := refreshModalEndpointDetailed
	t.Cleanup(func() { refreshModalEndpointDetailed = original })

	apiErr := errors.New("snapshot exec unavailable")
	for _, tt := range []struct {
		name string
		err  error
	}{
		{name: "snapshot succeeds"},
		{name: "snapshot fails after restore", err: apiErr},
	} {
		t.Run(tt.name, func(t *testing.T) {
			refresh := modalEndpointRefresh{
				SSHHost:         "restored-host",
				SSHPort:         5678,
				Restored:        true,
				SnapshotImageId: "im-snapshot",
			}
			refreshModalEndpointDetailed = func(context.Context, string) (modalEndpointRefresh, error) {
				return refresh, nil
			}
			calls := 0
			e := &ModalEnv{
				SandboxName: "snapshot-sandbox",
				runModalAPICommand: func(context.Context, EnvRunCommandInput) (EnvRunCommandOutput, error) {
					calls++
					if calls == 1 {
						return EnvRunCommandOutput{}, errModalSandboxNotRunning
					}
					return EnvRunCommandOutput{Stdout: "snapshot output", Stderr: "snapshot diagnostics"}, tt.err
				},
			}

			output, err := e.Snapshot(context.Background())
			if tt.err != nil {
				require.ErrorIs(t, err, tt.err)
				assert.Contains(t, err.Error(), refresh.restoreNotice(e.SandboxName))
			} else {
				require.NoError(t, err)
			}
			assert.Equal(t, "snapshot output", output.Stdout)
			assert.Equal(t, refresh.restoreNotice(e.SandboxName)+"\nsnapshot diagnostics", output.Stderr)
			assert.Equal(t, 2, calls)
		})
	}
}
