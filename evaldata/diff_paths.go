package evaldata

import (
	"regexp"
	"strings"
)

// Source constants for file path discovery.
const (
	SourceReviewMergeDiff = "review_merge_diff"
	SourceToolCallArgs    = "tool_call_args"
	SourceToolCallResult  = "tool_call_result"
	SourceDiff            = "diff"
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
