package llm2

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRunawayWhitespaceDetector(t *testing.T) {
	t.Parallel()

	delta := func(index int, s string) Event { return Event{Type: EventTextDelta, Index: index, Delta: s} }
	started := func(index int) Event {
		return Event{Type: EventBlockStarted, Index: index, ContentBlock: &ContentBlock{Type: ContentBlockTypeText}}
	}
	done := func(index int) Event { return Event{Type: EventBlockDone, Index: index} }

	tests := []struct {
		name      string
		events    []Event
		wantError bool
	}{
		{
			name:   "whitespace at limit is allowed",
			events: []Event{started(0), delta(0, strings.Repeat(" ", 10))},
		},
		{
			name:      "single delta exceeding limit",
			events:    []Event{started(0), delta(0, strings.Repeat("\t", 11))},
			wantError: true,
		},
		{
			name:      "run accumulates across deltas",
			events:    []Event{started(0), delta(0, "x"), delta(0, "     "), delta(0, "   \n"), delta(0, "  ")},
			wantError: true,
		},
		{
			name:   "non-whitespace resets the run",
			events: []Event{started(0), delta(0, "        "), delta(0, "y   "), delta(0, "      ")},
		},
		{
			name:   "runs are tracked per block",
			events: []Event{started(0), delta(0, "      "), started(1), delta(1, "      ")},
		},
		{
			name:   "block done clears the run",
			events: []Event{started(0), delta(0, "        "), done(0), started(0), delta(0, "        ")},
		},
		{
			name:      "trailing whitespace of mixed delta counts toward run",
			events:    []Event{started(0), delta(0, "z       "), delta(0, "    ")},
			wantError: true,
		},
		{
			name:      "oversized run followed by text within one delta",
			events:    []Event{started(0), delta(0, strings.Repeat(" ", 11)+"text")},
			wantError: true,
		},
		{
			name:      "run crossing deltas whose final delta ends with text",
			events:    []Event{started(0), delta(0, "a"+strings.Repeat("\t", 6)), delta(0, strings.Repeat("\t", 5)+"}")},
			wantError: true,
		},
		{
			name:   "runs separated by text within one delta stay under limit",
			events: []Event{started(0), delta(0, strings.Repeat(" ", 8)+"b"+strings.Repeat(" ", 8)+"c"+strings.Repeat(" ", 8))},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			detector := NewRunawayWhitespaceDetector(10)
			var err error
			for _, event := range tt.events {
				if err = detector.Observe(event); err != nil {
					break
				}
			}
			if tt.wantError {
				var runaway *RunawayWhitespaceError
				require.ErrorAs(t, err, &runaway)
				require.Greater(t, runaway.RunLength, 10)
			} else {
				require.NoError(t, err)
			}
		})
	}
}
