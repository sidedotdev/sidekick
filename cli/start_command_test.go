package main

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	temporalsrv "sidekick/temporal"

	"github.com/stretchr/testify/assert"
)

func newTestMigrationWaiter(state *atomic.Int32, notices *atomic.Int32) *temporalMigrationWaiter {
	return &temporalMigrationWaiter{
		getState: func() temporalsrv.SchemaMigrationState {
			return temporalsrv.SchemaMigrationState(state.Load())
		},
		pollInterval: time.Millisecond,
		notify:       func() { notices.Add(1) },
	}
}

// waitConcurrently runs two concurrent waiters against the same
// temporalMigrationWaiter, resolves the migration state after both have had a
// chance to observe it running, and returns both results.
func waitConcurrently(t *testing.T, w *temporalMigrationWaiter, state *atomic.Int32, resolvedState temporalsrv.SchemaMigrationState) []bool {
	t.Helper()
	results := make(chan bool, 2)
	for range 2 {
		go func() { results <- w.wait(context.Background()) }()
	}

	// Give both waiters time to poll the running state at least once so the
	// single-notice guarantee is actually exercised.
	time.Sleep(20 * time.Millisecond)
	state.Store(int32(resolvedState))

	var got []bool
	for range 2 {
		select {
		case ok := <-results:
			got = append(got, ok)
		case <-time.After(5 * time.Second):
			t.Fatal("waiter did not unblock after migration state resolved")
		}
	}
	return got
}

func TestTemporalMigrationWaiter_ConcurrentCallersSingleNoticeAndUnblockOnSuccess(t *testing.T) {
	t.Parallel()
	var state, notices atomic.Int32
	state.Store(int32(temporalsrv.SchemaMigrationRunning))
	w := newTestMigrationWaiter(&state, &notices)

	results := waitConcurrently(t, w, &state, temporalsrv.SchemaMigrationSucceeded)

	assert.Equal(t, []bool{true, true}, results)
	assert.Equal(t, int32(1), notices.Load(), "notice must be emitted exactly once across concurrent waiters")
}

func TestTemporalMigrationWaiter_ConcurrentCallersStopOnFailure(t *testing.T) {
	t.Parallel()
	var state, notices atomic.Int32
	state.Store(int32(temporalsrv.SchemaMigrationRunning))
	w := newTestMigrationWaiter(&state, &notices)

	results := waitConcurrently(t, w, &state, temporalsrv.SchemaMigrationFailed)

	assert.Equal(t, []bool{false, false}, results)
	assert.Equal(t, int32(1), notices.Load(), "notice must be emitted exactly once across concurrent waiters")
}

func TestTemporalMigrationWaiter_StopsOnShutdown(t *testing.T) {
	t.Parallel()
	var state, notices atomic.Int32
	state.Store(int32(temporalsrv.SchemaMigrationRunning))
	w := newTestMigrationWaiter(&state, &notices)

	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan bool, 1)
	go func() { result <- w.wait(ctx) }()
	cancel()

	select {
	case ok := <-result:
		assert.False(t, ok)
	case <-time.After(5 * time.Second):
		t.Fatal("waiter did not stop after shutdown was requested")
	}
}
