package diffanalysis

import (
	"strings"
)

// FilterDiffForReview filters sinceReviewDiff to remove merge-introduced
// changes. It uses hunk-level filtering within each file:
//   - A sinceReview hunk that overlaps a baseSinceReviewDiff hunk (base branch
//     changed those lines) is dropped, UNLESS it also overlaps a branchDiff hunk
//     (our branch independently changed the same lines — convergence).
//   - All other sinceReview hunks are kept.
//
// branchDiff is the three-dot diff (main...HEAD) showing our branch's unique
// changes. baseSinceReviewDiff is the diff from the review tree to the base
// branch tip, showing what the base branch changed since the review.
func FilterDiffForReview(sinceReviewDiff, branchDiff, baseSinceReviewDiff string) (string, error) {
	sinceReviewFiles, err := ParseUnifiedDiff(sinceReviewDiff)
	if err != nil {
		return "", err
	}
	branchFiles, err := ParseUnifiedDiff(branchDiff)
	if err != nil {
		return "", err
	}
	baseFiles, err := ParseUnifiedDiff(baseSinceReviewDiff)
	if err != nil {
		return "", err
	}

	branchHunksByPath := make(map[string][]Hunk, len(branchFiles))
	for _, f := range branchFiles {
		branchHunksByPath[f.NewPath] = f.Hunks
	}

	baseHunksByPath := make(map[string][]Hunk, len(baseFiles))
	for _, f := range baseFiles {
		baseHunksByPath[f.NewPath] = f.Hunks
	}

	var result strings.Builder
	for _, file := range sinceReviewFiles {
		baseHunks := baseHunksByPath[file.NewPath]
		branchHunks := branchHunksByPath[file.NewPath]

		var kept []Hunk
		for _, h := range file.Hunks {
			// Compare on old side for base (both diffs share the review tree as old),
			// and on new side for branch (three-dot diff new side = HEAD = our new side).
			if overlapsAnyOld(h, baseHunks) && !overlapsAnyNew(h, branchHunks) {
				// Merge-introduced hunk: base changed it but we didn't
				continue
			}
			kept = append(kept, h)
		}
		if len(kept) == 0 {
			continue
		}
		result.WriteString(formatFileHeader(file))
		for _, h := range kept {
			result.WriteString(formatHunkContent(h))
		}
	}

	return result.String(), nil
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
