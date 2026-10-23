package llm2

import (
	"context"
	"io"
	"net/http"
	"time"

	"github.com/openai/openai-go/v3/option"
)

const defaultUpstreamActivityInterval = 5 * time.Second

// reportUpstreamActivity emits EventUpstreamActivity when response body bytes
// arrive, at most once per interval. The openai-go SSE decoder silently drops
// comment lines, so the raw body is the only place the ChatGPT codex
// backend's ": keep-alive" comments are observable.
func reportUpstreamActivity(ctx context.Context, eventChan chan<- Event, interval time.Duration) option.Middleware {
	return func(req *http.Request, next option.MiddlewareNext) (*http.Response, error) {
		resp, err := next(req)
		if err != nil || resp == nil || resp.Body == nil {
			return resp, err
		}
		resp.Body = &activityReportingBody{
			ReadCloser: resp.Body,
			ctx:        ctx,
			eventChan:  eventChan,
			interval:   interval,
			lastReport: time.Now(),
		}
		return resp, nil
	}
}

// activityReportingBody must only be read from one goroutine, which holds for
// SDK stream decoders.
type activityReportingBody struct {
	io.ReadCloser
	ctx        context.Context
	eventChan  chan<- Event
	interval   time.Duration
	lastReport time.Time
}

func (b *activityReportingBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if n > 0 {
		if now := time.Now(); now.Sub(b.lastReport) >= b.interval {
			b.lastReport = now
			select {
			case b.eventChan <- Event{Type: EventUpstreamActivity}:
			case <-b.ctx.Done():
			}
		}
	}
	return n, err
}
