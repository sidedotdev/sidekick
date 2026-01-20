package evaldata

import (
	"sidekick/domain"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRankToolCalls(t *testing.T) {
	t.Parallel()

	t.Run("empty tool calls", func(t *testing.T) {
		t.Parallel()
		result := RankToolCalls(nil, []string{"golden.go"})
		assert.Nil(t, result)
	})

	t.Run("no golden paths", func(t *testing.T) {
		t.Parallel()
		toolCalls := []ToolCallSpec{
			{ToolName: ToolNameGetSymbolDefinitions, ToolCallId: "tc-1", ArgumentsJson: `{"requests":[{"file_path":"a.go"}]}`},
			{ToolName: ToolNameReadFileLines, ToolCallId: "tc-2", ArgumentsJson: `{"file_lines":[{"file_path":"b.go"}]}`},
		}
		result := RankToolCalls(toolCalls, nil)
		assert.Equal(t, toolCalls, result)
	})

	t.Run("golden calls first", func(t *testing.T) {
		t.Parallel()
		toolCalls := []ToolCallSpec{
			{ToolName: ToolNameGetSymbolDefinitions, ToolCallId: "tc-1", ArgumentsJson: `{"requests":[{"file_path":"secondary.go"}]}`},
			{ToolName: ToolNameReadFileLines, ToolCallId: "tc-2", ArgumentsJson: `{"file_lines":[{"file_path":"golden.go"}]}`},
			{ToolName: ToolNameGetSymbolDefinitions, ToolCallId: "tc-3", ArgumentsJson: `{"requests":[{"file_path":"other.go"}]}`},
		}
		goldenPaths := []string{"golden.go"}

		result := RankToolCalls(toolCalls, goldenPaths)

		assert.Len(t, result, 3)
		assert.Equal(t, "tc-2", result[0].ToolCallId)
		assert.Equal(t, "tc-1", result[1].ToolCallId)
		assert.Equal(t, "tc-3", result[2].ToolCallId)
	})

	t.Run("multiple golden calls preserve order", func(t *testing.T) {
		t.Parallel()
		toolCalls := []ToolCallSpec{
			{ToolName: ToolNameGetSymbolDefinitions, ToolCallId: "tc-1", ArgumentsJson: `{"requests":[{"file_path":"golden1.go"}]}`},
			{ToolName: ToolNameReadFileLines, ToolCallId: "tc-2", ArgumentsJson: `{"file_lines":[{"file_path":"secondary.go"}]}`},
			{ToolName: ToolNameGetSymbolDefinitions, ToolCallId: "tc-3", ArgumentsJson: `{"requests":[{"file_path":"golden2.go"}]}`},
		}
		goldenPaths := []string{"golden1.go", "golden2.go"}

		result := RankToolCalls(toolCalls, goldenPaths)

		assert.Len(t, result, 3)
		assert.Equal(t, "tc-1", result[0].ToolCallId)
		assert.Equal(t, "tc-3", result[1].ToolCallId)
		assert.Equal(t, "tc-2", result[2].ToolCallId)
	})

	t.Run("uses typed arguments when available", func(t *testing.T) {
		t.Parallel()
		toolCalls := []ToolCallSpec{
			{
				ToolName:      ToolNameGetSymbolDefinitions,
				ToolCallId:    "tc-1",
				ArgumentsJson: `{"requests":[{"file_path":"golden.go"}]}`,
				Arguments: GetSymbolDefinitionsArgs{
					Requests: []FileSymDefRequestArgs{{FilePath: "golden.go"}},
				},
			},
		}
		goldenPaths := []string{"golden.go"}

		result := RankToolCalls(toolCalls, goldenPaths)

		assert.Len(t, result, 1)
		assert.Equal(t, "tc-1", result[0].ToolCallId)
	})

	t.Run("bulk search with exact path glob", func(t *testing.T) {
		t.Parallel()
		toolCalls := []ToolCallSpec{
			{
				ToolName:      ToolNameBulkSearchRepository,
				ToolCallId:    "tc-1",
				ArgumentsJson: `{"searches":[{"path_glob":"golden.go","search_term":"foo"}]}`,
			},
			{
				ToolName:      ToolNameBulkSearchRepository,
				ToolCallId:    "tc-2",
				ArgumentsJson: `{"searches":[{"path_glob":"**/*.go","search_term":"bar"}]}`,
			},
		}
		goldenPaths := []string{"golden.go"}

		result := RankToolCalls(toolCalls, goldenPaths)

		assert.Len(t, result, 2)
		assert.Equal(t, "tc-1", result[0].ToolCallId)
		assert.Equal(t, "tc-2", result[1].ToolCallId)
	})
}

func TestExtractRankedToolCalls(t *testing.T) {
	t.Parallel()

	t.Run("extracts and ranks tool calls", func(t *testing.T) {
		t.Parallel()
		c := Case{
			Actions: []domain.FlowAction{
				{
					Id:         "tc-1",
					ActionType: "tool_call.get_symbol_definitions",
					ActionParams: map[string]interface{}{
						"requests": []interface{}{
							map[string]interface{}{"file_path": "secondary.go"},
						},
					},
				},
				{
					Id:         "tc-2",
					ActionType: "tool_call.read_file_lines",
					ActionParams: map[string]interface{}{
						"file_lines": []interface{}{
							map[string]interface{}{"file_path": "golden.go", "line_number": 10},
						},
					},
				},
				{
					Id:         "merge-1",
					ActionType: ActionTypeMergeApproval,
					ActionParams: map[string]interface{}{
						"mergeApprovalInfo": map[string]interface{}{
							"diff": "diff --git a/golden.go b/golden.go\nindex 123..abc 100644\n--- a/golden.go\n+++ b/golden.go\n@@ -1 +1 @@\n-old\n+new",
						},
					},
				},
			},
		}

		result := ExtractRankedToolCalls(c)

		assert.Len(t, result, 2)
		assert.Equal(t, "tc-2", result[0].ToolCallId)
		assert.Equal(t, "tc-1", result[1].ToolCallId)
	})
}

func TestContainsGlobChars(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input    string
		expected bool
	}{
		{"foo.go", false},
		{"pkg/bar.go", false},
		{"*.go", true},
		{"**/*.go", true},
		{"foo?.go", true},
		{"[abc].go", true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.expected, containsGlobChars(tt.input))
		})
	}
}

