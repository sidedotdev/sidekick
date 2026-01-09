package diffanalysis

import (
	"fmt"
	"strings"
)

// ReversePatch applies a diff in reverse to reconstruct the original content
// from the current (new) content. This is useful for getting the "before" state
// of a file given its current state and the diff that was applied.
func ReversePatch(currentContent string, fileDiff FileDiff) (string, error) {
	if fileDiff.IsNewFile {
		// File didn't exist before, return empty
		return "", nil
	}
	if fileDiff.IsDeleted {
		// For deleted files, we need to reconstruct from the diff's removed lines
		return reconstructDeletedFile(fileDiff), nil
	}
	if fileDiff.IsBinary {
		return "", fmt.Errorf("cannot reverse patch binary file")
	}
	if len(fileDiff.Hunks) == 0 {
		// No changes, return as-is
		return currentContent, nil
	}

	currentLines := splitLines(currentContent)
	var oldLines []string

	// Process hunks in order, tracking position in current content
	currentIdx := 0 // 0-indexed position in currentLines

	for _, hunk := range fileDiff.Hunks {
		// Copy unchanged lines before this hunk
		hunkNewStart := hunk.NewStart - 1 // Convert to 0-indexed
		for currentIdx < hunkNewStart && currentIdx < len(currentLines) {
			oldLines = append(oldLines, currentLines[currentIdx])
			currentIdx++
		}

		// Process hunk lines to reconstruct old content
		for _, line := range hunk.Lines {
			switch line.Type {
			case LineContext:
				// Context line exists in both old and new
				if currentIdx < len(currentLines) {
					oldLines = append(oldLines, currentLines[currentIdx])
					currentIdx++
				}
			case LineAdded:
				// Added line only exists in new, skip it
				currentIdx++
			case LineRemoved:
				// Removed line only exists in old, add it back
				oldLines = append(oldLines, line.Content)
			}
		}
	}

	// Copy remaining lines after last hunk
	for currentIdx < len(currentLines) {
		oldLines = append(oldLines, currentLines[currentIdx])
		currentIdx++
	}

	return joinLines(oldLines), nil
}

// reconstructDeletedFile reconstructs a deleted file from its diff.
func reconstructDeletedFile(fileDiff FileDiff) string {
	var lines []string
	for _, hunk := range fileDiff.Hunks {
		for _, line := range hunk.Lines {
			if line.Type == LineRemoved {
				lines = append(lines, line.Content)
			}
		}
	}
	return joinLines(lines)
}

// splitLines splits content into lines, preserving empty trailing line info.
func splitLines(content string) []string {
	if content == "" {
		return nil
	}
	lines := strings.Split(content, "\n")
	// Remove trailing empty string if content ended with newline
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// joinLines joins lines back into content with newlines.
func joinLines(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n") + "\n"
}

// HunkLineMapping provides mapping information between old and new line numbers
// for a single hunk, useful for determining which symbols were affected.
type HunkLineMapping struct {
	// OldRange is the range of lines in the old file affected by this hunk
	OldRange LineRange
	// NewRange is the range of lines in the new file affected by this hunk
	NewRange LineRange
	// LineDelta is the net change in line count (positive = lines added, negative = lines removed)
	LineDelta int
}

// GetHunkMappings returns line mapping information for all hunks in a file diff.
// This is useful for mapping line numbers between old and new versions of a file.
func (fd *FileDiff) GetHunkMappings() []HunkLineMapping {
	var mappings []HunkLineMapping

	for _, hunk := range fd.Hunks {
		mapping := HunkLineMapping{
			OldRange: LineRange{
				Start: hunk.OldStart,
				End:   hunk.OldStart + hunk.OldCount,
			},
			NewRange: LineRange{
				Start: hunk.NewStart,
				End:   hunk.NewStart + hunk.NewCount,
			},
			LineDelta: hunk.NewCount - hunk.OldCount,
		}
		mappings = append(mappings, mapping)
	}

	return mappings
}

// MapOldLineToNew converts an old file line number to the corresponding new file
// line number, accounting for all hunks that come before it.
// Returns the mapped line number and a boolean indicating if the line still exists
// (false if the line was deleted).
func (fd *FileDiff) MapOldLineToNew(oldLine int) (newLine int, exists bool) {
	cumulativeDelta := 0

	for _, hunk := range fd.Hunks {
		hunkOldEnd := hunk.OldStart + hunk.OldCount

		if oldLine < hunk.OldStart {
			// Line is before this hunk, apply accumulated delta
			return oldLine + cumulativeDelta, true
		}

		if oldLine < hunkOldEnd {
			// Line is within this hunk, need to check if it was deleted
			lineOffset := oldLine - hunk.OldStart
			oldLinesProcessed := 0

			for _, line := range hunk.Lines {
				if line.Type == LineRemoved || line.Type == LineContext {
					if oldLinesProcessed == lineOffset {
						if line.Type == LineRemoved {
							return 0, false
						}
						// Context line, find its new position
						return line.NewLine, true
					}
					oldLinesProcessed++
				}
			}
			// Shouldn't reach here, but return deleted as fallback
			return 0, false
		}

		// Line is after this hunk, accumulate delta
		cumulativeDelta += hunk.NewCount - hunk.OldCount
	}

	// Line is after all hunks
	return oldLine + cumulativeDelta, true
}

// MapNewLineToOld converts a new file line number to the corresponding old file
// line number, accounting for all hunks that come before it.
// Returns the mapped line number and a boolean indicating if the line existed
// in the old file (false if the line was added).
func (fd *FileDiff) MapNewLineToOld(newLine int) (oldLine int, existed bool) {
	cumulativeDelta := 0

	for _, hunk := range fd.Hunks {
		hunkNewEnd := hunk.NewStart + hunk.NewCount

		if newLine < hunk.NewStart {
			// Line is before this hunk, apply accumulated delta
			return newLine - cumulativeDelta, true
		}

		if newLine < hunkNewEnd {
			// Line is within this hunk, need to check if it was added
			lineOffset := newLine - hunk.NewStart
			newLinesProcessed := 0

			for _, line := range hunk.Lines {
				if line.Type == LineAdded || line.Type == LineContext {
					if newLinesProcessed == lineOffset {
						if line.Type == LineAdded {
							return 0, false
						}
						// Context line, find its old position
						return line.OldLine, true
					}
					newLinesProcessed++
				}
			}
			// Shouldn't reach here, but return as added as fallback
			return 0, false
		}

		// Line is after this hunk, accumulate delta
		cumulativeDelta += hunk.NewCount - hunk.OldCount
	}

	// Line is after all hunks
	return newLine - cumulativeDelta, true
}
