package sideagent

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"
)

func TestActivityLeaseHeartbeatAndDrain(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		seal := testSeal(t)
		client := startTestChannelWithSeal(t, seal)
		marker := filepath.Join(t.TempDir(), "activity")
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		ready := make(chan struct{})
		done := make(chan error, 1)
		go func() {
			_, err := client.Exec(ctx, ExecRequest{
				ActivityLease: true,
				TouchPath:     marker,
				OnReady:       func() { close(ready) },
			})
			done <- err
		}()
		select {
		case <-ready:
		case err := <-done:
			t.Fatalf("lease ended before admission: %v", err)
		case <-time.After(5 * time.Second):
			t.Fatal("lease was not admitted")
		}
		mustNotSeal(t, seal, "an admitted background request is active")
		synctest.Wait()
		previous := time.Unix(100, 0)
		require.NoError(t, os.Chtimes(marker, previous, previous))
		for range 2 {
			time.Sleep(activityHeartbeatInterval)
			synctest.Wait()
			info, err := os.Stat(marker)
			require.NoError(t, err)
			require.True(t, info.ModTime().After(previous), "active lease must refresh its heartbeat")
			previous = info.ModTime()
		}
		cancel()
		select {
		case err := <-done:
			require.ErrorIs(t, err, context.Canceled)
		case <-time.After(5 * time.Second):
			t.Fatal("lease did not release on cancellation")
		}
		mustSeal(t, seal, "the background session has no active request")

		time.Sleep(2 * activityHeartbeatInterval)
		synctest.Wait()
		info, err := os.Stat(marker)
		require.NoError(t, err)
		require.Equal(t, previous, info.ModTime(), "released lease must stop its heartbeat")

		resp, err := client.Exec(context.Background(), ExecRequest{ActivityLease: true})
		require.NoError(t, err)
		require.True(t, resp.Sealed)
	})
}

func TestActivityLeaseDisconnectReleasesDrain(t *testing.T) {
	t.Parallel()
	seal := testSeal(t)
	reqR, reqW := io.Pipe()
	respR, respW := io.Pipe()
	t.Cleanup(func() { _ = reqW.Close() })
	serverDone := make(chan error, 1)
	go func() {
		err := serveWithSeal(reqR, respW, seal)
		_ = respW.CloseWithError(err)
		serverDone <- err
	}()
	client := NewClient(respR, reqW)
	ready := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, err := client.Exec(context.Background(), ExecRequest{
			ActivityLease: true,
			OnReady:       func() { close(ready) },
		})
		if err != nil && !errors.Is(err, ErrChannelClosed) {
			t.Errorf("unexpected activity lease error after disconnect: %v", err)
		}
	}()
	select {
	case <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("lease was not admitted")
	}
	mustNotSeal(t, seal, "the request is active")
	require.NoError(t, reqW.Close())
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("disconnect did not end the lease")
	}
	require.NoError(t, <-serverDone)
	mustSeal(t, seal, "the managed connection was lost")
}
