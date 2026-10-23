package main

import (
	"encoding/json"
	"testing"

	"sidekick/common"
	"sidekick/persisted_ai"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPrepareStreamProbe(t *testing.T) {
	t.Parallel()

	original := json.RawMessage(`{
		"Options": {
			"provider": "openai",
			"model": "test-model",
			"reasoningEffort": "highest",
			"toolChoice": {"type": "required"},
			"parallelToolCalls": true,
			"tools": [
				{"name": "advisor_proceed", "parameters": {"type": "object", "properties": {}}},
				{"type": "web_search"}
			]
		},
		"ChatHistory": {
			"type": "llm2",
			"refs": [{"role": "user", "blockKeys": ["original-block"]}],
			"flowId": "original-flow",
			"workspaceId": "original-workspace"
		},
		"FlowActionId": "original-action",
		"FlowId": "original-flow",
		"WorkspaceId": "original-workspace"
	}`)
	var expected persisted_ai.StreamInput
	require.NoError(t, json.Unmarshal(original, &expected))

	tests := []struct {
		name    string
		options streamProbeOptions
		wantErr string
		check   func(*testing.T, persisted_ai.StreamInput)
	}{
		{
			name: "unchanged request without live publication",
			check: func(t *testing.T, input persisted_ai.StreamInput) {
				assert.Equal(t, expected.Options, input.Options)
			},
		},
		{
			name:    "automatic tool choice",
			options: streamProbeOptions{ToolChoice: "auto"},
			check: func(t *testing.T, input persisted_ai.StreamInput) {
				assert.Equal(t, common.ToolChoice{Type: common.ToolChoiceTypeAuto}, input.Options.ToolChoice)
				assert.Equal(t, expected.Options.Tools, input.Options.Tools)
			},
		},
		{
			name:    "omit native tool",
			options: streamProbeOptions{OmitTools: "web_search"},
			check: func(t *testing.T, input persisted_ai.StreamInput) {
				require.Len(t, input.Options.Tools, 1)
				assert.Equal(t, "advisor_proceed", input.Options.Tools[0].Name)
				assert.Equal(t, expected.Options.ToolChoice, input.Options.ToolChoice)
			},
		},
		{
			name:    "model control",
			options: streamProbeOptions{Model: "control-model"},
			check: func(t *testing.T, input persisted_ai.StreamInput) {
				assert.Equal(t, "control-model", input.Options.Model)
				assert.Equal(t, expected.Options.ReasoningEffort, input.Options.ReasoningEffort)
			},
		},
		{
			name:    "effort control",
			options: streamProbeOptions{ReasoningEffort: "low"},
			check: func(t *testing.T, input persisted_ai.StreamInput) {
				assert.Equal(t, "low", input.Options.ReasoningEffort)
				assert.Equal(t, expected.Options.Model, input.Options.Model)
			},
		},
		{
			name:    "extra body merges over existing keys",
			options: streamProbeOptions{ExtraBody: `{"parallel_tool_calls": true, "store": false}`},
			check: func(t *testing.T, input persisted_ai.StreamInput) {
				assert.Equal(t, map[string]any{"parallel_tool_calls": true, "store": false}, input.Options.ExtraBody)
			},
		},
		{
			name:    "invalid extra body",
			options: streamProbeOptions{ExtraBody: `[1]`},
			wantErr: "extra body",
		},
		{
			name:    "invalid choice",
			options: streamProbeOptions{ToolChoice: "typo"},
			wantErr: "tool choice",
		},
		{
			name:    "unknown omitted tool",
			options: streamProbeOptions{OmitTools: "missing"},
			wantErr: "missing",
		},
		{
			name:    "required choice without tools",
			options: streamProbeOptions{OmitTools: "advisor_proceed,web_search"},
			wantErr: "required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			input, err := prepareStreamProbe(activityInvocation{
				Name: "Stream",
				Args: []json.RawMessage{original},
			}, tt.options)
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Empty(t, input.FlowActionId)
			assert.Equal(t, expected.FlowId, input.FlowId)
			assert.Equal(t, expected.WorkspaceId, input.WorkspaceId)
			assert.Equal(t, expected.ChatHistory, input.ChatHistory)
			tt.check(t, input)
			assert.Contains(t, string(original), `"FlowActionId": "original-action"`)
		})
	}
}

func TestPrepareStreamProbeRejectsInvalidInvocation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		invocation activityInvocation
	}{
		{name: "other activity", invocation: activityInvocation{Name: "DeleteSandbox"}},
		{name: "missing input", invocation: activityInvocation{Name: "Stream"}},
		{name: "malformed input", invocation: activityInvocation{Name: "Stream", Args: []json.RawMessage{json.RawMessage(`{`)}}},
		{name: "missing history", invocation: activityInvocation{Name: "Stream", Args: []json.RawMessage{json.RawMessage(`{}`)}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := prepareStreamProbe(tt.invocation, streamProbeOptions{})
			require.Error(t, err)
		})
	}
}
