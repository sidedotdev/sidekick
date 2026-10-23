package env

import (
	"context"
	"errors"
	"testing"
	"time"

	"sidekick/sideagent"

	"github.com/stretchr/testify/require"
)

func TestManagedActivityAdmissionAndRelease(t *testing.T) {
	t.Parallel()
	ended := make(chan struct{})
	release, err := beginActivity(context.Background(), func(ctx context.Context, req sideagent.ExecRequest) (sideagent.ExecResponse, error) {
		defer close(ended)
		require.True(t, req.ActivityLease)
		require.Equal(t, remoteActivityMarker, req.TouchPath)
		req.OnReady()
		<-ctx.Done()
		return sideagent.ExecResponse{}, ctx.Err()
	}, func(err error) { t.Errorf("normal release reported lease loss: %v", err) })
	require.NoError(t, err)
	select {
	case <-ended:
		t.Fatal("lease ended while the request was active")
	default:
	}
	release()
	select {
	case <-ended:
	case <-time.After(time.Second):
		t.Fatal("release did not drain the lease")
	}
	release()
}

func TestManagedActivityRefusalAndCancellation(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		run  func(context.Context, sideagent.ExecRequest) (sideagent.ExecResponse, error)
	}{
		{name: "sealed", run: func(context.Context, sideagent.ExecRequest) (sideagent.ExecResponse, error) {
			return sideagent.ExecResponse{Sealed: true}, nil
		}},
		{name: "transport failure", run: func(context.Context, sideagent.ExecRequest) (sideagent.ExecResponse, error) {
			return sideagent.ExecResponse{}, errors.New("connection lost")
		}},
		{name: "unacknowledged", run: func(context.Context, sideagent.ExecRequest) (sideagent.ExecResponse, error) {
			return sideagent.ExecResponse{}, nil
		}},
		{name: "cancellation before admission", run: func(ctx context.Context, _ sideagent.ExecRequest) (sideagent.ExecResponse, error) {
			<-ctx.Done()
			return sideagent.ExecResponse{}, ctx.Err()
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			release, err := beginActivity(ctx, tc.run, nil)
			require.Error(t, err)
			require.Nil(t, release)
		})
	}
}

func TestManagedActivityLossAfterAdmission(t *testing.T) {
	t.Parallel()
	disconnected := make(chan struct{})
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	release, err := beginActivity(ctx, func(ctx context.Context, req sideagent.ExecRequest) (sideagent.ExecResponse, error) {
		req.OnReady()
		select {
		case <-disconnected:
			return sideagent.ExecResponse{}, errors.New("agent disconnected")
		case <-ctx.Done():
			return sideagent.ExecResponse{}, ctx.Err()
		}
	}, cancel)
	require.NoError(t, err)
	close(disconnected)
	select {
	case <-ctx.Done():
		require.ErrorContains(t, context.Cause(ctx), "agent disconnected")
	case <-time.After(time.Second):
		t.Fatal("lease loss was not reported to the operation")
	}
	release()
}

func TestManagedActivityRecoversSealedSandbox(t *testing.T) {
	t.Parallel()
	calls := 0
	e := &ModalEnv{
		runModalCommand: func(ctx context.Context, input EnvRunCommandInput) (EnvRunCommandOutput, string, error) {
			calls++
			if calls == 1 {
				return modalExecResponse(sideagent.ExecResponse{Sealed: true}, "sandbox")
			}
			require.NotNil(t, input.activity)
			input.activity.admit()
			<-ctx.Done()
			return EnvRunCommandOutput{}, "", ctx.Err()
		},
		runModalAPICommand: func(context.Context, EnvRunCommandInput) (EnvRunCommandOutput, error) {
			t.Error("an activity lease must not use buffered API execution")
			return EnvRunCommandOutput{}, errors.New("unexpected API fallback")
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	release, err := BeginActivity(ctx, e, nil)
	require.NoError(t, err)
	release()
	require.Equal(t, 2, calls)
}
