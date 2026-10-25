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
