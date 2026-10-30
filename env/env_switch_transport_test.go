package env

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGitOutputForSwitchTransportRecovery(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name         string
		args         []string
		failures     int
		transport    bool
		cancel       bool
		exitStatus   int
		wantAttempts int
		wantError    bool
	}{
		{
			name:     "branch read recovers",
			args:     []string{"rev-parse", "--abbrev-ref", "HEAD"},
			failures: 1, transport: true, wantAttempts: 2,
		},
		{
			name:     "head read recovers",
			args:     []string{"rev-parse", "HEAD"},
			failures: 1, transport: true, wantAttempts: 2,
		},
		{
			name:     "persistent transport failure is bounded",
			args:     []string{"rev-parse", "HEAD"},
			failures: 100, transport: true, wantAttempts: 3, wantError: true,
		},
		{
			name:     "cancellation is not retried",
			args:     []string{"rev-parse", "HEAD"},
			failures: 1, transport: true, cancel: true, wantAttempts: 1, wantError: true,
		},
		{
			name:     "other errors are not retried",
			args:     []string{"rev-parse", "HEAD"},
			failures: 1, wantAttempts: 1, wantError: true,
		},
		{
			name:       "git failure is not retried",
			args:       []string{"rev-parse", "HEAD"},
			exitStatus: 128, wantAttempts: 1, wantError: true,
		},
		{
			name:     "reset is not replayed",
			args:     []string{"reset", "--hard", "HEAD"},
			failures: 1, transport: true, wantAttempts: 1, wantError: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			cause := errors.New("agent exec channel closed: closed by client")
			attempts := 0
			e := &ModalEnv{
				runModalCommand: func(context.Context, EnvRunCommandInput) (EnvRunCommandOutput, string, error) {
					attempts++
					if tc.cancel {
						cancel()
					}
					if attempts <= tc.failures {
						if tc.transport {
							return EnvRunCommandOutput{}, "", &agentExecTransportError{cause: cause}
						}
						return EnvRunCommandOutput{}, "", cause
					}
					return EnvRunCommandOutput{Stdout: "result\n", ExitStatus: tc.exitStatus}, "", nil
				},
			}

			output, err := gitOutputForSwitch(ctx, e, tc.args...)
			if tc.wantError {
				require.Error(t, err)
				if tc.failures > 0 {
					assert.ErrorIs(t, err, cause)
				}
			} else {
				require.NoError(t, err)
				assert.Equal(t, "result", output)
			}
			assert.Equal(t, tc.wantAttempts, attempts)
		})
	}
}
