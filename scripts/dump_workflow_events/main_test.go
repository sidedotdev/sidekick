package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSummarizePayload(t *testing.T) {
	t.Parallel()

	payload := `{
		"messages": [
			{"role":"assistant","content":[
				{"type":"tool_use","toolUse":{"id":"call_matched","name":"read","arguments":"{}"}},
				{"type":"tool_use","toolUse":{"id":"call_missing","name":"search","arguments":"{}"}}
			]},
			{"role":"user","content":[
				{"type":"tool_result","toolResult":{"toolCallId":"call_matched","content":[]}},
				{"type":"tool_result","toolResult":{"toolCallId":"call_orphaned","content":[]}}
			]}
		]
	}`

	summary := summarizePayload(t.Context(), nil, payload)

	assert.Contains(t, summary, "messages=2")
	assert.Contains(t, summary, "toolCalls=2")
	assert.Contains(t, summary, "toolResults=2")
	assert.Contains(t, summary, "missingResults=call_missing")
	assert.Contains(t, summary, "orphanedResults=call_orphaned")
	assert.NotContains(t, summary, "call_matched")
}

func TestSummarizePayload_ValidToolHistory(t *testing.T) {
	t.Parallel()

	payload := `{"messages":[
		{"role":"assistant","content":[
			{"toolUse":{"id":"call_1"}},
			{"toolUse":{"id":"call_2"}}
		]},
		{"role":"user","content":[
			{"toolResult":{"toolCallId":"call_1"}},
			{"toolResult":{"toolCallId":"call_2"}}
		]}
	]}`

	summary := summarizePayload(t.Context(), nil, payload)

	assert.Contains(t, summary, "messages=2")
	assert.Contains(t, summary, "toolCalls=2")
	assert.Contains(t, summary, "toolResults=2")
	assert.False(t, strings.Contains(summary, "missingResults="))
	assert.False(t, strings.Contains(summary, "orphanedResults="))
}

func TestSummarizePayload_NonJSON(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "bytes=10 nonJSON", summarizePayload(t.Context(), nil, "plain text"))
}

func TestSummarizePayload_DoesNotTreatPartialMessageAsCompleteHistory(t *testing.T) {
	t.Parallel()

	payload := `{"Message":{"role":"user","content":[
		{"toolResult":{"toolCallId":"call_from_previous_message"}}
	]}}`

	summary := summarizePayload(t.Context(), nil, payload)

	assert.Contains(t, summary, "toolResults=1")
	assert.NotContains(t, summary, "orphanedResults=")
}
