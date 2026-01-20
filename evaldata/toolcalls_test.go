package evaldata

import (
	"encoding/json"
	"sidekick/domain"
	"testing"
	"time"
)

func TestExtractToolCalls(t *testing.T) {
	t.Parallel()

	baseTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name              string
		actions           []domain.FlowAction
		expectedCount     int
		expectedToolNames []string
		expectedIds       []string
	}{
		{
			name:          "empty case",
			actions:       nil,
			expectedCount: 0,
		},
		{
			name: "no tool call actions",
			actions: []domain.FlowAction{
				{Id: "a1", ActionType: "ranked_repo_summary", Created: baseTime},
				{Id: "a2", ActionType: ActionTypeMergeApproval, Created: baseTime.Add(time.Minute)},
			},
			expectedCount: 0,
		},
		{
			name: "only allowed tool calls are extracted",
			actions: []domain.FlowAction{
				{Id: "a1", ActionType: "tool_call.get_symbol_definitions", Created: baseTime, ActionParams: map[string]interface{}{}},
				{Id: "a2", ActionType: "tool_call.bulk_search_repository", Created: baseTime.Add(time.Minute), ActionParams: map[string]interface{}{}},
				{Id: "a3", ActionType: "tool_call.read_file_lines", Created: baseTime.Add(2 * time.Minute), ActionParams: map[string]interface{}{}},
				{Id: "a4", ActionType: "tool_call.some_other_tool", Created: baseTime.Add(3 * time.Minute), ActionParams: map[string]interface{}{}},
				{Id: "a5", ActionType: "tool_call.apply_edit_blocks", Created: baseTime.Add(4 * time.Minute), ActionParams: map[string]interface{}{}},
			},
			expectedCount:     3,
			expectedToolNames: []string{"get_symbol_definitions", "bulk_search_repository", "read_file_lines"},
			expectedIds:       []string{"a1", "a2", "a3"},
		},
		{
			name: "preserves order from case actions",
			actions: []domain.FlowAction{
				{Id: "a1", ActionType: "tool_call.read_file_lines", Created: baseTime, ActionParams: map[string]interface{}{}},
				{Id: "a2", ActionType: "tool_call.get_symbol_definitions", Created: baseTime.Add(time.Minute), ActionParams: map[string]interface{}{}},
				{Id: "a3", ActionType: "tool_call.bulk_search_repository", Created: baseTime.Add(2 * time.Minute), ActionParams: map[string]interface{}{}},
			},
			expectedCount:     3,
			expectedToolNames: []string{"read_file_lines", "get_symbol_definitions", "bulk_search_repository"},
			expectedIds:       []string{"a1", "a2", "a3"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := Case{Actions: tt.actions}
			specs := ExtractToolCalls(c)

			if len(specs) != tt.expectedCount {
				t.Errorf("expected %d tool calls, got %d", tt.expectedCount, len(specs))
				return
			}

			for i, spec := range specs {
				if spec.ToolName != tt.expectedToolNames[i] {
					t.Errorf("spec %d: expected tool name %q, got %q", i, tt.expectedToolNames[i], spec.ToolName)
				}
				if spec.ToolCallId != tt.expectedIds[i] {
					t.Errorf("spec %d: expected id %q, got %q", i, tt.expectedIds[i], spec.ToolCallId)
				}
			}
		})
	}
}

func TestExtractToolCalls_GetSymbolDefinitions(t *testing.T) {
	t.Parallel()

	actionParams := map[string]interface{}{
		"analysis": "Looking for function definitions",
		"requests": []interface{}{
			map[string]interface{}{
				"file_path":    "foo/bar.go",
				"symbol_names": []interface{}{"FuncA", "TypeB"},
			},
		},
	}

	c := Case{
		Actions: []domain.FlowAction{
			{Id: "a1", ActionType: "tool_call.get_symbol_definitions", ActionParams: actionParams},
		},
	}

	specs := ExtractToolCalls(c)
	if len(specs) != 1 {
		t.Fatalf("expected 1 spec, got %d", len(specs))
	}

	spec := specs[0]
	if spec.ParseError != "" {
		t.Errorf("unexpected parse error: %s", spec.ParseError)
	}

	typed, ok := spec.Arguments.(GetSymbolDefinitionsArgs)
	if !ok {
		t.Fatalf("expected GetSymbolDefinitionsArgs, got %T", spec.Arguments)
	}

	if typed.Analysis != "Looking for function definitions" {
		t.Errorf("unexpected analysis: %q", typed.Analysis)
	}
	if len(typed.Requests) != 1 {
		t.Fatalf("expected 1 request, got %d", len(typed.Requests))
	}
	if typed.Requests[0].FilePath != "foo/bar.go" {
		t.Errorf("unexpected file path: %q", typed.Requests[0].FilePath)
	}
	if len(typed.Requests[0].SymbolNames) != 2 {
		t.Errorf("expected 2 symbol names, got %d", len(typed.Requests[0].SymbolNames))
	}
}

