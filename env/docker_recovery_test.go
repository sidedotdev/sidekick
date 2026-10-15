package env

import (
	"context"
	"errors"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/temporal"
)

func TestRestartDockerEngineActivityRequiresApproval(t *testing.T) {
	host, err := os.Hostname()
	require.NoError(t, err)
	restartErr := errors.New("restart failed")

	for _, tc := range []struct {
		name         string
		host         string
		confirmed    bool
		canceled     bool
		restartErr   error
		wantRestarts int32
		wantType     string
		wantErr      error
	}{
		{name: "unconfirmed", host: host, wantType: "DockerRestartNotConfirmed"},
		{name: "missing host", confirmed: true, wantType: "DockerRestartHostMismatch"},
		{name: "different host", host: host + "-other", confirmed: true, wantType: "DockerRestartHostMismatch"},
		{name: "canceled", host: host, confirmed: true, canceled: true, wantErr: context.Canceled},
		{name: "approved", host: host, confirmed: true, wantRestarts: 1},
		{name: "restart failed", host: host, confirmed: true, restartErr: restartErr, wantRestarts: 1, wantErr: restartErr},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var restarts atomic.Int32
			withDockerHealthHooks(t,
				func(context.Context, time.Duration) bool { return false },
				func(context.Context) error {
					restarts.Add(1)
					return tc.restartErr
				},
			)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tc.canceled {
				cancel()
			}

			_, err := RestartDockerEngineActivity(ctx, RestartDockerEngineInput{
				Host: tc.host, Confirmed: tc.confirmed,
			})

			assert.Equal(t, tc.wantRestarts, restarts.Load())
			switch {
			case tc.wantType != "":
				var appErr *temporal.ApplicationError
				require.ErrorAs(t, err, &appErr)
				assert.Equal(t, tc.wantType, appErr.Type())
				assert.True(t, appErr.NonRetryable())
			case tc.wantErr != nil:
				assert.ErrorIs(t, err, tc.wantErr)
			default:
				require.NoError(t, err)
			}
		})
	}
}
