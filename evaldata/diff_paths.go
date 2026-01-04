package evaldata

import (
	"regexp"
	"strings"
)

// Source constants for file path and line range discovery.
const (
	SourceReviewMergeDiff = "review_merge_diff"
	SourceToolCallArgs    = "tool_call_args"
	SourceToolCallResult  = "tool_call_result"
	SourceDiff            = "diff"
	SourceGoldenDiff      = "golden_diff"
)

// diffHeaderRegex matches "diff --git a/<path> b/<path>" lines.
// Uses (?m) for multiline mode so ^ and $ match line boundaries.
var diffHeaderRegex = regexp.MustCompile(`(?m)^diff --git a/(.+?) b/(.+?)$`)

// renameFromRegex matches "rename from <path>" lines.
var renameFromRegex = regexp.MustCompile(`^rename from (.+)$`)

// renameToRegex matches "rename to <path>" lines.
var renameToRegex = regexp.MustCompile(`^rename to (.+)$`)

// ParseDiffPaths extracts edited file paths from a unified diff string.
// It handles regular edits, new files, deleted files, and renames.
// Returns paths in the order they appear in the diff, de-duplicated.
func ParseDiffPaths(diff string) []string {
	if diff == "" {
		return nil
	}

	seen := make(map[string]bool)
	var paths []string

	lines := strings.Split(diff, "\n")
	for i, line := range lines {
		matches := diffHeaderRegex.FindStringSubmatch(line)
		if matches == nil {
			continue
		}

		aPath := matches[1]
		bPath := matches[2]

		// Look ahead for rename, new file, or deleted file indicators
		isNewFile := false
		isDeletedFile := false
		var renameFrom, renameTo string

		for j := i + 1; j < len(lines) && j < i+10; j++ {
			nextLine := lines[j]
			if strings.HasPrefix(nextLine, "diff --git") {
				break
			}
			if strings.HasPrefix(nextLine, "new file mode") {
				isNewFile = true
			}
			if strings.HasPrefix(nextLine, "deleted file mode") {
				isDeletedFile = true
			}
			if m := renameFromRegex.FindStringSubmatch(nextLine); m != nil {
				renameFrom = m[1]
			}
			if m := renameToRegex.FindStringSubmatch(nextLine); m != nil {
				renameTo = m[1]
			}
		}

		// Determine which paths to include
		var pathsToAdd []string

		if renameFrom != "" && renameTo != "" {
			// Rename: include both old and new paths
			pathsToAdd = append(pathsToAdd, normalizePath(renameFrom), normalizePath(renameTo))
		} else if isNewFile {
			// New file: use b path (a path is /dev/null)
			pathsToAdd = append(pathsToAdd, normalizePath(bPath))
		} else if isDeletedFile {
			// Deleted file: use a path (b path is /dev/null)
			pathsToAdd = append(pathsToAdd, normalizePath(aPath))
		} else {
			// Regular edit: a and b paths should be the same
			pathsToAdd = append(pathsToAdd, normalizePath(bPath))
		}

		for _, p := range pathsToAdd {
			if p != "" && !seen[p] {
				seen[p] = true
				paths = append(paths, p)
			}
		}
	}

	return paths
}

// normalizePath normalizes a file path by stripping common prefixes.
func normalizePath(path string) string {
	// Strip /dev/null
	if path == "/dev/null" {
		return ""
	}
	// Strip leading "./"
	path = strings.TrimPrefix(path, "./")
	return path
}

// ParseBulkSearchResultPaths extracts file paths from bulk_search_repository results.
// fileLineRegex matches ripgrep output format: "path/to/file.go:123:" or "path/to/file.go:123-"
var fileLineRegex = regexp.MustCompile(`^([^\s:]+\.[a-zA-Z0-9]+):(\d+)[:-]`)

// fileHeaderRegex matches "File: path/to/file.go" headers from read_file_lines/get_symbol_definitions
var fileHeaderRegex = regexp.MustCompile(`(?m)^File:\s*(.+)$`)

// ParseToolResultPaths extracts file paths from tool result output.
// Supports both ripgrep format (path:line:) and File: header format.
func ParseToolResultPaths(result string) []string {
	if result == "" {
		return nil
	}

	seen := make(map[string]bool)
	var paths []string

	// Parse File: headers
	fileMatches := fileHeaderRegex.FindAllStringSubmatch(result, -1)
	for _, match := range fileMatches {
		path := normalizePath(strings.TrimSpace(match[1]))
		if path != "" && !seen[path] {
			seen[path] = true
			paths = append(paths, path)
		}
	}

	// Parse ripgrep format (path:line:)
	lines := strings.Split(result, "\n")
	for _, line := range lines {
		matches := fileLineRegex.FindStringSubmatch(line)
		if matches != nil {
			path := normalizePath(matches[1])
			if path != "" && !seen[path] {
				seen[path] = true
				paths = append(paths, path)
			}
		}
	}

	return paths
}

