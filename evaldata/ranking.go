package evaldata

import (
	"encoding/json"
	"strings"
)

// RankToolCalls partitions and orders tool calls with golden-primary ranking.
// Primary: tool calls that reference any golden edited file path.
// Secondary: remaining tool calls in original execution order.
func RankToolCalls(toolCalls []ToolCallSpec, goldenPaths []string) []ToolCallSpec {
	if len(toolCalls) == 0 {
		return nil
	}

	goldenSet := make(map[string]bool)
	for _, p := range goldenPaths {
		goldenSet[normalizePath(p)] = true
	}

	var primary, secondary []ToolCallSpec

	for _, tc := range toolCalls {
		if toolCallReferencesGoldenPath(tc, goldenSet) {
			primary = append(primary, tc)
		} else {
			secondary = append(secondary, tc)
		}
	}

	result := make([]ToolCallSpec, 0, len(primary)+len(secondary))
	result = append(result, primary...)
	result = append(result, secondary...)
	return result
}

// toolCallReferencesGoldenPath checks if a tool call references any golden path.
// Checks both tool arguments and tool results.
func toolCallReferencesGoldenPath(tc ToolCallSpec, goldenSet map[string]bool) bool {
	// Check typed arguments if available
	if tc.Arguments != nil {
		paths := extractPathsFromTypedArgs(tc.ToolName, tc.Arguments)
		for _, p := range paths {
			if goldenSet[normalizePath(p)] {
				return true
			}
		}
	}

	// Fallback: parse from ArgumentsJson
	if tc.ArgumentsJson != "" {
		paths := extractPathsFromArgsJson(tc.ToolName, tc.ArgumentsJson)
		for _, p := range paths {
			if goldenSet[normalizePath(p)] {
				return true
			}
		}
	}

	// Check tool result for file paths
	if tc.ResultJson != "" {
		paths := ParseToolResultPaths(tc.ResultJson)
		for _, p := range paths {
			if goldenSet[normalizePath(p)] {
				return true
			}
		}
	}

	return false
}

// extractPathsFromTypedArgs extracts file paths from typed tool arguments.
func extractPathsFromTypedArgs(toolName string, args interface{}) []string {
	var paths []string

	switch toolName {
	case ToolNameGetSymbolDefinitions:
		if typedArgs, ok := args.(GetSymbolDefinitionsArgs); ok {
			for _, req := range typedArgs.Requests {
				if req.FilePath != "" {
					paths = append(paths, req.FilePath)
				}
			}
		}

	case ToolNameReadFileLines:
		if typedArgs, ok := args.(ReadFileLinesArgs); ok {
			for _, fl := range typedArgs.FileLines {
				if fl.FilePath != "" {
					paths = append(paths, fl.FilePath)
				}
			}
		}

	case ToolNameBulkSearchRepository:
		// bulk_search_repository uses path globs, not exact file paths
		// We could try to match globs against golden paths, but for now
		// we rely on result parsing which happens elsewhere
		if typedArgs, ok := args.(BulkSearchRepositoryArgs); ok {
			for _, search := range typedArgs.Searches {
				// If the path_glob looks like an exact file path (no wildcards),
				// treat it as a file path reference
				if search.PathGlob != "" && !containsGlobChars(search.PathGlob) {
					paths = append(paths, search.PathGlob)
				}
			}
		}
	}

	return paths
}

// extractPathsFromArgsJson extracts file paths by parsing the JSON arguments.
func extractPathsFromArgsJson(toolName string, argsJson string) []string {
	var paths []string

	switch toolName {
	case ToolNameGetSymbolDefinitions:
		var args GetSymbolDefinitionsArgs
		if err := json.Unmarshal([]byte(argsJson), &args); err == nil {
			for _, req := range args.Requests {
				if req.FilePath != "" {
					paths = append(paths, req.FilePath)
				}
			}
		}

	case ToolNameReadFileLines:
		var args ReadFileLinesArgs
		if err := json.Unmarshal([]byte(argsJson), &args); err == nil {
			for _, fl := range args.FileLines {
				if fl.FilePath != "" {
					paths = append(paths, fl.FilePath)
				}
			}
		}

	case ToolNameBulkSearchRepository:
		var args BulkSearchRepositoryArgs
		if err := json.Unmarshal([]byte(argsJson), &args); err == nil {
			for _, search := range args.Searches {
				if search.PathGlob != "" && !containsGlobChars(search.PathGlob) {
					paths = append(paths, search.PathGlob)
				}
			}
		}
	}

	return paths
}

// containsGlobChars checks if a string contains glob wildcard characters.
func containsGlobChars(s string) bool {
	return strings.ContainsAny(s, "*?[")
}

// ExtractRankedToolCalls extracts and ranks tool calls from a case.
// Returns tool calls ordered with golden-primary ranking.
func ExtractRankedToolCalls(c Case) []ToolCallSpec {
	toolCalls := ExtractToolCalls(c)
	goldenPaths := GetGoldenFilePaths(c)
	return RankToolCalls(toolCalls, goldenPaths)
}
