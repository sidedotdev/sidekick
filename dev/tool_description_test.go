package dev

import (
	"encoding/json"
	"sidekick/llm"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestToolDescriptions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		tool   *llm.Tool
		params func() any
		input  string
	}{
		{
			tool:   getSymbolDefinitionsTool,
			params: func() any { return &RequiredCodeContext{} },
			input:  `{"description":"Inspect command permission checks","requests":[{"file_path":"dev/run_command.go","symbols":[{"name":"RunCommand"}]}]}`,
		},
		{
			tool:   &runCommandTool,
			params: func() any { return &RunCommandParams{} },
			input:  `{"description":"Run command permission regression tests","command":"go test ./common","workingDir":""}`,
		},
		{
			tool:   &bulkSearchRepositoryTool,
			params: func() any { return &BulkSearchRepositoryParams{} },
			input:  `{"description":"Find command permission checks","context_lines":2,"searches":[{"path_glob":"**/*.go","search_term":"CheckCommandPermission"}]}`,
		},
		{
			tool:   &bulkReadFileTool,
			params: func() any { return &BulkReadFileParams{} },
			input:  `{"description":"Read the failing permission assertion","file_lines":[{"file_path":"common/command_permissions_test.go","line_number":10}],"window_size":5}`,
		},
		{
			tool:   &readImageTool,
			params: func() any { return &ReadImageParams{} },
			input:  `{"description":"Inspect the command approval screenshot","file_path":"docs/approval.png"}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.tool.Name, func(t *testing.T) {
			t.Parallel()

			schema, ok := tt.tool.Parameters.Properties.Get("description")
			if assert.True(t, ok, "the model must receive the description parameter") {
				assert.Equal(t, "string", schema.Type)
			}
			_, hasAnalysis := tt.tool.Parameters.Properties.Get("analysis")
			assert.False(t, hasAnalysis)

			var input map[string]any
			require.NoError(t, json.Unmarshal([]byte(tt.input), &input))
			params := tt.params()
			require.NoError(t, json.Unmarshal([]byte(tt.input), params))
			encoded, err := json.Marshal(params)
			require.NoError(t, err)
			var output map[string]any
			require.NoError(t, json.Unmarshal(encoded, &output))
			assert.Equal(t, input["description"], output["description"])
			assert.NotContains(t, output, "analysis")

			delete(input, "description")
			legacy, err := json.Marshal(input)
			require.NoError(t, err)
			require.NoError(t, json.Unmarshal(legacy, tt.params()))
		})
	}
}

func TestSymbolDescriptionLegacyInput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		input       string
		description string
	}{
		{
			name:        "legacy analysis",
			input:       `{"analysis":"Inspect permission checks","code_context_requests":[{"file_path":"dev/run_command.go"}]}`,
			description: "Inspect permission checks",
		},
		{
			name:        "description takes precedence",
			input:       `{"analysis":"Legacy explanation","description":"Inspect permission checks","requests":[{"file_path":"dev/run_command.go"}]}`,
			description: "Inspect permission checks",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var params RequiredCodeContext
			require.NoError(t, json.Unmarshal([]byte(tt.input), &params))
			require.Len(t, params.Requests, 1)
			assert.Equal(t, "dev/run_command.go", params.Requests[0].FilePath)
			encoded, err := json.Marshal(params)
			require.NoError(t, err)
			var output map[string]any
			require.NoError(t, json.Unmarshal(encoded, &output))
			assert.Equal(t, tt.description, output["description"])
			assert.NotContains(t, output, "analysis")
		})
	}
}

func TestReadFileDescriptionLenientInput(t *testing.T) {
	t.Parallel()

	params, err := parseBulkReadFileParamsLenient(`{"description":"Read the failing permission assertion","file_lines":[{"file_path":"test.go","line_number":10}],"window_size":5}`)
	require.NoError(t, err)
	encoded, err := json.Marshal(params)
	require.NoError(t, err)
	var output map[string]any
	require.NoError(t, json.Unmarshal(encoded, &output))
	assert.Equal(t, "Read the failing permission assertion", output["description"])
	require.Len(t, params.FileLines, 1)
}
