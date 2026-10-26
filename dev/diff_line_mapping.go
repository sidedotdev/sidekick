package dev

import (
	"regexp"
	"strconv"
	"strings"
)

// unifiedHunkHeaderPattern accepts omitted counts (eg "@@ -4 +5,3 @@"), which
// git emits for single-line ranges.
var unifiedHunkHeaderPattern = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+\d+(?:,\d+)? @@`)

// diffEdit is one run of consecutive changed lines: `removed` old lines
// starting at 1-based `oldStart` were replaced by `added` new lines.
type diffEdit struct {
	oldStart int
	removed  int
	added    int
}

// parseUnifiedDiffEdits walks the hunks of a unified diff for a single file
// and returns its edits in old-file order.
func parseUnifiedDiffEdits(diff string) []diffEdit {
	var edits []diffEdit
	var current *diffEdit
	flush := func() {
		if current != nil {
			edits = append(edits, *current)
			current = nil
		}
	}

	oldLine := 0
	inHunk := false
	for _, line := range strings.Split(diff, "\n") {
		if match := unifiedHunkHeaderPattern.FindStringSubmatch(line); match != nil {
			flush()
			oldStart, _ := strconv.Atoi(match[1])
			oldCount := 1
			if match[2] != "" {
				oldCount, _ = strconv.Atoi(match[2])
			}
			// a zero-count hunk names the line *before* the insertion
			oldLine = oldStart
			if oldCount == 0 {
				oldLine = oldStart + 1
			}
			inHunk = true
			continue
		}
		if strings.HasPrefix(line, "diff ") {
			flush()
			inHunk = false
			continue
		}
		if !inHunk {
			continue
		}

		switch {
		case strings.HasPrefix(line, "-"):
			if current == nil {
				current = &diffEdit{oldStart: oldLine}
			}
			current.removed++
			oldLine++
		case strings.HasPrefix(line, "+"):
			if current == nil {
				current = &diffEdit{oldStart: oldLine}
			}
			current.added++
		case strings.HasPrefix(line, "\\"):
			// "\ No newline at end of file" carries no line
		default:
			flush()
			oldLine++
		}
	}
	flush()
	return edits
}

// mapOldLineToNew translates a 1-based line number of the pre-diff file to the
// post-diff file. Lines inside a replaced region map to the replacement's
// first line, or its last line when clampToEnd is set, so a range spanning a
// replacement still covers it.
func mapOldLineToNew(line int, edits []diffEdit, clampToEnd bool) int {
	shift := 0
	for _, edit := range edits {
		if line < edit.oldStart {
			break
		}
		if line < edit.oldStart+edit.removed {
			if clampToEnd {
				return edit.oldStart + shift + edit.added - 1
			}
			return edit.oldStart + shift
		}
		shift += edit.added - edit.removed
	}
	return line + shift
}

// shiftFileRangesByDiff rebases file ranges expressed against the file before
// the given diff onto the file after it. Ranges for other files are untouched.
func shiftFileRangesByDiff(fileRanges []FileRange, filePath string, diff string) []FileRange {
	edits := parseUnifiedDiffEdits(diff)
	if len(edits) == 0 {
		return fileRanges
	}
	shifted := make([]FileRange, len(fileRanges))
	for i, fileRange := range fileRanges {
		if fileRange.FilePath != filePath {
			shifted[i] = fileRange
			continue
		}
		shifted[i] = FileRange{
			FilePath:  fileRange.FilePath,
			StartLine: mapOldLineToNew(fileRange.StartLine, edits, false),
			EndLine:   mapOldLineToNew(fileRange.EndLine, edits, true),
		}
	}
	return shifted
}
