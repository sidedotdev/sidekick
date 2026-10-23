package diffanalysis

import (
	"fmt"
	"slices"
	"strings"

	"github.com/bluekeyes/go-gitdiff/gitdiff"
)

// RebaseReviewDiff transplants a review onto a changed base. Both inputs are
// relative to the original base; the result is relative to the new base.
// Unknown content retains its original identity throughout the three-way merge.
func RebaseReviewDiff(review, baseChange string) (string, error) {
	return rebaseReviewDiff(review, baseChange, false)
}

func sameReviewContent(a, b reviewFile) bool {
	return a.binaryID == b.binaryID && slices.Equal(a.spans, b.spans)
}

func sameReviewFile(a, b reviewFile) bool {
	return a.mode == b.mode && sameReviewContent(a, b)
}

func mergeReviewTrees(trees reviewTreeSet, preserveConflicts bool) (map[string]reviewFile, error) {
	base, prior, upstream := trees.base, trees.prior, trees.current
	fragments := [2]map[string][]*gitdiff.TextFragment{}
	for side, patch := range trees.patches {
		fragments[side] = map[string][]*gitdiff.TextFragment{}
		for _, file := range patch {
			if !file.IsNew {
				fragments[side][file.OldName] = file.TextFragments
			}
		}
	}
	locations := func(tree map[string]reviewFile) (map[string]string, error) {
		result := map[string]string{}
		for path, file := range tree {
			if file.origin == "" {
				continue
			}
			if _, exists := result[file.origin]; exists {
				return nil, fmt.Errorf("%s: cannot transplant multiple copies of an opaque source", file.origin)
			}
			result[file.origin] = path
		}
		return result, nil
	}
	leftPaths, err := locations(prior)
	if err != nil {
		return nil, err
	}
	rightPaths, err := locations(upstream)
	if err != nil {
		return nil, err
	}
	merged := map[string]reviewFile{}
	add := func(path string, file reviewFile) error {
		if _, exists := merged[path]; exists {
			return fmt.Errorf("%s: conflicting destinations after base change", path)
		}
		merged[path] = file
		return nil
	}
	for path, original := range base {
		leftPath, rightPath := leftPaths[path], rightPaths[path]
		left, leftExists := prior[leftPath]
		right, rightExists := upstream[rightPath]
		if !leftExists || !rightExists {
			if leftExists && (leftPath != path || !sameReviewFile(left, original)) ||
				rightExists && (rightPath != path || !sameReviewFile(right, original)) {
				return nil, fmt.Errorf("%s: deletion conflicts with changes on the other side", path)
			}
			continue
		}
		destination := leftPath
		if leftPath == path {
			destination = rightPath
		} else if rightPath != path && leftPath != rightPath {
			return nil, fmt.Errorf("%s: conflicting renames", path)
		}
		file, err := mergeReviewFile(original, left, right, fragments[0][path], fragments[1][path], preserveConflicts)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		if err := add(destination, file); err != nil {
			return nil, err
		}
	}
	for _, tree := range []map[string]reviewFile{prior, upstream} {
		for path, file := range tree {
			if file.origin != "" {
				continue
			}
			if existing, exists := merged[path]; exists {
				if existing.origin != "" || !sameReviewFile(existing, file) {
					return nil, fmt.Errorf("%s: conflicting file creations", path)
				}
				continue
			}
			if err := add(path, file); err != nil {
				return nil, err
			}
		}
	}
	return merged, nil
}

func mergeReviewFile(base, prior, upstream reviewFile, priorEdits, upstreamEdits []*gitdiff.TextFragment, preserveConflicts bool) (reviewFile, error) {
	result := prior
	if prior.mode == base.mode {
		result.mode = upstream.mode
	} else if upstream.mode != base.mode && prior.mode != upstream.mode {
		return reviewFile{}, fmt.Errorf("conflicting file modes")
	}
	switch {
	case sameReviewContent(prior, base):
		result.spans, result.oid, result.binaryID = upstream.spans, upstream.oid, upstream.binaryID
	case sameReviewContent(upstream, base), sameReviewContent(prior, upstream):
		// The prior content already contains the combined change.
	default:
		if base.binaryID != "" || prior.binaryID != "" || upstream.binaryID != "" {
			return reviewFile{}, fmt.Errorf("conflicting binary changes")
		}
		spans, err := mergeReviewSpans(base.spans, priorEdits, upstreamEdits, preserveConflicts)
		if err != nil {
			return reviewFile{}, err
		}
		result.spans, result.oid = spans, ""
	}
	return result, nil
}

type reviewEdit[T comparable] struct {
	start, end  int
	side        int
	replacement []T
}

func mergeReviewSpans(base []reviewSpan, prior, upstream []*gitdiff.TextFragment, preserveConflicts bool) ([]reviewSpan, error) {
	boundaries := map[int64]int{}
	position := int64(0)
	for i, span := range base {
		boundaries[position] = i
		if span.size < 0 {
			break
		}
		position += span.size
		if i == len(base)-1 {
			boundaries[position] = len(base)
		}
	}
	if len(base) == 0 {
		boundaries[0] = 0
	}
	var edits []reviewEdit[reviewSpan]
	for side, fragments := range [][]*gitdiff.TextFragment{prior, upstream} {
		for _, fragment := range fragments {
			position := reviewOffset(fragment.OldPosition, fragment.OldLines)
			var edit *reviewEdit[reviewSpan]
			for _, line := range fragment.Lines {
				if line.Old() && line.New() {
					edit = nil
					position++
					continue
				}
				if edit == nil {
					index, ok := boundaries[position]
					if !ok {
						return nil, fmt.Errorf("edit does not start on a base boundary")
					}
					edits = append(edits, reviewEdit[reviewSpan]{start: index, end: index, side: side})
					edit = &edits[len(edits)-1]
				}
				if line.Old() {
					position++
					end, ok := boundaries[position]
					if !ok {
						return nil, fmt.Errorf("edit does not end on a base boundary")
					}
					edit.end = end
				}
				if line.New() {
					edit.replacement = append(edit.replacement, reviewSpan{text: line.Line, size: 1})
				}
			}
		}
	}
	return mergeReviewEdits(base, edits, func(base, prior, upstream []reviewSpan) ([]reviewSpan, error) {
		return mergeReviewText(base, prior, upstream, preserveConflicts)
	})
}

func rebaseReviewDiff(review, baseChange string, preserveConflicts bool) (string, error) {
	if strings.TrimSpace(baseChange) == "" {
		return review, nil
	}
	trees, err := readReviewTrees(review, baseChange)
	if err != nil {
		return "", err
	}
	merged, err := mergeReviewTrees(trees, preserveConflicts)
	if err != nil {
		return "", err
	}
	return compareReviewTrees(trees.current, merged)
}