// ParseBulkSearchResultPaths extracts file paths from bulk_search_repository results.
// Deprecated: Use ParseToolResultPaths instead.
func ParseBulkSearchResultPaths(result string) []string {
	return ParseToolResultPaths(result)
}

// ContainsDiff checks if a string contains unified diff content.
func ContainsDiff(s string) bool {
	return diffHeaderRegex.MatchString(s)
}

// hunkHeaderRegex matches unified diff hunk headers: @@ -start,count +start,count @@
// Examples: @@ -1,3 +1,4 @@, @@ -10 +10,2 @@, @@ -0,0 +1,5 @@
var hunkHeaderRegex = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@`)

// DiffHunk represents a single hunk from a unified diff.
type DiffHunk struct {
	OldStart int
	OldCount int
	NewStart int
	NewCount int
}

// ParseDiffHunks extracts file paths and their associated line ranges from a unified diff.
// Returns a map of file path to list of hunks (line ranges) for that file.
func ParseDiffHunks(diff string) map[string][]DiffHunk {
	if diff == "" {
		return nil
	}

	result := make(map[string][]DiffHunk)
	lines := strings.Split(diff, "\n")
	var currentPath string

	for i, line := range lines {
		// Check for diff header to get current file
		if matches := diffHeaderRegex.FindStringSubmatch(line); matches != nil {
			aPath := matches[1]
			bPath := matches[2]

			// Look ahead for rename, new file, or deleted file indicators
			isNewFile := false
			isDeletedFile := false
			var renameTo string

			for j := i + 1; j < len(lines) && j < i+10; j++ {
				nextLine := lines[j]
				if strings.HasPrefix(nextLine, "diff --git") {
					break
				}
				if strings.HasPrefix(nextLine, "new file mode") {
					isNewFile = true
				}
				if strings.HasPrefix(nextLine, "deleted file mode") {
					isDeletedFile = true
				}
				if m := renameToRegex.FindStringSubmatch(nextLine); m != nil {
					renameTo = m[1]
				}
			}

			// Determine the relevant path
			if renameTo != "" {
				currentPath = normalizePath(renameTo)
			} else if isNewFile {
				currentPath = normalizePath(bPath)
			} else if isDeletedFile {
				currentPath = normalizePath(aPath)
			} else {
				currentPath = normalizePath(bPath)
			}
			continue
		}

		// Check for hunk header
		if currentPath != "" {
			if matches := hunkHeaderRegex.FindStringSubmatch(line); matches != nil {
				hunk := DiffHunk{
					OldStart: parseInt(matches[1]),
					OldCount: 1,
					NewStart: parseInt(matches[3]),
					NewCount: 1,
				}
				if matches[2] != "" {
					hunk.OldCount = parseInt(matches[2])
				}
				if matches[4] != "" {
					hunk.NewCount = parseInt(matches[4])
				}
				result[currentPath] = append(result[currentPath], hunk)
			}
		}
	}

	return result
}

// parseInt parses a string to int, returning 0 on error.
func parseInt(s string) int {
	var n int
	for _, c := range s {
		if c >= '0' && c <= '9' {
			n = n*10 + int(c-'0')
		}
	}
	return n
}

// DiffHunksToLineRanges converts diff hunks to FileLineRange entries.
// Uses the "new" side of the diff (the +start,count) as the relevant lines.
func DiffHunksToLineRanges(hunks map[string][]DiffHunk, source string) []FileLineRange {
	if len(hunks) == 0 {
		return nil
	}

	var ranges []FileLineRange
	// Sort paths for deterministic output
	var paths []string
	for path := range hunks {
		paths = append(paths, path)
	}
	sortStrings(paths)

	for _, path := range paths {
		for _, hunk := range hunks[path] {
			// For new files or additions, use the new side
			// For deletions (NewCount=0), we still record the range as it indicates relevant context
			startLine := hunk.NewStart
			endLine := hunk.NewStart + hunk.NewCount - 1
			if hunk.NewCount == 0 {
				// Deletion: use old side to indicate where lines were removed
				startLine = hunk.OldStart
				endLine = hunk.OldStart + hunk.OldCount - 1
			}
			if endLine < startLine {
				endLine = startLine
			}
			ranges = append(ranges, FileLineRange{
				Path:      path,
				StartLine: startLine,
				EndLine:   endLine,
				Sources:   []string{source},
			})
		}
	}

	return ranges
}

// sortStrings sorts a slice of strings in place.
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
