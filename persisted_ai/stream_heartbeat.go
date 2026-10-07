package persisted_ai

import (
	"sync"
	"time"
)

// OpenAI Responses keepalives arrive about every 30 seconds. Allow jitter
// without masking prolonged silence after keepalive support is observed.
const upstreamSilenceGrace = 35 * time.Second

type streamHeartbeat struct {
	mu           sync.Mutex
	sawKeepalive bool
	lastEvent    time.Time
}

func (h *streamHeartbeat) observe(keepalive bool, now time.Time) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.sawKeepalive = h.sawKeepalive || keepalive
	if now.After(h.lastEvent) {
		h.lastEvent = now
	}
}

func (h *streamHeartbeat) active(now time.Time) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return !h.sawKeepalive || now.Sub(h.lastEvent) < upstreamSilenceGrace
}
