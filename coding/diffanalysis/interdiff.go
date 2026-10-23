package diffanalysis

import "strings"

const interdiffContextLines = 5

// Interdiff compares two full review diffs against a common base, including
// reversions of prior work. Omitted, unchanged content is shared symbolically;
// contradictory base evidence or missing content needed by an output patch
// returns an error so callers can distinguish a fallback from a comparison.
// Binary summaries remain summaries because they do not contain file bytes.
//
// An empty priorDiff yields an empty result: there is nothing to compare
// against, so callers are expected to fall back to the full diff.
func Interdiff(priorDiff, currentDiff string) (string, error) {
	if strings.TrimSpace(priorDiff) == "" {
		return "", nil
	}
	trees, err := readReviewTrees(priorDiff, currentDiff)
	if err != nil {
		return "", err
	}
	return compareReviewTrees(trees.prior, trees.current)
}

// InterdiffWithBaseChange compares reviews across a base transition. All three
// patches retain their original coordinates: prior and baseChange share the old
// base, while current is relative to the new base.
func InterdiffWithBaseChange(prior, current, baseChange string) (string, error) {
	// Competing replacements retain the reviewed content, so their eventual
	// resolution remains visible rather than becoming part of the baseline.
	rebased, err := rebaseReviewDiff(prior, baseChange, true)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(rebased) == "" {
		return current, nil
	}
	return Interdiff(rebased, current)
}
