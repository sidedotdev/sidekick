package main

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProbeAlphaSearchURL(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		input    string
		expected string
		errText  string
	}{
		{"codex subscription", "https://chatgpt.com/backend-api/codex/responses", "https://chatgpt.com/backend-api/codex/alpha/search", ""},
		{"api", "https://api.openai.com/v1/responses", "https://api.openai.com/v1/alpha/search", ""},
		{"not responses", "https://api.openai.com/v1/chat/completions", "", "responses"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			actual, err := probeAlphaSearchURL(tc.input)
			if tc.errText != "" {
				require.ErrorContains(t, err, tc.errText)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.expected, actual)
		})
	}
}

func TestBuildProbeHostedSearchBody(t *testing.T) {
	t.Parallel()
	input := json.RawMessage(`[{"role":"user","content":"Search."}]`)
	for _, variant := range probeHostedSearchToolVariants {
		t.Run(variant.Name, func(t *testing.T) {
			t.Parallel()
			body, err := buildProbeHostedSearchBody("test-model", input, variant.Tool)
			require.NoError(t, err)
			var fields map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(body, &fields))
			require.JSONEq(t, `"test-model"`, string(fields["model"]))
			require.JSONEq(t, string(input), string(fields["input"]))
			require.JSONEq(t, `false`, string(fields["store"]))
			require.JSONEq(t, `true`, string(fields["stream"]))
			var tools []map[string]any
			require.NoError(t, json.Unmarshal(fields["tools"], &tools))
			require.Len(t, tools, 1)
			require.Equal(t, "web_search", tools[0]["type"])
			require.Len(t, fields, 5)
		})
	}

	body, err := buildProbeHostedSearchBody("test-model", input, json.RawMessage(`{"type":"web_search","external_web_access":true}`))
	require.NoError(t, err)
	require.JSONEq(t, `{
		"model":"test-model",
		"input":[{"role":"user","content":"Search."}],
		"store":false,
		"stream":true,
		"tools":[{"type":"web_search","external_web_access":true}]
	}`, string(body))

	_, err = buildProbeHostedSearchBody("", input, probeHostedSearchToolVariants[0].Tool)
	require.Error(t, err)
	_, err = buildProbeHostedSearchBody("test-model", nil, probeHostedSearchToolVariants[0].Tool)
	require.Error(t, err)
	_, err = buildProbeHostedSearchBody("test-model", input, json.RawMessage(`{"type":"function"}`))
	require.ErrorContains(t, err, "web_search")
}

func TestValidateProbeAlphaSearchResponse(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		status  int
		body    string
		output  bool
		results int
		errText string
	}{
		{"output and results", 200, `{"output":"Go 1.99 is current.","encrypted_output":null,"results":[{"url":"https://go.dev"}]}`, true, 1, ""},
		{"results only", 200, `{"output":null,"results":[{"url":"https://go.dev"}]}`, false, 1, ""},
		{"empty", 200, `{"output":"","results":[]}`, false, 0, "neither output nor results"},
		{"http error", 400, `{"detail":"missing model"}`, false, 0, "HTTP 400"},
		{"malformed", 200, `{"output":`, false, 0, "decode"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			report, err := validateProbeAlphaSearchResponse(tc.status, []byte(tc.body))
			assert.Equal(t, tc.status, report.HTTPStatus)
			if tc.errText != "" {
				require.ErrorContains(t, err, tc.errText)
				assert.Equal(t, "invalid", report.Outcome)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, "success", report.Outcome)
			assert.Equal(t, tc.output, report.OutputBytes > 0)
			assert.Equal(t, tc.results, report.ResultCount)
		})
	}
}
