package evaldata

import (
	"encoding/json"
	"regexp"
	"strings"

	"sidekick/domain"
)

// ExtractLineRanges extracts ranked line ranges from a case.
// Primary (golden) ranges come from the merge approval diff hunks.
// Secondary ranges come from tool call args and results.
func ExtractLineRanges(c Case) []FileLineRange {
	rangeMap := make(map[string]*FileLineRange)
	var orderedKeys []string

	// Helper to add a range with a source (merges overlapping ranges)
	addRange := func(path string, startLine, endLine int, source string) {
		path = normalizePath(path)
		if path == "" || startLine <= 0 {
			return
		}
		if endLine < startLine {
			endLine = startLine
		}

		key := rangeKey(path, startLine, endLine)
		if existing, ok := rangeMap[key]; ok {
			// Add source if not already present
			for _, s := range existing.Sources {
				if s == source {
					return
				}
			}
			existing.Sources = append(existing.Sources, source)
		} else {
			rangeMap[key] = &FileLineRange{
				Path:      path,
				StartLine: startLine,
				EndLine:   endLine,
				Sources:   []string{source},
			}
			orderedKeys = append(orderedKeys, key)
		}
	}

	// 1. Primary (golden): extract from merge approval diff hunks
	goldenRanges := extractMergeApprovalLineRanges(c)
	for _, r := range goldenRanges {
		addRange(r.Path, r.StartLine, r.EndLine, SourceGoldenDiff)
	}

	// 2. Secondary: tool call args and results (in action order)
	for _, action := range c.Actions {
		if action.ActionType == ActionTypeMergeApproval {
			continue
		}

		if !strings.HasPrefix(action.ActionType, ToolCallActionPrefix) {
			continue
		}

		toolName := strings.TrimPrefix(action.ActionType, ToolCallActionPrefix)
		if !ContextToolNames[toolName] {
			continue
		}

		// Extract from tool call arguments
		argRanges := extractLineRangesFromToolCallArgs(toolName, action.ActionParams)
		for _, r := range argRanges {
			addRange(r.Path, r.StartLine, r.EndLine, SourceToolCallArgs)
		}

		// Extract from tool call results
		if action.ActionResult != "" {
			resultRanges := extractLineRangesFromToolResult(action.ActionResult)
			for _, r := range resultRanges {
				addRange(r.Path, r.StartLine, r.EndLine, SourceToolCallResult)
			}
		}
	}

	// Build result in order
	if len(orderedKeys) == 0 {
		return nil
	}
	result := make([]FileLineRange, 0, len(orderedKeys))
	for _, key := range orderedKeys {
		result = append(result, *rangeMap[key])
	}

	return result
}

// rangeKey creates a unique key for a line range.
func rangeKey(path string, startLine, endLine int) string {
	return path + ":" + itoa(startLine) + "-" + itoa(endLine)
}

// itoa converts int to string without importing strconv.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

// extractMergeApprovalLineRanges extracts line ranges from the merge approval diff.
func extractMergeApprovalLineRanges(c Case) []FileLineRange {
	mergeAction := GetMergeApprovalAction(c)
	if mergeAction == nil {
		return nil
	}

	mergeApprovalInfo, ok := mergeAction.ActionParams["mergeApprovalInfo"].(map[string]interface{})
	if !ok {
		return nil
	}

	diff, ok := mergeApprovalInfo["diff"].(string)
	if !ok {
		return nil
	}

	hunks := ParseDiffHunks(diff)
	return DiffHunksToLineRanges(hunks, SourceGoldenDiff)
}

// extractLineRangesFromToolCallArgs extracts line ranges from tool call arguments.
func extractLineRangesFromToolCallArgs(toolName string, params map[string]interface{}) []FileLineRange {
	if params == nil {
		return nil
	}

	paramsJson, err := json.Marshal(params)
	if err != nil {
		return nil
	}

	var ranges []FileLineRange

	switch toolName {
	case ToolNameReadFileLines:
		var args ReadFileLinesArgs
		if err := json.Unmarshal(paramsJson, &args); err != nil {
			return nil
		}
		windowSize := args.WindowSize
		if windowSize <= 0 {
			windowSize = 50 // default window size
		}
		for _, fl := range args.FileLines {
			if fl.FilePath != "" && fl.LineNumber > 0 {
				// The tool reads windowSize lines centered on LineNumber
				halfWindow := windowSize / 2
				startLine := fl.LineNumber - halfWindow
				if startLine < 1 {
					startLine = 1
				}
				endLine := fl.LineNumber + halfWindow
				ranges = append(ranges, FileLineRange{
					Path:      fl.FilePath,
					StartLine: startLine,
					EndLine:   endLine,
					Sources:   []string{SourceToolCallArgs},
				})
			}
		}

	case ToolNameGetSymbolDefinitions:
		// get_symbol_definitions doesn't have explicit line numbers in args,
		// but we can extract them from results
		// For args, we just note the file was accessed (will be refined by results)

	case ToolNameBulkSearchRepository:
		// bulk_search_repository uses globs, line ranges come from results
	}

	return ranges
}

