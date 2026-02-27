package diffanalysis

import (
	"strings"
)

// computeKeptHunks runs the filtering logic on zero-context diffs and returns,
// per file path, the list of hunks that should be kept.
func computeKeptHunks(sinceReviewDiff, branchDiff, baseSinceReviewDiff string) (map[string][]Hunk, error) {
	sinceReviewFiles, err := ParseUnifiedDiff(sinceReviewDiff)
	if err != nil {
		return nil, err
	}
	branchFiles, err := ParseUnifiedDiff(branchDiff)
	if err != nil {
		return nil, err
	}
	baseFiles, err := ParseUnifiedDiff(baseSinceReviewDiff)
	if err != nil {
		return nil, err
	}

	branchHunksByPath := make(map[string][]Hunk, len(branchFiles))
	for _, f := range branchFiles {
		branchHunksByPath[f.NewPath] = f.Hunks
	}

	baseHunksByPath := make(map[string][]Hunk, len(baseFiles))
	for _, f := range baseFiles {
		baseHunksByPath[f.NewPath] = f.Hunks
	}

	result := make(map[string][]Hunk)
	for _, file := range sinceReviewFiles {
		baseHunks := baseHunksByPath[file.NewPath]
		branchHunks := branchHunksByPath[file.NewPath]

		for _, h := range file.Hunks {
			if isMergeIntroduced(h, baseHunks, branchHunks) {
				continue
			}
			result[file.NewPath] = append(result[file.NewPath], h)
		}
	}
	return result, nil
}

// FilterDiffForReview filters displayDiff to remove merge-introduced changes.
// It uses sinceReviewDiff for overlap decisions (should use zero-context for
// accuracy) and outputs hunks from displayDiff (which may have richer context).
//
// A displayDiff hunk is kept if its new-file range overlaps any kept
// sinceReviewDiff hunk for the same file. A sinceReview hunk is kept unless it
// overlaps a baseSinceReviewDiff hunk without also overlapping a branchDiff hunk.
//
// branchDiff is the three-dot diff (main...HEAD) showing our branch's unique
// changes. baseSinceReviewDiff is the diff from the review tree to the base
// branch tip, showing what the base branch changed since the review.
func FilterDiffForReview(sinceReviewDiff, branchDiff, baseSinceReviewDiff, displayDiff string) (string, error) {
	keptHunks, err := computeKeptHunks(sinceReviewDiff, branchDiff, baseSinceReviewDiff)
	if err != nil {
		return "", err
	}

	displayFiles, err := ParseUnifiedDiff(displayDiff)
	if err != nil {
		return "", err
	}

	var result strings.Builder
	for _, file := range displayFiles {
		kept := keptHunks[file.NewPath]
		if len(kept) == 0 {
			continue
		}
		var filteredHunks []Hunk
		for _, dh := range file.Hunks {
			if overlapsAnyNew(dh, kept) {
				filteredHunks = append(filteredHunks, dh)
			}
		}
		if len(filteredHunks) == 0 {
			continue
		}
		result.WriteString(formatFileHeader(file))
		for _, h := range filteredHunks {
			result.WriteString(formatHunkContent(h))
		}
	}
	return result.String(), nil
}

// isMergeIntroduced returns true if a sinceReview hunk was introduced by
// merging the base branch and is not part of our branch's own work.
// It checks old-side overlap with base (they share the review-tree origin)
// and new-side non-overlap with branch (our unique changes).
func isMergeIntroduced(h Hunk, baseHunks, branchHunks []Hunk) bool {
	return overlapsAnyOld(h, baseHunks) && !overlapsAnyNew(h, branchHunks)
}

// rangesOverlap returns true if [aStart, aStart+aCount) overlaps [bStart, bStart+bCount).
// Zero-count ranges (pure deletions/additions) are treated as one line wide.
func rangesOverlap(aStart, aCount, bStart, bCount int) bool {
	aEnd := aStart + aCount
	if aCount == 0 {
		aEnd = aStart + 1
	}
	bEnd := bStart + bCount
	if bCount == 0 {
		bEnd = bStart + 1
	}
	return aStart < bEnd && bStart < aEnd
}

// overlapsAnyNew returns true if h's new-file range overlaps any hunk's new-file range.
func overlapsAnyNew(h Hunk, hunks []Hunk) bool {
	for _, other := range hunks {
		if rangesOverlap(h.NewStart, h.NewCount, other.NewStart, other.NewCount) {
			return true
		}
	}
	return false
}

// overlapsAnyOld returns true if h's old-file range overlaps any hunk's old-file range.
func overlapsAnyOld(h Hunk, hunks []Hunk) bool {
	for _, other := range hunks {
		if rangesOverlap(h.OldStart, h.OldCount, other.OldStart, other.OldCount) {
			return true
		}
	}
	return false
}

// formatFileHeader reconstructs the diff header lines for a FileDiff.
func formatFileHeader(fd FileDiff) string {
	var sb strings.Builder
	sb.WriteString("diff --git a/")
	sb.WriteString(fd.OldPath)
	sb.WriteString(" b/")
	sb.WriteString(fd.NewPath)
	sb.WriteString("\n")
	if fd.IsNewFile {
		sb.WriteString("new file mode 100644\n")
		sb.WriteString("--- /dev/null\n")
	} else if fd.IsDeleted {
		sb.WriteString("deleted file mode 100644\n")
		sb.WriteString("--- a/")
		sb.WriteString(fd.OldPath)
		sb.WriteString("\n")
	} else {
		sb.WriteString("--- a/")
		sb.WriteString(fd.OldPath)
		sb.WriteString("\n")
	}
	if fd.IsDeleted {
		sb.WriteString("+++ /dev/null\n")
	} else {
		sb.WriteString("+++ b/")
		sb.WriteString(fd.NewPath)
		sb.WriteString("\n")
	}
	return sb.String()
}

// formatHunkContent formats a hunk back to unified diff format.
func formatHunkContent(h Hunk) string {
	var sb strings.Builder
	sb.WriteString(h.RawHeader)
	sb.WriteString("\n")
	for _, line := range h.Lines {
		switch line.Type {
		case LineContext:
			sb.WriteString(" ")
		case LineAdded:
			sb.WriteString("+")
		case LineRemoved:
			sb.WriteString("-")
		}
		sb.WriteString(line.Content)
		sb.WriteString("\n")
	}
	return sb.String()
}
