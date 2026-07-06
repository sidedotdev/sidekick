package dev

import (
	"context"
	"os"
	"strconv"
	"strings"
)

// defaultConflictRereviewPercent is the share of the merged diff that an
// auto-resolution may change (relative to the diff the user already approved)
// before we ask the user to review the merge again. It is intentionally small
// and can be tuned via SIDE_CONFLICT_REREVIEW_PERCENT.
const defaultConflictRereviewPercent = 4.0

const conflictRereviewPercentEnvVar = "SIDE_CONFLICT_REREVIEW_PERCENT"

func conflictRereviewPercent() float64 {
	if raw := strings.TrimSpace(os.Getenv(conflictRereviewPercentEnvVar)); raw != "" {
		if pct, err := strconv.ParseFloat(raw, 64); err == nil && pct >= 0 {
			return pct
		}
	}
	return defaultConflictRereviewPercent
}

// AssessResolutionSubstantialityInput carries the two base-branch diffs that
// bracket a conflict auto-resolution.
type AssessResolutionSubstantialityInput struct {
	// BeforeDiff is the diff against the target branch that the user approved
	// prior to conflict resolution.
	BeforeDiff string
	// AfterDiff is the diff against the target branch after conflict resolution.
	AfterDiff string
}

// AssessResolutionSubstantialityResult reports how much the resolution changed
// the merged diff and whether that crosses the re-review threshold.
type AssessResolutionSubstantialityResult struct {
	PercentChanged   float64 `json:"percentChanged"`
	ThresholdPercent float64 `json:"thresholdPercent"`
	Substantial      bool    `json:"substantial"`
}

// AssessResolutionSubstantialityActivity measures the whitespace-insensitive
// interdiff between the pre- and post-resolution diffs and decides whether the
// change is large enough (as a percentage of the whole diff) to warrant another
// human review.
func AssessResolutionSubstantialityActivity(ctx context.Context, input AssessResolutionSubstantialityInput) (AssessResolutionSubstantialityResult, error) {
	changed, total := interdiffChangedLines(input.BeforeDiff, input.AfterDiff)
	var percent float64
	if total > 0 {
		percent = float64(changed) / float64(total) * 100
	}
	threshold := conflictRereviewPercent()
	return AssessResolutionSubstantialityResult{
		PercentChanged:   percent,
		ThresholdPercent: threshold,
		Substantial:      percent >= threshold,
	}, nil
}

// interdiffChangedLines computes a whitespace-insensitive interdiff between two
// unified diffs. It returns the number of changed diff lines that differ
// between them (the multiset symmetric difference) and the size of the larger
// diff, so callers can express the delta as a fraction of the whole diff.
func interdiffChangedLines(before, after string) (changed int, total int) {
	beforeCounts := normalizedChangedLines(before)
	afterCounts := normalizedChangedLines(after)

	seen := make(map[string]struct{}, len(beforeCounts)+len(afterCounts))
	for key := range beforeCounts {
		seen[key] = struct{}{}
	}
	for key := range afterCounts {
		seen[key] = struct{}{}
	}
	for key := range seen {
		delta := afterCounts[key] - beforeCounts[key]
		if delta < 0 {
			delta = -delta
		}
		changed += delta
	}

	beforeTotal := sumCounts(beforeCounts)
	afterTotal := sumCounts(afterCounts)
	total = beforeTotal
	if afterTotal > total {
		total = afterTotal
	}
	return changed, total
}

// normalizedChangedLines counts the added/removed content lines of a unified
// diff keyed by their sign plus whitespace-stripped content, so that identical
// changes (modulo whitespace) collapse together and pure-whitespace changes are
// ignored entirely.
func normalizedChangedLines(diff string) map[string]int {
	counts := make(map[string]int)
	for _, line := range strings.Split(diff, "\n") {
		if len(line) == 0 {
			continue
		}
		if strings.HasPrefix(line, "+++ ") || strings.HasPrefix(line, "--- ") {
			continue
		}
		sign := line[0]
		if sign != '+' && sign != '-' {
			continue
		}
		content := strings.Join(strings.Fields(line[1:]), "")
		if content == "" {
			continue
		}
		counts[string(sign)+content]++
	}
	return counts
}

func sumCounts(counts map[string]int) int {
	total := 0
	for _, c := range counts {
		total += c
	}
	return total
}
