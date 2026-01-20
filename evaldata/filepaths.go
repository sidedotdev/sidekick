package evaldata

import (
	"encoding/json"
	"strings"

	"sidekick/domain"
)

// ExtractFilePaths extracts ranked file paths from a case.
// Primary (golden) paths come from the merge approval diff.
// Secondary paths come from tool call args, tool call results, and other diffs.
func ExtractFilePaths(c Case) []FilePath {
	pathMap := make(map[string]*FilePath)
	var orderedPaths []string

	// Helper to add a path with a source (deduplicates sources)
	addPath := func(path, source string) {
		path = normalizePath(path)
		if path == "" {
			return
		}
		if existing, ok := pathMap[path]; ok {
			// Only add source if not already present
			for _, s := range existing.Sources {
				if s == source {
					return
				}
			}
			existing.Sources = append(existing.Sources, source)
		} else {
			pathMap[path] = &FilePath{
				Path:    path,
				Sources: []string{source},
			}
			orderedPaths = append(orderedPaths, path)
		}
	}

	// 1. Primary (golden): extract from merge approval diff
	goldenPaths := extractMergeApprovalDiffPaths(c)
	for _, p := range goldenPaths {
		addPath(p, SourceReviewMergeDiff)
	}

	// 2. Secondary: tool call args and results, other diffs (in action order)
	for _, action := range c.Actions {
		// Skip the merge approval action for secondary extraction
		if action.ActionType == ActionTypeMergeApproval {
			continue
		}

		// Extract from tool call arguments
		if strings.HasPrefix(action.ActionType, ToolCallActionPrefix) {
			toolName := strings.TrimPrefix(action.ActionType, ToolCallActionPrefix)
			argPaths := extractPathsFromToolCallArgs(toolName, action.ActionParams)
			for _, p := range argPaths {
				addPath(p, SourceToolCallArgs)
			}

			// Extract from tool call results (bulk_search_repository)
			if toolName == ToolNameBulkSearchRepository && action.ActionResult != "" {
				resultPaths := ParseBulkSearchResultPaths(action.ActionResult)
				for _, p := range resultPaths {
					addPath(p, SourceToolCallResult)
				}
			}
		}

		// Extract from any diffs in ActionResult
		if action.ActionResult != "" && ContainsDiff(action.ActionResult) {
			diffPaths := ParseDiffPaths(action.ActionResult)
			for _, p := range diffPaths {
				addPath(p, SourceDiff)
			}
		}
	}

	// Build result in order
	if len(orderedPaths) == 0 {
		return nil
	}
	result := make([]FilePath, 0, len(orderedPaths))
	for _, path := range orderedPaths {
		result = append(result, *pathMap[path])
	}

	return result
}

// extractMergeApprovalDiffPaths extracts file paths from the merge approval action's diff.
func extractMergeApprovalDiffPaths(c Case) []string {
	mergeAction := GetMergeApprovalAction(c)
	if mergeAction == nil {
		return nil
	}

	// The diff is nested under mergeApprovalInfo.diff in ActionParams
	mergeApprovalInfo, ok := mergeAction.ActionParams["mergeApprovalInfo"].(map[string]interface{})
	if !ok {
		return nil
	}

	diff, ok := mergeApprovalInfo["diff"].(string)
	if !ok {
		return nil
	}

	return ParseDiffPaths(diff)
}

// extractPathsFromToolCallArgs extracts file paths from tool call arguments.
func extractPathsFromToolCallArgs(toolName string, params map[string]interface{}) []string {
	if params == nil {
		return nil
	}

	// Marshal and unmarshal to get typed access
	paramsJson, err := json.Marshal(params)
	if err != nil {
		return nil
	}

	var paths []string

	switch toolName {
	case ToolNameGetSymbolDefinitions:
		var args GetSymbolDefinitionsArgs
		if err := json.Unmarshal(paramsJson, &args); err != nil {
			return nil
		}
		for _, req := range args.Requests {
			if req.FilePath != "" {
				paths = append(paths, req.FilePath)
			}
		}

	case ToolNameReadFileLines:
		var args ReadFileLinesArgs
		if err := json.Unmarshal(paramsJson, &args); err != nil {
			return nil
		}
		for _, fl := range args.FileLines {
			if fl.FilePath != "" {
				paths = append(paths, fl.FilePath)
			}
		}

	case ToolNameBulkSearchRepository:
		// bulk_search_repository args contain path globs, not file paths
		// File paths are extracted from results, not args
		return nil
	}

	return paths
}

// GetGoldenFilePaths returns just the golden (primary) file paths from a case.
func GetGoldenFilePaths(c Case) []string {
	return extractMergeApprovalDiffPaths(c)
}

// IsGoldenPath checks if a path is in the golden set.
func IsGoldenPath(path string, goldenPaths []string) bool {
	normalizedPath := normalizePath(path)
	for _, gp := range goldenPaths {
		if normalizePath(gp) == normalizedPath {
			return true
		}
	}
	return false
}

// ExtractFilePathsFromActions extracts file paths from a raw slice of actions.
func ExtractFilePathsFromActions(actions []domain.FlowAction) []FilePath {
	tempCase := Case{Actions: actions}
	return ExtractFilePaths(tempCase)
}
