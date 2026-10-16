package env

import (
	"bytes"
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGitSyncBackgroundCoalescesAndCheckpointWaits(t *testing.T) {
	t.Parallel()
	c := newGitSyncTestCoordinator(t, 0)
	started := make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	var calls atomic.Int32
	run := func(ctx context.Context, checkpoint bool) error {
		call := calls.Add(1)
		if !checkpoint {
			if call == 1 {
				close(started)
			}
			select {
			case <-release:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return nil
	}

	c.background(context.Background(), "repo", "tree", run)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("background sync did not start")
	}
	c.background(context.Background(), "repo", "tree", run)
	assert.EqualValues(t, 1, calls.Load())

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, c.checkpoint(ctx, "repo", "tree", run), context.Canceled)
	assert.EqualValues(t, 1, calls.Load())
}

func TestGitSyncCheckpointIsFreshAndReturnsFailure(t *testing.T) {
	t.Parallel()
	c := newGitSyncTestCoordinator(t, 0)
	started := make(chan struct{})
	release := make(chan struct{})
	backgroundDone := make(chan struct{})
	c.background(context.Background(), "repo", "tree", func(ctx context.Context, checkpoint bool) error {
		close(started)
		<-release
		close(backgroundDone)
		return nil
	})
	select {
	case <-started:
	case <-time.After(time.Second):
		close(release)
		t.Fatal("background sync did not start")
	}

	expected := errors.New("checkpoint failed")
	result := make(chan error, 1)
	var checkpointCalls atomic.Int32
	go func() {
		result <- c.checkpoint(context.Background(), "repo", "tree", func(ctx context.Context, checkpoint bool) error {
			checkpointCalls.Add(1)
			if !checkpoint {
				return errors.New("checkpoint was not marked as blocking")
			}
			return expected
		})
	}()
	select {
	case err := <-result:
		close(release)
		t.Fatalf("checkpoint passed in-flight background sync: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	select {
	case err := <-result:
		require.ErrorIs(t, err, expected)
	case <-time.After(time.Second):
		t.Fatal("checkpoint did not finish")
	}
	<-backgroundDone
	assert.EqualValues(t, 1, checkpointCalls.Load())
}

func TestGitSyncBackgroundLogsFailureAndThrottles(t *testing.T) {
	previous := log.Logger
	var output bytes.Buffer
	log.Logger = zerolog.New(&output)
	defer func() { log.Logger = previous }()

	c := newGitSyncTestCoordinator(t, 0)
	var calls atomic.Int32
	done := make(chan struct{})
	c.background(context.Background(), "repo", "tree", func(ctx context.Context, checkpoint bool) error {
		calls.Add(1)
		close(done)
		return errors.New("test background failure")
	})
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("background sync did not start")
	}
	require.NoError(t, c.checkpoint(context.Background(), "repo", "tree", func(context.Context, bool) error { return nil }))
	c.background(context.Background(), "repo", "tree", func(context.Context, bool) error {
		calls.Add(1)
		return nil
	})
	assert.EqualValues(t, 1, calls.Load())
	assert.Contains(t, output.String(), "test background failure")
}

func TestGitSyncTrailingRequests(t *testing.T) {
	for _, otherWorktree := range []bool{false, true} {
		t.Run(map[bool]string{false: "same worktree", true: "another worktree"}[otherWorktree], func(t *testing.T) {
			t.Parallel()
			c := newGitSyncTestCoordinator(t, time.Millisecond)
			started := make(chan struct{})
			release := make(chan struct{})
			c.background(context.Background(), "repo", "first", func(context.Context, bool) error {
				close(started)
				<-release
				return nil
			})
			select {
			case <-started:
			case <-time.After(time.Second):
				close(release)
				t.Fatal("initial scan did not start")
			}
			worktree := "first"
			if otherWorktree {
				worktree = "second"
			}
			synced := make(chan struct{}, 1)
			c.background(context.Background(), "repo", worktree, func(context.Context, bool) error {
				synced <- struct{}{}
				return nil
			})
			close(release)
			select {
			case <-synced:
			case <-time.After(time.Second):
				t.Fatal("pending worktree was not synced")
			}
		})
	}
}

func TestGitSyncFinalCommandWithinThrottle(t *testing.T) {
	t.Parallel()
	c := newGitSyncTestCoordinator(t, 10*time.Millisecond)
	require.NoError(t, c.checkpoint(context.Background(), "repo", "tree", func(context.Context, bool) error { return nil }))
	synced := make(chan struct{}, 1)
	c.background(context.Background(), "repo", "tree", func(context.Context, bool) error {
		synced <- struct{}{}
		return nil
	})
	select {
	case <-synced:
	case <-time.After(time.Second):
		t.Fatal("final command was lost during throttle")
	}
}

func TestGitSyncRetriesWithoutAnotherCommand(t *testing.T) {
	t.Parallel()
	c := newGitSyncTestCoordinator(t, time.Millisecond)
	var calls atomic.Int32
	synced := make(chan struct{}, 1)
	c.background(context.Background(), "repo", "tree", func(context.Context, bool) error {
		if calls.Add(1) < 3 {
			return errors.New("temporary transport failure")
		}
		synced <- struct{}{}
		return nil
	})
	select {
	case <-synced:
	case <-time.After(time.Second):
		t.Fatal("failed background sync was not retried")
	}
	assert.EqualValues(t, 3, calls.Load())
}

func newGitSyncTestCoordinator(t *testing.T, interval time.Duration) *gitSyncCoordinator {
	t.Helper()
	c := &gitSyncCoordinator{interval: interval}
	t.Cleanup(func() {
		for {
			c.mu.Lock()
			var running <-chan struct{}
			for _, job := range c.jobs {
				if job.timer != nil {
					job.timer.Stop()
					job.timer = nil
				}
				job.pending = make(map[string]gitSyncRequest)
				job.order = nil
				if job.running {
					running = job.done
				}
			}
			c.mu.Unlock()
			if running == nil {
				return
			}
			select {
			case <-running:
			case <-time.After(time.Second):
				t.Fatal("git sync goroutine outlived its test")
			}
		}
	})
	return c
}
