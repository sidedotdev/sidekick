package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestProbeCaptureContinuation(t *testing.T) {
	t.Parallel()

	const original = `{"model":"test-model","stream":true,"input":[{"role":"user","content":"Search"}],"tools":[{"type":"function","name":"builtin_web_search"},{"type":"function","name":"advisor_guide"}],"tool_choice":"required","parallel_tool_calls":true}`
	const searchOnly = `{"model":"test-model","stream":true,"input":[{"role":"user","content":"Search"}],"tools":[{"type":"function","name":"builtin_web_search"}],"tool_choice":"required","parallel_tool_calls":true}`

	for _, tc := range []struct {
		name      string
		original  string
		changes   map[string]json.RawMessage
		wantError bool
	}{
		{
			name: "input only",
		},
		{
			name: "search withdrawn",
			changes: map[string]json.RawMessage{
				"tools": json.RawMessage(`[{"type":"function","name":"advisor_guide"}]`),
			},
		},
		{
			name:     "last tool withdrawn",
			original: searchOnly,
			changes: map[string]json.RawMessage{
				"tools":       nil,
				"tool_choice": nil,
			},
		},
		{
			name: "model changed",
			changes: map[string]json.RawMessage{
				"model": json.RawMessage(`"other-model"`),
			},
			wantError: true,
		},
		{
			name: "other function changed",
			changes: map[string]json.RawMessage{
				"tools": json.RawMessage(`[{"type":"function","name":"builtin_web_search"},{"type":"function","name":"other_function"}]`),
			},
			wantError: true,
		},
		{
			name: "tool choice changed",
			changes: map[string]json.RawMessage{
				"tool_choice": json.RawMessage(`"auto"`),
			},
			wantError: true,
		},
		{
			name: "input removed",
			changes: map[string]json.RawMessage{
				"input": nil,
			},
			wantError: true,
		},
		{
			name: "input null",
			changes: map[string]json.RawMessage{
				"input": json.RawMessage(`null`),
			},
			wantError: true,
		},
		{
			name: "parallel setting changed",
			changes: map[string]json.RawMessage{
				"parallel_tool_calls": json.RawMessage(`false`),
			},
			wantError: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			initial := tc.original
			if initial == "" {
				initial = original
			}
			var fields map[string]json.RawMessage
			require.NoError(t, json.Unmarshal([]byte(initial), &fields))
			fields["input"] = json.RawMessage(`[{"role":"user","content":"Search"},{"type":"function_call_output","call_id":"search-call","output":"Search results"}]`)
			for key, value := range tc.changes {
				if value == nil {
					delete(fields, key)
				} else {
					fields[key] = value
				}
			}
			continuation, err := json.Marshal(fields)
			require.NoError(t, err)

			var calls atomic.Int32
			const responseBody = "data: fixture\n\n"
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, responseBody)
			}))
			defer server.Close()

			transport := &probeCaptureTransport{
				base:              server.Client().Transport,
				allowContinuation: true,
				expected: &probeCapturedRequest{
					URL:    server.URL + "/responses",
					Header: http.Header{"Authorization": {"Bearer fixture"}},
					Body:   []byte(initial),
				},
			}
			send := func(body string) error {
				t.Helper()
				request, err := http.NewRequest(http.MethodPost, server.URL+"/responses", strings.NewReader(body))
				require.NoError(t, err)
				request.Header.Set("Authorization", "Bearer fixture")
				response, err := transport.RoundTrip(request)
				if err != nil {
					return err
				}
				_, err = io.Copy(io.Discard, response.Body)
				require.NoError(t, err)
				return response.Body.Close()
			}
			require.NoError(t, send(initial))
			err = send(string(continuation))
			if tc.wantError {
				require.Error(t, err)
				require.EqualValues(t, 1, calls.Load(), "invalid continuation must not be sent")
				require.Len(t, transport.exchanges, 1)
			} else {
				require.NoError(t, err)
				require.EqualValues(t, 2, calls.Load())
				require.Len(t, transport.exchanges, 2)
				require.JSONEq(t, string(continuation), string(transport.exchanges[1].RequestBody))
				require.Equal(t, responseBody, transport.exchanges[1].ResponseBody.String())
			}
			require.JSONEq(t, initial, string(transport.exchanges[0].RequestBody))
			require.Equal(t, responseBody, transport.exchanges[0].ResponseBody.String())
		})
	}
}
