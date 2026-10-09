package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func probeTestSSE(events ...string) string {
	var stream strings.Builder
	for _, event := range events {
		stream.WriteString(": keepalive\r\n")
		stream.WriteString("data: " + event + "\r\n\r\n")
	}
	return stream.String()
}

func TestAnalyzeProbeSSE(t *testing.T) {
	t.Parallel()

	created := `{"type":"response.created","response":{"id":"resp_test","model":"test-model"}}`
	searchAdded := `{"type":"response.output_item.added","item":{"id":"ws_test","type":"web_search_call","status":"in_progress"}}`
	searchDone := `{"type":"response.output_item.done","item":{"id":"ws_test","type":"web_search_call","status":"completed"}}`
	functionAdded := `{"type":"response.output_item.added","item":{"id":"fc_test","type":"function_call","name":"advisor_guide","arguments":""}}`
	argumentsDone := `{"type":"response.function_call_arguments.done","item_id":"fc_test","arguments":"{\"feedback\":\"Found a source\"}"}`
	functionDone := `{"type":"response.output_item.done","item":{"id":"fc_test","type":"function_call","name":"advisor_guide","arguments":"{\"feedback\":\"Found a source\"}","status":"completed"}}`
	completed := `{"type":"response.completed","response":{"id":"resp_test","status":"completed"}}`
	serverError := `{"type":"error","code":"server_error","message":"provider request failed; request ID example"}`

	for _, tc := range []struct {
		name       string
		events     []string
		outcome    string
		transition bool
		wantError  string
	}{
		{
			name:       "completed transition",
			events:     []string{created, searchAdded, searchDone, functionAdded, argumentsDone, functionDone, completed},
			outcome:    "success",
			transition: true,
		},
		{
			name:       "server error at function start",
			events:     []string{created, searchAdded, searchDone, functionAdded, serverError},
			outcome:    "providerError",
			transition: true,
			wantError:  "server_error",
		},
		{
			name:       "nested subscription error",
			events:     []string{created, searchAdded, searchDone, functionAdded, `{"type":"error","error":{"type":"server_error","code":"server_error","message":"provider request failed; request ID example"},"sequence_number":24}`},
			outcome:    "providerError",
			transition: true,
			wantError:  "server_error",
		},
		{
			name:       "error followed by failed response",
			events:     []string{created, searchAdded, searchDone, functionAdded, serverError, `{"type":"response.failed","response":{"id":"resp_test","status":"failed","error":{"code":"server_error","message":"provider request failed; request ID example"}}}`},
			outcome:    "providerError",
			transition: true,
			wantError:  "server_error",
		},
		{
			name:      "function without search is inconclusive",
			events:    []string{created, functionAdded, argumentsDone, functionDone, completed},
			outcome:   "inconclusive",
			wantError: "web search",
		},
		{
			name:      "search after function is not the target transition",
			events:    []string{created, functionAdded, argumentsDone, functionDone, searchAdded, searchDone, completed},
			outcome:   "inconclusive",
			wantError: "web search",
		},
		{
			name:      "search completion without start",
			events:    []string{created, searchDone, functionAdded, completed},
			outcome:   "invalid",
			wantError: "unknown item",
		},
		{
			name:       "truncated after transition",
			events:     []string{created, searchAdded, searchDone, functionAdded},
			outcome:    "invalid",
			transition: true,
			wantError:  "terminal",
		},
		{
			name:      "missing created event",
			events:    []string{searchAdded, searchDone, functionAdded, serverError},
			outcome:   "invalid",
			wantError: "response.created",
		},
		{
			name:      "malformed JSON",
			events:    []string{created, `{"type":`},
			outcome:   "invalid",
			wantError: "decode",
		},
		{
			name:      "empty stream",
			outcome:   "invalid",
			wantError: "response.created",
		},
		{
			name:       "completed response without completed function",
			events:     []string{created, searchAdded, searchDone, functionAdded, completed},
			outcome:    "invalid",
			transition: true,
			wantError:  "function",
		},
		{
			name:       "invalid function arguments",
			events:     []string{created, searchAdded, searchDone, functionAdded, `{"type":"response.function_call_arguments.done","item_id":"fc_test","arguments":"{"}`},
			outcome:    "invalid",
			transition: true,
			wantError:  "arguments",
		},
		{
			name:      "event after terminal",
			events:    []string{created, serverError, searchAdded},
			outcome:   "invalid",
			wantError: "terminal",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			report, err := analyzeProbeSSE(strings.NewReader(probeTestSSE(tc.events...)), "advisor_guide")
			if tc.wantError == "" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, tc.wantError)
			}
			assert.Equal(t, tc.outcome, report.Outcome)
			assert.Equal(t, tc.transition, report.TransitionObserved)
			if tc.outcome == "providerError" {
				assert.Equal(t, "server_error", report.ErrorCode)
				assert.Contains(t, report.ErrorMessage, "request ID example")
				assert.Equal(t, 0, report.ArgumentDeltaBytes)
			}
		})
	}
}

