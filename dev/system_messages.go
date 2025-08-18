package dev

import "sort"

const (
	SystemWarnNearToolCallLimit = "SYSTEM MESSAGE: Warning, nearing limit for too many tool calls."
	SystemHitToolCallLimit      = "SYSTEM MESSAGE: Hit limit for too many tool calls."
)

// CycleThresholds returns the unique threshold iteration offsets within a cycle
// based on the feedback cadence F. The thresholds are derived as:
// T1 = floor(0.8 * F), T2 = floor(0.9 * F), T3 = F - 1.
// Only thresholds where 1 <= Ti < F are included, with duplicates removed.
func CycleThresholds(F int) []int {
	if F <= 1 {
		return nil
	}

	t1 := (F * 8) / 10
	t2 := (F * 9) / 10
	t3 := F - 1

	unique := make(map[int]struct{}, 3)
	add := func(t int) {
		if t >= 1 && t < F {
			unique[t] = struct{}{}
		}
	}

	add(t1)
	add(t2)
	add(t3)

	out := make([]int, 0, len(unique))
	for k := range unique {
		out = append(out, k)
	}
	sort.Ints(out)
	return out
}

// ThresholdMessageForCounter determines whether a system message should be
// injected for the given feedback cadence F and since-last-feedback counter S.
// It repeats each cycle by checking S % F against the thresholds. T3 is checked
// first to prefer the "hit limit" message when thresholds collapse.
func ThresholdMessageForCounter(F, S int) (string, bool) {
	if F <= 1 || S == 0 {
		return "", false
	}
	r := S % F
	if r == 0 {
		return "", false
	}

	t1 := (F * 8) / 10
	t2 := (F * 9) / 10
	t3 := F - 1

	if t3 >= 1 && t3 < F && r == t3 {
		return SystemHitToolCallLimit, true
	}
	if t2 >= 1 && t2 < F && r == t2 {
		return SystemWarnNearToolCallLimit, true
	}
	if t1 >= 1 && t1 < F && r == t1 {
		return SystemWarnNearToolCallLimit, true
	}
	return "", false
}
