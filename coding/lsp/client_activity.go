package lsp

import (
	"context"
	"errors"
	"sync/atomic"

	"github.com/rs/zerolog/log"
)

func (la *LSPActivities) beginClientActivity(ctx context.Context, client LSPClient) (context.Context, func(), error) {
	active, ok := client.(interface {
		BeginActivity(context.Context, func(error)) (func(), error)
	})
	if !ok {
		return ctx, func() {}, nil
	}
	ctx, cancel := context.WithCancelCause(ctx)
	var lost atomic.Bool
	end, err := active.BeginActivity(ctx, func(err error) {
		lost.Store(true)
		cancel(err)
		if err := closeClient(client); err != nil {
			log.Warn().Err(err).Msg("Failed to close analysis client after activity lease loss")
		}
	})
	if err != nil {
		cancel(err)
		return ctx, nil, err
	}
	release := func() {
		end()
		if lost.Load() {
			if err := la.closeClients(); err != nil {
				log.Warn().Err(err).Msg("Failed to discard analysis clients after activity lease loss")
			}
		}
		cancel(nil)
	}
	if err := ctx.Err(); err != nil {
		end()
		err = errors.Join(err, la.closeClients())
		cancel(nil)
		return ctx, nil, err
	}
	return ctx, release, nil
}

func clientIsConnected(client LSPClient) bool {
	if health, ok := client.(interface{ IsConnected() bool }); ok {
		return health.IsConnected()
	}
	return true
}
