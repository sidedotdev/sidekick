package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func decodeProbeLiteBody(t *testing.T, body []byte) map[string]json.RawMessage {
	t.Helper()
	var fields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(body, &fields))
	return fields
}

func TestBuildProbeLiteBody(t *testing.T) {
	t.Parallel()

	t.Run("full dialect declares tools at top level", func(t *testing.T) {
		t.Parallel()
		for _, control := range []probeLiteControl{{Name: "hosted"}, {Name: "client", ClientSearch: true}} {
			body, err := buildProbeLiteBody("gpt-6-astra", control, nil)
			require.NoError(t, err)
			fields := decodeProbeLiteBody(t, body)
			assert.NotContains(t, fields, "parallel_tool_calls")
			assert.NotContains(t, fields, "reasoning")
			var tools []map[string]any
			require.NoError(t, json.Unmarshal(fields["tools"], &tools))
			require.Len(t, tools, 1)
			var input []map[string]any
			require.NoError(t, json.Unmarshal(fields["input"], &input))
			require.Len(t, input, 1)
			assert.Equal(t, "user", input[0]["role"])
			if control.ClientSearch {
				assert.Equal(t, "function", tools[0]["type"])
				assert.Equal(t, probeLiteSearchFunction, tools[0]["name"])
			} else {
				assert.Equal(t, map[string]any{"type": "web_search"}, tools[0])
			}
		}
	})

	t.Run("lite dialect moves tools into a leading additional_tools item", func(t *testing.T) {
		t.Parallel()
		body, err := buildProbeLiteBody("gpt-6-astra", probeLiteControl{Lite: true, ClientSearch: true}, nil)
		require.NoError(t, err)
		fields := decodeProbeLiteBody(t, body)
		assert.NotContains(t, fields, "tools")
		assert.JSONEq(t, `false`, string(fields["parallel_tool_calls"]))
		assert.JSONEq(t, `{"context":"all_turns"}`, string(fields["reasoning"]))
		assert.JSONEq(t, `"gpt-6-astra"`, string(fields["model"]))

		var input []struct {
			Type  string `json:"type"`
			Role  string `json:"role"`
			Tools []struct {
				Type  string            `json:"type"`
				Name  string            `json:"name"`
				Tools []json.RawMessage `json:"tools"`
			} `json:"tools"`
		}
		require.NoError(t, json.Unmarshal(fields["input"], &input))
		require.Len(t, input, 2)
		assert.Equal(t, "additional_tools", input[0].Type)
		assert.Equal(t, "developer", input[0].Role)
		require.Len(t, input[0].Tools, 1)
		assert.Equal(t, "namespace", input[0].Tools[0].Type)
		assert.Equal(t, "functions", input[0].Tools[0].Name)
		require.Len(t, input[0].Tools[0].Tools, 1)
		assert.JSONEq(t, string(probeLiteFunctionTool), string(input[0].Tools[0].Tools[0]))
		assert.Equal(t, "user", input[1].Role)
	})

	t.Run("lite hosted search is declared without a namespace", func(t *testing.T) {
		t.Parallel()
		body, err := buildProbeLiteBody("gpt-6-astra", probeLiteControl{Lite: true}, nil)
		require.NoError(t, err)
		var input []map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(decodeProbeLiteBody(t, body)["input"], &input))
		assert.JSONEq(t, `[{"type":"web_search"}]`, string(input[0]["tools"]))
	})

	t.Run("follow-up items are appended after the prompt", func(t *testing.T) {
		t.Parallel()
		call := probeTraceItem{CallID: "call_1", Name: probeLiteSearchFunction, Arguments: `{"query":"go"}`}
		followUp, err := probeFunctionCallFollowUp(call, "results")
		require.NoError(t, err)
		body, err := buildProbeLiteBody("gpt-6-astra", probeLiteControl{Lite: true, ClientSearch: true}, followUp)
		require.NoError(t, err)
		var input []map[string]any
		require.NoError(t, json.Unmarshal(decodeProbeLiteBody(t, body)["input"], &input))
		require.Len(t, input, 4)
		assert.Equal(t, map[string]any{"type": "function_call", "call_id": "call_1", "name": probeLiteSearchFunction, "arguments": `{"query":"go"}`}, input[2])
		assert.Equal(t, map[string]any{"type": "function_call_output", "call_id": "call_1", "output": "results"}, input[3])
	})

	t.Run("requires a model", func(t *testing.T) {
		t.Parallel()
		_, err := buildProbeLiteBody("", probeLiteControl{}, nil)
		require.ErrorContains(t, err, "requires a model")
	})
}

