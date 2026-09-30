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
