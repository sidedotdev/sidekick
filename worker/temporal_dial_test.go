package worker

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/client"
)

type logCapture struct {
	lines []string
}

func (c *logCapture) Write(p []byte) (int, error) {
	c.lines = append(c.lines, strings.TrimSpace(string(p)))
	return len(p), nil
}

func (c *logCapture) levels(t *testing.T) []string {
	t.Helper()
	levels := make([]string, 0, len(c.lines))
	for _, line := range c.lines {
		var entry map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &entry))
		level, _ := entry["level"].(string)
		levels = append(levels, level)
	}
	return levels
}

// newTestRetrier wires a retrier to a virtual clock so retries are instant and
// deterministic.
func newTestRetrier(dial func(ctx context.Context) (client.Client, error), capture *logCapture) *temporalDialRetrier {
	now := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	retrier := newTemporalDialRetrier(dial)
	retrier.logger = zerolog.New(capture).Level(zerolog.DebugLevel)
	retrier.now = func() time.Time { return now }
	retrier.sleep = func(ctx context.Context, d time.Duration) bool {
		if ctx.Err() != nil {
			return false
		}
		now = now.Add(d)
		return true
	}
	return retrier
}

func countLevel(levels []string, level string) int {
	count := 0
	for _, l := range levels {
		if l == level {
			count++
		}
	}
	return count
}

func TestTemporalDialRetrierRetriesIndefinitely(t *testing.T) {
	t.Parallel()

	capture := &logCapture{}
	attempts := 0
	// enough attempts that any bounded retry policy would have given up
	const successfulAttempt = 500
	retrier := newTestRetrier(func(context.Context) (client.Client, error) {
		attempts++
		if attempts < successfulAttempt {
			return nil, errors.New("connection refused")
		}
		return nil, nil
	}, capture)

	_, err := retrier.run(context.Background())
	require.NoError(t, err)
	assert.Equal(t, successfulAttempt, attempts)
}

func TestTemporalDialRetrierLogsErrorsOnlyBeyondThreshold(t *testing.T) {
	t.Parallel()

	capture := &logCapture{}
	attempts := 0
	retrier := newTestRetrier(func(context.Context) (client.Client, error) {
		attempts++
		if attempts <= 20 {
			return nil, errors.New("connection refused")
		}
		return nil, nil
	}, capture)
	retrier.retryInterval = time.Second
	retrier.errorThreshold = 30 * time.Second

	_, err := retrier.run(context.Background())
	require.NoError(t, err)

	levels := capture.levels(t)
	assert.Equal(t, 0, countLevel(levels, "error"))
	assert.Equal(t, 20, countLevel(levels, "debug"))
}

func TestTemporalDialRetrierThrottlesErrorLogs(t *testing.T) {
	t.Parallel()

	capture := &logCapture{}
	attempts := 0
	// 5 minutes of failures at one dial per second
	const failures = 300
	retrier := newTestRetrier(func(context.Context) (client.Client, error) {
		attempts++
		if attempts <= failures {
			return nil, errors.New("connection refused")
		}
		return nil, nil
	}, capture)
	retrier.retryInterval = time.Second
	retrier.errorThreshold = 30 * time.Second
	retrier.errorInterval = time.Minute

	_, err := retrier.run(context.Background())
	require.NoError(t, err)

	// first error at the 30s threshold, then once per minute after that
	assert.Equal(t, 5, countLevel(capture.levels(t), "error"))
}

func TestTemporalDialRetrierStopsOnContextCancellation(t *testing.T) {
	t.Parallel()

	capture := &logCapture{}
	ctx, cancel := context.WithCancel(context.Background())
	attempts := 0
	retrier := newTestRetrier(func(context.Context) (client.Client, error) {
		attempts++
		if attempts == 3 {
			cancel()
		}
		return nil, errors.New("connection refused")
	}, capture)

	_, err := retrier.run(ctx)
	assert.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, 3, attempts)
}

func TestTemporalDialRetrierSkipsDialWhenContextAlreadyDone(t *testing.T) {
	t.Parallel()

	capture := &logCapture{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	dials := 0
	retrier := newTestRetrier(func(context.Context) (client.Client, error) {
		dials++
		return nil, errors.New("connection refused")
	}, capture)

	_, err := retrier.run(ctx)
	assert.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, 0, dials)
}

func TestTemporalDialRetrierCancelsInFlightDial(t *testing.T) {
	t.Parallel()

	capture := &logCapture{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dialStarted := make(chan struct{})
	dialObservedCancellation := false
	retrier := newTestRetrier(func(dialCtx context.Context) (client.Client, error) {
		close(dialStarted)
		select {
		case <-dialCtx.Done():
			dialObservedCancellation = true
			return nil, dialCtx.Err()
		case <-time.After(5 * time.Second):
			return nil, errors.New("dial was not given a cancellable context")
		}
	}, capture)

	go func() {
		<-dialStarted
		cancel()
	}()

	_, err := retrier.run(ctx)
	assert.ErrorIs(t, err, context.Canceled)
	assert.True(t, dialObservedCancellation, "an in-flight dial should abort rather than block until its own timeout")
}
