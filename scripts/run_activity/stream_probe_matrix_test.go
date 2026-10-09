package main

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBuildProbeSDKParams(t *testing.T) {
	t.Parallel()
	request := []byte(`{
		"model":"test-model",
		"stream":true,
		"store":false,
		"parallel_tool_calls":true,
		"reasoning":{"effort":"high","summary":"auto"},
		"include":["reasoning.encrypted_content"],
		"tool_choice":"required",
		"input":[{"role":"user","content":"Search first."}],
		"tools":[
			{"type":"web_search"},
			{"type":"function","name":"advisor_guide","description":"Report results","parameters":{"type":"object","properties":{},"required":[]}}
		]
	}`)
	params, err := buildProbeSDKParams(request, "Search first.")
	require.NoError(t, err)
	body, err := json.Marshal(params)
	require.NoError(t, err)
	var expected, actual map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(request, &expected))
	require.NoError(t, json.Unmarshal(body, &actual))
	delete(expected, "stream")
	expectedJSON, err := json.Marshal(expected)
	require.NoError(t, err)
	actualJSON, err := json.Marshal(actual)
	require.NoError(t, err)
	require.JSONEq(t, string(expectedJSON), string(actualJSON))
	require.NoError(t, validateProbeRequest(expectedJSON, actualJSON))
}

func TestBuildProbeSDKParamsRejectsUncontrolledFields(t *testing.T) {
	t.Parallel()
	for _, body := range []string{
		`{"model":"test","stream":true,"unexpected":true}`,
		`{"stream":true}`,
		`{"model":"test","stream":false}`,
		`{"model":"test","stream":true,"tools":[{"type":"unknown"}]}`,
		`{"model":"test","stream":true,"tool_choice":{"type":"function","name":"advisor_guide"}}`,
	} {
		_, err := buildProbeSDKParams([]byte(body), "")
		require.Error(t, err, body)
	}
}

func TestMinimalProbeToolsMatchSDK(t *testing.T) {
	t.Parallel()
	tools, err := minimalProbeTools()
	require.NoError(t, err)
	require.Len(t, tools, 2)
	require.Equal(t, "advisor_guide", tools[0].Name)
	require.Equal(t, "web_search", string(tools[1].Type))

	body, err := json.Marshal(tools[0].Parameters)
	require.NoError(t, err)
	require.JSONEq(t, `{"type":"object","properties":{"feedback":{"type":"string"}},"required":["feedback"],"additionalProperties":false}`, string(body))

	sdkTools := minimalProbeSDKTools()
	require.Len(t, sdkTools, 2)
	body, err = json.Marshal(sdkTools)
	require.NoError(t, err)
	require.JSONEq(t, `[
		{"type":"function","name":"advisor_guide","description":"Report the result of the web search.","parameters":{"type":"object","properties":{"feedback":{"type":"string"}},"required":["feedback"],"additionalProperties":false}},
		{"type":"web_search"}
	]`, string(body))
}

func TestProbeRequestVariant(t *testing.T) {
	t.Parallel()
	original := &probeCapturedRequest{
		URL:  "https://example.test/responses",
		Body: []byte(`{"model":"test","tool_choice":"required","reasoning":{"effort":"max","summary":"auto"},"tools":[{"type":"web_search"}]}`),
	}
	variant, err := probeRequestVariant(original, map[string]json.RawMessage{
		"tool_choice": json.RawMessage(`"auto"`),
		"reasoning":   nil,
	})
	require.NoError(t, err)
	require.Equal(t, original.URL, variant.URL)
	require.JSONEq(t, `{"model":"test","tool_choice":"auto","tools":[{"type":"web_search"}]}`, string(variant.Body))
	require.JSONEq(t, `{"model":"test","tool_choice":"required","reasoning":{"effort":"max","summary":"auto"},"tools":[{"type":"web_search"}]}`, string(original.Body))
	_, err = probeRequestVariant(original, map[string]json.RawMessage{"missing": nil})
	require.ErrorContains(t, err, "missing")
	_, err = probeRequestVariant(original, map[string]json.RawMessage{"tool_choice": json.RawMessage(`invalid`)})
	require.Error(t, err)
}
