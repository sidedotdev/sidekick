package evaldata

import (
	"encoding/json"
	"sidekick/domain"
	"strings"
)

// ExtractToolCalls extracts context retrieval tool call specs from a case.
// Only extracts calls for the allowed tool names: get_symbol_definitions,
// bulk_search_repository, and read_file_lines.
// Returns tool calls in the same order as they appear in the case actions.
func ExtractToolCalls(c Case) []ToolCallSpec {
	var specs []ToolCallSpec

	for _, action := range c.Actions {
		if !strings.HasPrefix(action.ActionType, ToolCallActionPrefix) {
			continue
		}

		toolName := strings.TrimPrefix(action.ActionType, ToolCallActionPrefix)
		if !ContextToolNames[toolName] {
			continue
		}

		spec := ToolCallSpec{
			ToolName:   toolName,
			ToolCallId: action.Id,
		}

		// Serialize ActionParams to canonical JSON
		argsJson, err := json.Marshal(action.ActionParams)
		if err != nil {
			spec.ParseError = "failed to marshal action params: " + err.Error()
		} else {
			spec.ArgumentsJson = string(argsJson)

			// Attempt typed parsing
			typedArgs, parseErr := parseTypedArguments(toolName, argsJson)
			if parseErr != nil {
				spec.ParseError = parseErr.Error()
			} else {
				spec.Arguments = typedArgs
			}
		}

		// Capture tool result if available
		if action.ActionResult != "" {
			spec.ResultJson = action.ActionResult
		}

		specs = append(specs, spec)
	}

	return specs
}

// parseTypedArguments attempts to unmarshal the JSON arguments into the
// appropriate typed struct based on the tool name.
func parseTypedArguments(toolName string, argsJson []byte) (interface{}, error) {
	switch toolName {
	case ToolNameGetSymbolDefinitions:
		var args GetSymbolDefinitionsArgs
		if err := json.Unmarshal(argsJson, &args); err != nil {
			return nil, err
		}
		return args, nil

	case ToolNameBulkSearchRepository:
		var args BulkSearchRepositoryArgs
		if err := json.Unmarshal(argsJson, &args); err != nil {
			return nil, err
		}
		return args, nil

	case ToolNameReadFileLines:
		var args ReadFileLinesArgs
		if err := json.Unmarshal(argsJson, &args); err != nil {
			return nil, err
		}
		return args, nil

	default:
		return nil, nil
	}
}

// ExtractToolCallsFromActions extracts tool calls from a raw slice of actions.
// This is a convenience function that creates a temporary case and extracts.
func ExtractToolCallsFromActions(actions []domain.FlowAction) []ToolCallSpec {
	tempCase := Case{Actions: actions}
	return ExtractToolCalls(tempCase)
}