func TestExtractToolCalls_BulkSearchRepository(t *testing.T) {
	t.Parallel()

	actionParams := map[string]interface{}{
		"context_lines": float64(5),
		"searches": []interface{}{
			map[string]interface{}{
				"path_glob":   "**/*.go",
				"search_term": "func main",
			},
		},
	}

	c := Case{
		Actions: []domain.FlowAction{
			{Id: "a1", ActionType: "tool_call.bulk_search_repository", ActionParams: actionParams},
		},
	}

	specs := ExtractToolCalls(c)
	if len(specs) != 1 {
		t.Fatalf("expected 1 spec, got %d", len(specs))
	}

	spec := specs[0]
	if spec.ParseError != "" {
		t.Errorf("unexpected parse error: %s", spec.ParseError)
	}

	typed, ok := spec.Arguments.(BulkSearchRepositoryArgs)
	if !ok {
		t.Fatalf("expected BulkSearchRepositoryArgs, got %T", spec.Arguments)
	}

	if typed.ContextLines != 5 {
		t.Errorf("unexpected context lines: %d", typed.ContextLines)
	}
	if len(typed.Searches) != 1 {
		t.Fatalf("expected 1 search, got %d", len(typed.Searches))
	}
	if typed.Searches[0].PathGlob != "**/*.go" {
		t.Errorf("unexpected path glob: %q", typed.Searches[0].PathGlob)
	}
	if typed.Searches[0].SearchTerm != "func main" {
		t.Errorf("unexpected search term: %q", typed.Searches[0].SearchTerm)
	}
}

func TestExtractToolCalls_ReadFileLines(t *testing.T) {
	t.Parallel()

	actionParams := map[string]interface{}{
		"window_size": float64(10),
		"file_lines": []interface{}{
			map[string]interface{}{
				"file_path":   "main.go",
				"line_number": float64(42),
			},
		},
	}

	c := Case{
		Actions: []domain.FlowAction{
			{Id: "a1", ActionType: "tool_call.read_file_lines", ActionParams: actionParams},
		},
	}

	specs := ExtractToolCalls(c)
	if len(specs) != 1 {
		t.Fatalf("expected 1 spec, got %d", len(specs))
	}

	spec := specs[0]
	if spec.ParseError != "" {
		t.Errorf("unexpected parse error: %s", spec.ParseError)
	}

	typed, ok := spec.Arguments.(ReadFileLinesArgs)
	if !ok {
		t.Fatalf("expected ReadFileLinesArgs, got %T", spec.Arguments)
	}

	if typed.WindowSize != 10 {
		t.Errorf("unexpected window size: %d", typed.WindowSize)
	}
	if len(typed.FileLines) != 1 {
		t.Fatalf("expected 1 file line, got %d", len(typed.FileLines))
	}
	if typed.FileLines[0].FilePath != "main.go" {
		t.Errorf("unexpected file path: %q", typed.FileLines[0].FilePath)
	}
	if typed.FileLines[0].LineNumber != 42 {
		t.Errorf("unexpected line number: %d", typed.FileLines[0].LineNumber)
	}
}

func TestExtractToolCalls_ArgumentsJson(t *testing.T) {
	t.Parallel()

	actionParams := map[string]interface{}{
		"analysis": "test",
		"requests": []interface{}{},
	}

	c := Case{
		Actions: []domain.FlowAction{
			{Id: "a1", ActionType: "tool_call.get_symbol_definitions", ActionParams: actionParams},
		},
	}

	specs := ExtractToolCalls(c)
	if len(specs) != 1 {
		t.Fatalf("expected 1 spec, got %d", len(specs))
	}

	if specs[0].ArgumentsJson == "" {
		t.Error("expected non-empty ArgumentsJson")
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(specs[0].ArgumentsJson), &parsed); err != nil {
		t.Errorf("ArgumentsJson is not valid JSON: %v", err)
	}
}
