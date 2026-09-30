package persisted_ai

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestStreamHeartbeatLiveness(t *testing.T) {
	t.Parallel()
	start := time.Now()
	tests := []struct {
		name       string
		events     []string
		gap        time.Duration
		wantActive bool
	}{
		{"no events preserves slow requests", nil, 44 * time.Minute, true},
		{"content alone does not imply keepalives", []string{"response.output_text.delta"}, 44 * time.Minute, true},
		{"keepalive tolerates normal gap", []string{"keepalive"}, 30 * time.Second, true},
		{"keepalive tolerates delayed event", []string{"keepalive"}, 34 * time.Second, true},
		{"silence expires", []string{"keepalive"}, 35 * time.Second, false},
		{"content does not disable silence detection", []string{"keepalive", "response.output_text.delta"}, 35 * time.Second, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var heartbeat streamHeartbeat
			for _, event := range tt.events {
				heartbeat.observe(event == "keepalive", start)
			}
			assert.Equal(t, tt.wantActive, heartbeat.active(start.Add(tt.gap)))
		})
	}
}

func TestStreamHeartbeatEveryEventRenewsLiveness(t *testing.T) {
	t.Parallel()
	for _, event := range []string{"keepalive", "response.in_progress", "response.output_text.delta", "response.future_event"} {
		t.Run(event, func(t *testing.T) {
			t.Parallel()
			start := time.Now()
			var heartbeat streamHeartbeat
			heartbeat.observe(true, start)
			assert.False(t, heartbeat.active(start.Add(35*time.Second)))
			heartbeat.observe(event == "keepalive", start.Add(50*time.Second))
			assert.True(t, heartbeat.active(start.Add(84*time.Second)))
			assert.False(t, heartbeat.active(start.Add(85*time.Second)))
		})
	}
}

func TestStreamHeartbeatConcurrentObservation(t *testing.T) {
	t.Parallel()
	var heartbeat streamHeartbeat
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				heartbeat.observe(true, time.Now())
				heartbeat.active(time.Now())
			}
		}()
	}
	wg.Wait()
	assert.True(t, heartbeat.active(time.Now()))
}