func TestAnalyzeProbeLiteSSE(t *testing.T) {
	t.Parallel()

	created := `{"type":"response.created","response":{"id":"resp_1","model":"gpt-6-astra"}}`
	searchAdded := `{"type":"response.output_item.added","item":{"id":"ws_1","type":"web_search_call","status":"in_progress"}}`
	searchDone := `{"type":"response.output_item.done","item":{"id":"ws_1","type":"web_search_call","status":"completed"}}`
	messageAdded := `{"type":"response.output_item.added","item":{"id":"msg_1","type":"message"}}`
	messageDone := `{"type":"response.output_item.done","item":{"id":"msg_1","type":"message","status":"completed"}}`
	functionAdded := `{"type":"response.output_item.added","item":{"id":"fc_1","type":"function_call","name":"search_web","call_id":"call_1","arguments":""}}`
	functionDone := `{"type":"response.output_item.done","item":{"id":"fc_1","type":"function_call","name":"search_web","call_id":"call_1","arguments":"{\"query\":\"go\"}","status":"completed"}}`
	functionNoCallID := `{"type":"response.output_item.done","item":{"id":"fc_1","type":"function_call","name":"search_web","arguments":"{\"query\":\"go\"}","status":"completed"}}`
	completed := `{"type":"response.completed","response":{"id":"resp_1","status":"completed"}}`
	serverError := `{"type":"error","code":"server_error","message":"request failed"}`
	failed := `{"type":"response.failed","response":{"id":"resp_1","status":"failed","error":{"code":"server_error","message":"request failed"}}}`

	for _, tc := range []struct {
		name      string
		events    []string
		outcome   string
		searches  int
		messages  int
		functions int
		errorText string
	}{
		{
			name:     "hosted search followed by an answer",
			events:   []string{created, searchAdded, searchDone, messageAdded, messageDone, completed},
			outcome:  "completed",
			searches: 1, messages: 1,
		},
		{
			name:      "client search function call",
			events:    []string{created, functionAdded, functionDone, completed},
			outcome:   "completed",
			functions: 1,
		},
		{
			name:      "provider error after search, followed by response.failed",
			events:    []string{created, searchAdded, searchDone, serverError, failed},
			outcome:   "providerError",
			searches:  1,
			errorText: "server_error: request failed",
		},
		{
			name:      "function call without call id is rejected",
			events:    []string{created, functionAdded, functionNoCallID, completed},
			outcome:   "invalid",
			errorText: "missing call id",
		},
		{
			name:      "missing terminal event",
			events:    []string{created, searchAdded, searchDone},
			outcome:   "invalid",
			searches:  1,
			errorText: "missing terminal response event",
		},
		{
			name:      "events before response.created",
			events:    []string{searchAdded, created, completed},
			outcome:   "invalid",
			errorText: "missing response.created",
		},
		{
			name:      "completion for an unknown item",
			events:    []string{created, searchDone, completed},
			outcome:   "invalid",
			errorText: "inconsistent completion",
		},
		{
			name:      "events after completion",
			events:    []string{created, completed, messageAdded},
			outcome:   "invalid",
			errorText: "after terminal response event",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			trace, err := analyzeProbeLiteSSE(strings.NewReader(probeTestSSE(tc.events...)))
			if tc.errorText == "" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, tc.errorText)
			}
			assert.Equal(t, tc.outcome, trace.Outcome)
			assert.Equal(t, tc.searches, trace.SearchesCompleted)
			assert.Equal(t, tc.messages, trace.MessagesCompleted)
			assert.Len(t, trace.FunctionCalls, tc.functions)
		})
	}
}

func TestProbeAlphaSearchToolOutput(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name      string
		body      string
		output    string
		errorText string
	}{
		{
			name:   "results are rendered as JSON",
			body:   `{"results":[{"title":"Go","url":"https://go.dev","snippet":"Go 1.27"}]}`,
			output: `[{"title":"Go","url":"https://go.dev","snippet":"Go 1.27"}]`,
		},
		{
			name:   "plain output is used when there are no results",
			body:   `{"output":"Go 1.27 is the latest release."}`,
			output: "Go 1.27 is the latest release.",
		},
		{
			name:      "empty response is rejected",
			body:      `{}`,
			errorText: "neither output nor results",
		},
		{
			name:      "malformed response is rejected",
			body:      `not json`,
			errorText: "decode alpha search response",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			output, err := probeAlphaSearchToolOutput([]byte(tc.body))
			if tc.errorText != "" {
				require.ErrorContains(t, err, tc.errorText)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.output, output)
		})
	}
}
