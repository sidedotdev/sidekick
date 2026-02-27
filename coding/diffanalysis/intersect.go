package diffanalysis

import (
	"strings"
)

// IntersectDiffs returns hunks from diffA that overlap (by new-file line
// ranges) with hunks in diffB for the same file. This is useful for keeping
// only the hunks from an incremental diff (A) that correspond to our branch's
// own work (B), filtering out content introduced by merging another branch.
func IntersectDiffs(diffA, diffB string) (string, error) {
	filesA, err := ParseUnifiedDiff(diffA)
	if err != nil {
		return "", err
	}
	filesB, err := ParseUnifiedDiff(diffB)
	if err != nil {
		return "", err
	}

	// Index diffB files by path for quick lookup
	bFilesByPath := make(map[string]*FileDiff, len(filesB))
	for i := range filesB {
		bFilesByPath[filesB[i].NewPath] = &filesB[i]
	}

	var result strings.Builder
	for _, fileA := range filesA {
		fileB, exists := bFilesByPath[fileA.NewPath]
		if !exists {
			// File not in our branch's diff — skip entirely (came from merge)
			continue
		}

		kept := filterOverlappingHunks(fileA.Hunks, fileB.Hunks)
		if len(kept) == 0 {
			continue
		}

		// Reconstruct file header and kept hunks
		result.WriteString(formatFileHeader(fileA))
		for _, h := range kept {
			result.WriteString(formatHunkContent(h))
		}
	}

	return result.String(), nil
}

// hunksOverlap returns true if two hunks have overlapping new-file line ranges.
// Zero-count ranges (pure deletions) are treated as occupying one line so they
// can still match against hunks at the same position.
func hunksOverlap(a, b Hunk) bool {
	aCount := a.NewCount
	if aCount == 0 {
		aCount = 1
	}
	bCount := b.NewCount
	if bCount == 0 {
		bCount = 1
	}
	return a.NewStart < b.NewStart+bCount && b.NewStart < a.NewStart+aCount
}

// filterOverlappingHunks returns hunks from listA that overlap with any hunk in listB.
func filterOverlappingHunks(hunksA, hunksB []Hunk) []Hunk {
	var kept []Hunk
	for _, hA := range hunksA {
		for _, hB := range hunksB {
			if hunksOverlap(hA, hB) {
				kept = append(kept, hA)
				break
			}
		}
	}
	return kept
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
