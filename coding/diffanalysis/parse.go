// Package diffanalysis provides utilities for parsing and analyzing unified diffs.
package diffanalysis

import (
	"regexp"
	"strings"
)

// FileDiff represents a single file's diff within a unified diff.
type FileDiff struct {
	OldPath    string
	NewPath    string
	IsNewFile  bool
	IsDeleted  bool
	IsBinary   bool
	Hunks      []Hunk
	RawContent string
}

// Hunk represents a single hunk within a file diff.
type Hunk struct {
	OldStart  int
	OldCount  int
	NewStart  int
	NewCount  int
	Context   string // Optional context text after @@ (e.g., function name)
	RawHeader string // The original hunk header line (e.g., "@@ -1,3 +1,4 @@ func foo()")
	Lines     []DiffLine
}

// DiffLine represents a single line within a hunk.
type DiffLine struct {
	Type    LineType
	Content string
	OldLine int // 1-indexed line number in old file, 0 if not applicable
	NewLine int // 1-indexed line number in new file, 0 if not applicable
}

// LineType indicates whether a line is context, added, or removed.
type LineType int

const (
	LineContext LineType = iota
	LineAdded
	LineRemoved
)

var (
	diffHeaderRegex = regexp.MustCompile(`^diff --git a/(.+) b/(.+)$`)
	oldFileRegex    = regexp.MustCompile(`^--- (?:a/)?(.+)$`)
	newFileRegex    = regexp.MustCompile(`^\+\+\+ (?:b/)?(.+)$`)
	hunkHeaderRegex = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@(.*)$`)
)

// ParseUnifiedDiff parses a unified diff string into a slice of FileDiff entries.
func ParseUnifiedDiff(diff string) ([]FileDiff, error) {
	if diff == "" {
		return nil, nil
	}

	lines := strings.Split(diff, "\n")
	var files []FileDiff
	var currentFile *FileDiff
	var currentHunk *Hunk
	var oldLineNum, newLineNum int

	// Track the start of current file's raw content
	var fileStartIdx int

	for i := 0; i < len(lines); i++ {
		line := lines[i]

		// Check for diff header (git format)
		if matches := diffHeaderRegex.FindStringSubmatch(line); matches != nil {
			// Save previous file if exists
			if currentFile != nil {
				if currentHunk != nil {
					currentFile.Hunks = append(currentFile.Hunks, *currentHunk)
				}
				currentFile.RawContent = strings.Join(lines[fileStartIdx:i], "\n")
				files = append(files, *currentFile)
			}

			currentFile = &FileDiff{
				OldPath: matches[1],
				NewPath: matches[2],
			}
			currentHunk = nil
			fileStartIdx = i
			continue
		}

		// Check for old file header
		if matches := oldFileRegex.FindStringSubmatch(line); matches != nil {
			if currentFile != nil {
				path := matches[1]
				if path == "/dev/null" {
					currentFile.IsNewFile = true
				} else {
					currentFile.OldPath = path
				}
			}
			continue
		}

		// Check for new file header
		if matches := newFileRegex.FindStringSubmatch(line); matches != nil {
			if currentFile != nil {
				path := matches[1]
				if path == "/dev/null" {
					currentFile.IsDeleted = true
				} else {
					currentFile.NewPath = path
				}
			}
			continue
		}

		// Check for binary file indicator
		if strings.HasPrefix(line, "Binary files") {
			if currentFile != nil {
				currentFile.IsBinary = true
			}
			continue
		}

		// Check for hunk header
		if matches := hunkHeaderRegex.FindStringSubmatch(line); matches != nil {
			if currentFile != nil {
				if currentHunk != nil {
					currentFile.Hunks = append(currentFile.Hunks, *currentHunk)
				}

				oldStart := parseInt(matches[1])
				oldCount := 1
				if matches[2] != "" {
					oldCount = parseInt(matches[2])
				}
				newStart := parseInt(matches[3])
				newCount := 1
				if matches[4] != "" {
					newCount = parseInt(matches[4])
				}

				context := ""
				if len(matches) > 5 {
					context = strings.TrimSpace(matches[5])
				}

				currentHunk = &Hunk{
					OldStart:  oldStart,
					OldCount:  oldCount,
					NewStart:  newStart,
					NewCount:  newCount,
					Context:   context,
					RawHeader: line,
				}
				oldLineNum = oldStart
				newLineNum = newStart
			}
			continue
		}

		// Process diff lines within a hunk
		if currentHunk != nil && len(line) > 0 {
			switch line[0] {
			case ' ':
				currentHunk.Lines = append(currentHunk.Lines, DiffLine{
					Type:    LineContext,
					Content: line[1:],
					OldLine: oldLineNum,
					NewLine: newLineNum,
				})
				oldLineNum++
				newLineNum++
			case '+':
				currentHunk.Lines = append(currentHunk.Lines, DiffLine{
					Type:    LineAdded,
					Content: line[1:],
					NewLine: newLineNum,
				})
				newLineNum++
			case '-':
				currentHunk.Lines = append(currentHunk.Lines, DiffLine{
					Type:    LineRemoved,
					Content: line[1:],
					OldLine: oldLineNum,
				})
				oldLineNum++
			case '\\':
				// "\ No newline at end of file" - skip
				continue
			}
		}
	}

	// Save last file
	if currentFile != nil {
		if currentHunk != nil {
			currentFile.Hunks = append(currentFile.Hunks, *currentHunk)
		}
		currentFile.RawContent = strings.Join(lines[fileStartIdx:], "\n")
		files = append(files, *currentFile)
	}

	return files, nil
}

func parseInt(s string) int {
	var n int
	for _, c := range s {
		n = n*10 + int(c-'0')
	}
	return n
}

// ChangedLineRanges returns the ranges of lines that were changed in the new file.
// Each range is [start, end) where start is inclusive and end is exclusive.
// Line numbers are 1-indexed.
type LineRange struct {
	Start int // inclusive, 1-indexed
	End   int // exclusive, 1-indexed
}

// GetChangedLineRanges returns the line ranges in the new file that contain changes.
func (fd *FileDiff) GetChangedLineRanges() []LineRange {
	var ranges []LineRange

	for _, hunk := range fd.Hunks {
		var rangeStart int
		inRange := false

		for _, line := range hunk.Lines {
			if line.Type == LineAdded {
				if !inRange {
					rangeStart = line.NewLine
					inRange = true
				}
			} else if inRange {
				ranges = append(ranges, LineRange{Start: rangeStart, End: line.NewLine})
				inRange = false
			}
		}

		// Close any open range at end of hunk
		if inRange {
			// Find the last added line's number
			for i := len(hunk.Lines) - 1; i >= 0; i-- {
				if hunk.Lines[i].Type == LineAdded {
					ranges = append(ranges, LineRange{Start: rangeStart, End: hunk.Lines[i].NewLine + 1})
					break
				}
			}
		}
	}

	return ranges
}
