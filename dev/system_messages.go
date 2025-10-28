package dev

const (
	SystemWarnNearToolCallLimit = "SYSTEM MESSAGE: Warning, nearing limit for too many tool calls without progress. Resolution: Either start to progress the task or use just 1 more tool call."
	SystemHitToolCallLimit      = "SYSTEM MESSAGE: ⚠️‼️ Hit limit for too many additional tool calls without task progress. Resolution: Please progress the task now."
)

// ThresholdMessageForCounter determines whether a system message should be
// injected for the given feedback cadence and since-last-feedback counter.
// It repeats each cycle by checking the counter modulo the cadence against the
// thresholds. T3 is checked first to prefer the "hit limit" message when
// thresholds collapse.
func ThresholdMessageForCounter(feedbackCadence, sinceLastFeedback int) (string, bool) {
	if feedbackCadence <= 1 || sinceLastFeedback == 0 {
		return "", false
	}
	r := sinceLastFeedback % feedbackCadence
	if r == 0 {
		return "", false
	}

	t1 := (feedbackCadence * 7) / 10
	t2 := (feedbackCadence * 8) / 10
	t3 := feedbackCadence - 1

	if r == t3 {
		return SystemHitToolCallLimit, true
	}
	if r == t2 || r == t1 {
		return SystemWarnNearToolCallLimit, true
	}
	return "", false
}
