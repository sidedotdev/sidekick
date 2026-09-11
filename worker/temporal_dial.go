package worker

import (
	"context"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"go.temporal.io/sdk/client"
)

const (
	temporalDialRetryInterval = 1 * time.Second

	// Temporal typically isn't accepting connections for the first several
	// seconds after a restart, so dial failures only become noteworthy once
	// they persist well past that.
	temporalDialErrorThreshold = 30 * time.Second

	temporalDialErrorLogInterval = 1 * time.Minute
)

// temporalDialRetrier dials Temporal until it succeeds or the context is done,
// keeping early failures quiet and reporting persistent ones at a bounded rate.
type temporalDialRetrier struct {
	dial           func(ctx context.Context) (client.Client, error)
	retryInterval  time.Duration
	errorThreshold time.Duration
	errorInterval  time.Duration
	logger         zerolog.Logger
	now            func() time.Time
	sleep          func(ctx context.Context, d time.Duration) bool
}

func newTemporalDialRetrier(dial func(ctx context.Context) (client.Client, error)) *temporalDialRetrier {
	return &temporalDialRetrier{
		dial:           dial,
		retryInterval:  temporalDialRetryInterval,
		errorThreshold: temporalDialErrorThreshold,
		errorInterval:  temporalDialErrorLogInterval,
		logger:         log.Logger,
		now:            time.Now,
		sleep:          sleepUnlessDone,
	}
}

func (r *temporalDialRetrier) run(ctx context.Context) (client.Client, error) {
	start := r.now()
	var lastErrorLog time.Time

	for attempt := 1; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		temporalClient, err := r.dial(ctx)
		if err == nil {
			if attempt > 1 {
				r.logger.Info().
					Dur("elapsed", r.now().Sub(start)).
					Int("attempts", attempt).
					Msg("Connected to Temporal server")
			}
			return temporalClient, nil
		}

		now := r.now()
		elapsed := now.Sub(start)
		beyondThreshold := elapsed >= r.errorThreshold
		dueForErrorLog := lastErrorLog.IsZero() || now.Sub(lastErrorLog) >= r.errorInterval
		if beyondThreshold && dueForErrorLog {
			lastErrorLog = now
			r.logger.Error().Err(err).
				Dur("elapsed", elapsed).
				Int("attempts", attempt).
				Msg("Still unable to create Temporal client, retrying")
		} else {
			r.logger.Debug().Err(err).
				Int("attempts", attempt).
				Msgf("Failed to create Temporal client, retrying in %s", r.retryInterval)
		}

		if !r.sleep(ctx, r.retryInterval) {
			return nil, ctx.Err()
		}
	}
}

func sleepUnlessDone(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