// lineRangeFromResultRegex matches "Lines: X-Y" or "Lines: X" patterns in tool results.
var lineRangeFromResultRegex = regexp.MustCompile(`(?m)^Lines:\s*(\d+)(?:-(\d+))?`)

// extractLineRangesFromToolResult extracts line ranges from tool result output.
func extractLineRangesFromToolResult(result string) []FileLineRange {
	if result == "" {
		return nil
	}

	var ranges []FileLineRange
	var currentPath string

	lines := strings.Split(result, "\n")
	for _, line := range lines {
		// Check for File: header
		if matches := fileHeaderRegex.FindStringSubmatch(line); matches != nil {
			currentPath = normalizePath(strings.TrimSpace(matches[1]))
			continue
		}

		// Check for Lines: header (follows File: header)
		if currentPath != "" {
			if matches := lineRangeFromResultRegex.FindStringSubmatch(line); matches != nil {
				startLine := parseInt(matches[1])
				endLine := startLine
				if matches[2] != "" {
					endLine = parseInt(matches[2])
				}
				if startLine > 0 {
					ranges = append(ranges, FileLineRange{
						Path:      currentPath,
						StartLine: startLine,
						EndLine:   endLine,
						Sources:   []string{SourceToolCallResult},
					})
				}
				currentPath = "" // Reset after capturing
				continue
			}
		}

		// Check for ripgrep format (path:line:)
		if matches := fileLineRegex.FindStringSubmatch(line); matches != nil {
			path := normalizePath(matches[1])
			lineNum := parseInt(matches[2])
			if path != "" && lineNum > 0 {
				ranges = append(ranges, FileLineRange{
					Path:      path,
					StartLine: lineNum,
					EndLine:   lineNum,
					Sources:   []string{SourceToolCallResult},
				})
			}
		}
	}

	return ranges
}

// GetGoldenLineRanges returns just the golden (primary) line ranges from a case.
func GetGoldenLineRanges(c Case) []FileLineRange {
	return extractMergeApprovalLineRanges(c)
}

// ExtractLineRangesFromActions extracts line ranges from a raw slice of actions.
func ExtractLineRangesFromActions(actions []domain.FlowAction) []FileLineRange {
	tempCase := Case{Actions: actions}
	return ExtractLineRanges(tempCase)
}

// MergeOverlappingRanges merges overlapping or adjacent line ranges for the same file.
// This produces a cleaner output for evaluation.
func MergeOverlappingRanges(ranges []FileLineRange) []FileLineRange {
	if len(ranges) == 0 {
		return nil
	}

	// Group by path
	byPath := make(map[string][]FileLineRange)
	var pathOrder []string
	for _, r := range ranges {
		if _, exists := byPath[r.Path]; !exists {
			pathOrder = append(pathOrder, r.Path)
		}
		byPath[r.Path] = append(byPath[r.Path], r)
	}

	var result []FileLineRange
	for _, path := range pathOrder {
		pathRanges := byPath[path]
		// Sort by start line
		for i := 1; i < len(pathRanges); i++ {
			for j := i; j > 0 && pathRanges[j].StartLine < pathRanges[j-1].StartLine; j-- {
				pathRanges[j], pathRanges[j-1] = pathRanges[j-1], pathRanges[j]
			}
		}

		// Merge overlapping/adjacent ranges
		merged := []FileLineRange{pathRanges[0]}
		for i := 1; i < len(pathRanges); i++ {
			last := &merged[len(merged)-1]
			curr := pathRanges[i]
			// Merge if overlapping or adjacent (within 1 line)
			if curr.StartLine <= last.EndLine+1 {
				if curr.EndLine > last.EndLine {
					last.EndLine = curr.EndLine
				}
				// Merge sources
				for _, s := range curr.Sources {
					found := false
					for _, ls := range last.Sources {
						if ls == s {
							found = true
							break
						}
					}
					if !found {
						last.Sources = append(last.Sources, s)
					}
				}
			} else {
				merged = append(merged, curr)
			}
		}
		result = append(result, merged...)
	}

	return result
}
