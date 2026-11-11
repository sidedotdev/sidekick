package main

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPrepareStreamProbeHistoryEdits(t *testing.T) {
	t.Parallel()

	const original = `{
		"Options": {"provider":"anthropic","model":"test-model"},
		"probeMessages": [
			{"role":"user","content":[{"type":"text","text":"first prompt"}]},
			{"role":"assistant","content":[{"type":"tool_use","toolUse":{
				"id":"call-1","name":"get_symbol_definitions",
				"arguments":"{\"analysis\":\"inspect the function\",\"requests\":[]}"
			}}]},
			{"role":"user","content":[{"type":"tool_result","toolResult":{
				"toolCallId":"call-1","name":"get_symbol_definitions",
				"content":[{"type":"text","text":"function definition"}]
			}}]},
			{"role":"user","content":[{"type":"text","text":"last prompt"}]}
		],
		"probeEdits": %s,
		"FlowActionId":"original-action",
		"FlowId":"original-flow",
		"WorkspaceId":"original-workspace"
	}`
	tests := []struct {
		name  string
		edits string
		check func(*testing.T, json.RawMessage)
	}{
		{
			name:  "select messages",
			edits: `{"keep":[0,3]}`,
			check: func(t *testing.T, raw json.RawMessage) {
				input, err := prepareStreamProbe(activityInvocation{Name: "Stream", Args: []json.RawMessage{raw}}, streamProbeOptions{})
				require.NoError(t, err)
				messages := input.ChatHistory.Llm2Messages()
				require.Len(t, messages, 2)
				assert.Equal(t, "first prompt", messages[0].Content[0].Text)
				assert.Equal(t, "last prompt", messages[1].Content[0].Text)
				assert.Equal(t, "test-model", input.Options.Model)
				assert.Empty(t, input.FlowActionId)
			},
		},
		{
			name:  "rename only selected tool argument",
			edits: `{"rename":[{"tool":"get_symbol_definitions","from":"analysis","to":"description"}]}`,
			check: func(t *testing.T, raw json.RawMessage) {
				input, err := prepareStreamProbe(activityInvocation{Name: "Stream", Args: []json.RawMessage{raw}}, streamProbeOptions{})
				require.NoError(t, err)
				messages := input.ChatHistory.Llm2Messages()
				require.Len(t, messages, 4)
				var arguments map[string]json.RawMessage
				require.NoError(t, json.Unmarshal([]byte(messages[1].Content[0].ToolUse.Arguments), &arguments))
				assert.NotContains(t, arguments, "analysis")
				assert.JSONEq(t, `"inspect the function"`, string(arguments["description"]))
				assert.JSONEq(t, `[]`, string(arguments["requests"]))
				assert.Equal(t, "function definition", messages[2].Content[0].ToolResult.Content[0].Text)
				assert.Contains(t, string(raw), `\"analysis\"`)
			},
		},
		{
			name:  "scoped text replacement",
			edits: `{"replace":[{"messages":[3],"old":"prompt","new":"request"}]}`,
			check: func(t *testing.T, raw json.RawMessage) {
				input, err := prepareStreamProbe(activityInvocation{Name: "Stream", Args: []json.RawMessage{raw}}, streamProbeOptions{})
				require.NoError(t, err)
				messages := input.ChatHistory.Llm2Messages()
				assert.Equal(t, "first prompt", messages[0].Content[0].Text)
				assert.Equal(t, "last request", messages[3].Content[0].Text)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			raw := json.RawMessage(fmtProbeInput(original, test.edits))
			test.check(t, raw)
		})
	}
}

func fmtProbeInput(template, edits string) string {
	for i := 0; i+1 < len(template); i++ {
		if template[i:i+2] == "%s" {
			return template[:i] + edits + template[i+2:]
		}
	}
	panic("missing edits placeholder")
}

func TestPrepareStreamProbeRejectsInvalidHistoryEdits(t *testing.T) {
	t.Parallel()

	for _, edits := range []string{
		`{"keep":[]}`,
		`{"keep":[9]}`,
		`{"keep":[0,0]}`,
		`{"unknown":true}`,
		`{"rename":[{"tool":"missing","from":"analysis","to":"description"}]}`,
		`{"replace":[{"old":"not present","new":"replacement"}]}`,
	} {
		t.Run(edits, func(t *testing.T) {
			t.Parallel()
			raw := json.RawMessage(`{
				"probeMessages":[{"role":"user","content":[{"type":"text","text":"prompt"}]}],
				"probeEdits":` + edits + `}`)
			_, err := prepareStreamProbe(activityInvocation{Name: "Stream", Args: []json.RawMessage{raw}}, streamProbeOptions{})
			require.Error(t, err)
		})
	}
}