func TestAnalyzeProbeSSEFraming(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		stream string
		want   string
	}{
		{
			name:   "multiline data is decoded",
			stream: "event: response.created\ndata: {\"type\":\"response.created\",\ndata: \"response\":{\"id\":\"resp_test\",\"model\":\"test-model\"}}\n\n",
			want:   "terminal",
		},
		{
			name:   "event and payload disagree",
			stream: "event: response.completed\ndata: {\"type\":\"response.created\"}\n\n",
			want:   "event type",
		},
		{
			name:   "unterminated frame",
			stream: "data: {\"type\":\"response.created\"}",
			want:   "unterminated",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := analyzeProbeSSE(strings.NewReader(tc.stream), "advisor_guide")
			require.ErrorContains(t, err, tc.want)
		})
	}
}

func TestValidateProbeRequest(t *testing.T) {
	t.Parallel()

	expected := json.RawMessage(`{
		"model":"test-model",
		"stream":true,
		"store":false,
		"parallel_tool_calls":true,
		"reasoning":{"effort":"max","summary":"auto"},
		"input":[{"role":"user","content":"Search, then call advisor_guide."}],
		"tools":[{"type":"web_search"},{"type":"function","name":"advisor_guide","parameters":{"type":"object","properties":{}}}]
	}`)
	for _, tc := range []struct {
		name string
		body string
	}{
		{name: "matching", body: string(expected)},
		{name: "missing fields", body: `{"model":"test-model"}`},
		{name: "wrong model", body: strings.Replace(string(expected), "test-model", "other-model", 1)},
		{name: "wrong parallel calls", body: strings.Replace(string(expected), `"parallel_tool_calls":true`, `"parallel_tool_calls":false`, 1)},
		{name: "changed history", body: strings.Replace(string(expected), "Search, then call", "Do not search; call", 1)},
		{name: "malformed", body: `{"model":`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := validateProbeRequest(expected, []byte(tc.body))
			if tc.name == "matching" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}

func TestAnalyzeProbeSSESearchOnly(t *testing.T) {
	t.Parallel()
	created := `data: {"type":"response.created","response":{"id":"resp_test","model":"test-model"}}` + "\n\n"
	search := `data: {"type":"response.output_item.added","item":{"id":"ws_test","type":"web_search_call","status":"in_progress"}}` + "\n\n" +
		`data: {"type":"response.output_item.done","item":{"id":"ws_test","type":"web_search_call","status":"completed"}}` + "\n\n"
	completed := `data: {"type":"response.completed","response":{"id":"resp_test","status":"completed"}}` + "\n\n"
	for _, tc := range []struct {
		name    string
		events  string
		outcome string
		errText string
	}{
		{"completed search", created + search + completed, "success", ""},
		{"no search", created + completed, "inconclusive", "no completed web search"},
		{"unexpected function", created + search + `data: {"type":"response.output_item.added","item":{"id":"fc_test","type":"function_call","name":"advisor_guide"}}` + "\n\n" + completed, "invalid", "unexpected function"},
		{"error after search", created + search + `data: {"type":"error","code":"server_error","message":"failed after search"}` + "\n\n", "providerError", "server_error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			report, err := analyzeProbeSSE(strings.NewReader(tc.events), "")
			assert.Equal(t, tc.outcome, report.Outcome)
			if tc.errText != "" {
				require.ErrorContains(t, err, tc.errText)
			} else {
				require.NoError(t, err)
				assert.Equal(t, 1, report.SearchesCompleted)
			}
		})
	}
}

func TestValidateProbeRequestReportsChangedFields(t *testing.T) {
	t.Parallel()
	err := validateProbeRequest(
		[]byte(`{"model":"same","input":"private prompt","tools":[]}`),
		[]byte(`{"model":"same","input":"another private prompt","reasoning":{}}`),
	)
	require.ErrorContains(t, err, "input")
	require.ErrorContains(t, err, "tools")
	require.ErrorContains(t, err, "reasoning")
	assert.NotContains(t, err.Error(), "private prompt")
}
