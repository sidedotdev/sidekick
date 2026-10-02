package llm2

import (
	"fmt"
)

// RunawayWhitespaceLimit is the longest run of consecutive whitespace a single
// content block may stream before the response is considered degenerate.
// Legitimate output never approaches this: indentation and blank lines in text
// are at most a few hundred characters, and inside tool call JSON strings
// newlines and tabs are escaped, so only literal indentation spaces count.
const RunawayWhitespaceLimit = 4096

// RunawayWhitespaceError reports a content block whose streamed deltas
// degenerated into an unbounded run of whitespace.
type RunawayWhitespaceError struct {
	Index     int
	RunLength int
}

func (e *RunawayWhitespaceError) Error() string {
	return fmt.Sprintf("runaway whitespace output: content block %d streamed %d consecutive whitespace characters", e.Index, e.RunLength)
}

// RunawayWhitespaceDetector tracks the trailing whitespace run of each
// streaming content block across text deltas.
type RunawayWhitespaceDetector struct {
	limit int
	runs  map[int]int
}

func NewRunawayWhitespaceDetector(limit int) *RunawayWhitespaceDetector {
	return &RunawayWhitespaceDetector{limit: limit, runs: make(map[int]int)}
}

// Observe feeds one stream event to the detector and returns a
// *RunawayWhitespaceError as soon as any whitespace run within a block
// exceeds the limit. Runs are counted across delta boundaries, so detection
// does not depend on how the provider chunks the stream.
func (d *RunawayWhitespaceDetector) Observe(event Event) error {
	switch event.Type {
	case EventBlockStarted:
		d.runs[event.Index] = 0
	case EventBlockDone:
		delete(d.runs, event.Index)
	case EventTextDelta:
		run := d.runs[event.Index]
		for i := 0; i < len(event.Delta); i++ {
			if isWhitespace(event.Delta[i]) {
				run++
				if run > d.limit {
					d.runs[event.Index] = run
					return &RunawayWhitespaceError{Index: event.Index, RunLength: run}
				}
			} else {
				run = 0
			}
		}
		d.runs[event.Index] = run
	}
	return nil
}

func isWhitespace(c byte) bool {
	switch c {
	case ' ', '\t', '\n', '\r', '\v', '\f':
		return true
	}
	return false
}
