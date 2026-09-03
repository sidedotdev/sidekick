package diffanalysis

import "strings"

// FilterHunksByOldLineRanges keeps only the hunks whose changed lines touch one
// of the given old-side (pre-image) line ranges for that file. Files without any
// recorded ranges are passed through untouched, so an empty map yields the input
// diff unchanged. Unparseable input is returned unchanged along with the error.
func FilterHunksByOldLineRanges(diff string, rangesByPath map[string][]LineRange) (string, error) {
	if diff == "" || len(rangesByPath) == 0 {
		return diff, nil
	}

	files, err := ParseUnifiedDiff(diff)
	if err != nil {
		return diff, err
	}
	if len(files) == 0 {
		return diff, nil
	}

	var out strings.Builder
	for _, file := range files {
		ranges, ok := rangesForFile(rangesByPath, file)
		if !ok {
			out.WriteString(ensureTrailingNewline(file.RawContent))
			continue
		}

		var kept []Hunk
		for _, hunk := range file.Hunks {
			if hunkTouchesRanges(hunk, ranges) {
				kept = append(kept, hunk)
			}
		}
		if len(kept) == 0 {
			continue
		}
		out.WriteString(renderFileDiff(file, kept))
	}
	return out.String(), nil
}

func rangesForFile(rangesByPath map[string][]LineRange, file FileDiff) ([]LineRange, bool) {
	for _, path := range []string{file.OldPath, file.NewPath} {
		if path == "" {
			continue
		}
		if ranges, ok := rangesByPath[path]; ok {
			return ranges, true
		}
	}
	return nil, false
}

// hunkTouchesRanges reports whether the hunk's changed lines fall within any of
// the ranges, using old-side line numbers. Added lines are attributed to their
// insertion point in the old file, since they have no old-side line of their own.
func hunkTouchesRanges(hunk Hunk, ranges []LineRange) bool {
	oldLine := hunk.OldStart
	for _, line := range hunk.Lines {
		switch line.Type {
		case LineContext:
			oldLine++
		case LineRemoved:
			if lineInRanges(oldLine, ranges) {
				return true
			}
			oldLine++
		case LineAdded:
			if lineInRanges(oldLine, ranges) {
				return true
			}
		}
	}
	return false
}

func lineInRanges(line int, ranges []LineRange) bool {
	for _, r := range ranges {
		if line >= r.Start && line < r.End {
			return true
		}
	}
	return false
}