func TestRankToolCalls_WithToolResults(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		toolCalls   []ToolCallSpec
		goldenPaths []string
		wantOrder   []string // tool call IDs in expected order
	}{
		{
			name: "bulk_search result contains golden path",
			toolCalls: []ToolCallSpec{
				{
					ToolName:      ToolNameBulkSearchRepository,
					ToolCallId:    "tc1",
					ArgumentsJson: `{"searches":[{"path_glob":"*.go","search_term":"test"}]}`,
					ResultJson:    "Searched for \"test\" in \"*.go\"\nfoo/bar.go:10:func Test()",
				},
				{
					ToolName:      ToolNameGetSymbolDefinitions,
					ToolCallId:    "tc2",
					ArgumentsJson: `{"requests":[{"file_path":"other.go"}]}`,
				},
			},
			goldenPaths: []string{"foo/bar.go"},
			wantOrder:   []string{"tc1", "tc2"},
		},
		{
			name: "get_symbol_definitions result contains golden path via File header",
			toolCalls: []ToolCallSpec{
				{
					ToolName:      ToolNameBulkSearchRepository,
					ToolCallId:    "tc1",
					ArgumentsJson: `{"searches":[{"path_glob":"*.go","search_term":"test"}]}`,
				},
				{
					ToolName:      ToolNameGetSymbolDefinitions,
					ToolCallId:    "tc2",
					ArgumentsJson: `{"requests":[{"file_path":"other.go"}]}`,
					ResultJson:    "File: golden.go\nLines: 1-10\n```go\ncode\n```",
				},
			},
			goldenPaths: []string{"golden.go"},
			wantOrder:   []string{"tc2", "tc1"},
		},
		{
			name: "result path not in golden set",
			toolCalls: []ToolCallSpec{
				{
					ToolName:      ToolNameBulkSearchRepository,
					ToolCallId:    "tc1",
					ArgumentsJson: `{"searches":[{"path_glob":"*.go","search_term":"test"}]}`,
					ResultJson:    "Searched for \"test\" in \"*.go\"\nother.go:10:func Test()",
				},
			},
			goldenPaths: []string{"golden.go"},
			wantOrder:   []string{"tc1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := RankToolCalls(tt.toolCalls, tt.goldenPaths)

			if len(result) != len(tt.wantOrder) {
				t.Fatalf("RankToolCalls() returned %d items, want %d", len(result), len(tt.wantOrder))
			}

			for i, tc := range result {
				if tc.ToolCallId != tt.wantOrder[i] {
					t.Errorf("RankToolCalls()[%d].ToolCallId = %q, want %q", i, tc.ToolCallId, tt.wantOrder[i])
				}
			}
		})
	}
}
