package llm

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseJsonValue(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "valid json array",
			input:    `[{"key": "value"}]`,
			expected: `[{"key":"value"}]`,
		},
		{
			name:     "valid json object",
			input:    `{"key": "value"}`,
			expected: `{"key":"value"}`,
		},
		{
			name:     "valid json array with whitespace",
			input:    ` [ { "key" : "value" } ] `,
			expected: `[{"key":"value"}]`,
		},
		{
			name:     "invalid json",
			input:    `[{"key": "value"`,
			expected: `[{"key": "value"`,
		},
		{
			name:     "non-json string",
			input:    `just a string`,
			expected: `just a string`,
		},
		{
			name:     "string ending with </invoke>",
			input:    `[{"key": "value"}]</invoke>`,
			expected: `[{"key":"value"}]`,
		},
		{
			name:     "string ending with </invoke> and more",
			input:    `[{"key": "value"}]</invoke> and more`,
			expected: `[{"key":"value"}]`,
		},
		{
			name:     "nested json structure",
			input:    `{"outer": {"inner": [1,2,3]}}`,
			expected: `{"outer":{"inner":[1,2,3]}}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tryParseStringAsJson(tt.input)
			marshaled, err := json.Marshal(got)
			assert.NoError(t, err)
			if string(marshaled) != tt.expected && got != tt.expected {
				t.Errorf("parseJsonValue() = %s, want %s", string(marshaled), tt.expected)
			}
		})
	}
}

func TestTryConvertStringsToRawJson(t *testing.T) {
	tests := []struct {
		name     string
		input    interface{}
		expected interface{}
	}{
		{
			name: "simple string to object conversion",
			input: map[string]interface{}{
				"data": "{\"key\": \"value\"}",
			},
			expected: map[string]interface{}{
				"data": map[string]interface{}{
					"key": "value",
				},
			},
		},
		{
			name: "invalid conversion attempt",
			input: map[string]interface{}{
				"data": "{\"key\": value}",
			},
			expected: map[string]interface{}{
				"data": "{\"key\": value}",
			},
		},
		{
			name: "multiple string conversion",
			input: map[string]interface{}{
				"data":   "{\"arr\": [1,2]}",
				"other":  "{\"x\": 1}",
				"plain":  "just a string",
				"number": 42,
			},
			expected: map[string]interface{}{
				"data": map[string]interface{}{
					"arr": []interface{}{float64(1), float64(2)},
				},
				"other": map[string]interface{}{
					"x": float64(1),
				},
				"plain":  "just a string",
				"number": 42,
			},
		},
		{
			name: "nested structure",
			input: map[string]interface{}{
				"outer": map[string]interface{}{
					"inner": "{\"x\": [1,2,3]}",
				},
			},
			expected: map[string]interface{}{
				"outer": map[string]interface{}{
					"inner": map[string]interface{}{
						"x": []interface{}{float64(1), float64(2), float64(3)},
					},
				},
			},
		},
		{
			name: "array with JSON strings",
			input: []interface{}{
				"[1,2,3]",
				"{\"key\": \"value\"}",
				"not json",
			},
			expected: []interface{}{
				[]interface{}{float64(1), float64(2), float64(3)},
				map[string]interface{}{"key": "value"},
				"not json",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tryConvertStringsToRawJson(tt.input)
			assert.Equal(t, tt.expected, got)
		})
	}
}

func TestRepairJson(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name: "newline in string",
			input: `{"key": "value with 
 newline"}`,
			expected: `{"key":"value with \n newline"}`,
		},
		{
			name:     "string containing JSON array",
			input:    `{"searches": "[{\"path\": \"file.go\"}]</invoke>"}`,
			expected: `{"searches":[{"path":"file.go"}]}`,
		},
		{
			name:     "string containing JSON object",
			input:    `{"data": "{\"key\": \"value\"}</invoke>"}`,
			expected: `{"data":{"key":"value"}}`,
		},
		{
			name:     "nested JSON with newlines and JSON strings",
			input:    `{"outer": {"inner": "{\"key\": \"value\"}</invoke>"}}`,
			expected: `{"outer":{"inner":{"key":"value"}}}`,
		},
		{
			name:     "array with JSON string and newlines",
			input:    `{"items": ["normal", "{\"key\": \"value\"}</invoke>", "plain"]}`,
			expected: `{"items":["normal",{"key":"value"},"plain"]}`,
		},
		{
			name:     "invalid JSON string should remain unchanged",
			input:    `{"data": "{\"key\": \"value\"</invoke>"}`,
			expected: `{"data":"{\"key\": \"value\"</invoke>"}`,
		},
		{
			name:     "non-JSON string ending with </invoke> should remain unchanged",
			input:    `{"data": "not json</invoke>"}`,
			expected: `{"data":"not json</invoke>"}`,
		},
		{
			input: `{"key": "value with 
 newline"}`,
			expected: `{"key":"value with \n newline"}`,
		},
		{
			input:    "{\"key\": \"value with \r\n newline\"}",
			expected: `{"key":"value with \r\n newline"}`,
		},
		{
			input:    `{"key": "value without newline"}`,
			expected: `{"key":"value without newline"}`,
		},
		{
			input:    `{"key": "value with \n escaped newline"}`,
			expected: `{"key":"value with \n escaped newline"}`,
		},
		{
			input: `{"key": "value with 
 multiple 
 newlines"}`,
			expected: `{"key":"value with \n multiple \n newlines"}`,
		},
		{
			input: `{"key": "value with valid escape: \" and 
 newline"}`,
			expected: `{"key":"value with valid escape: \" and \n newline"}`,
		},
		{
			input: `{"key1": "value1",
"key2": "value2"}`,
			expected: `{"key1":"value1","key2":"value2"}`,
		},
		{
			input: `{"nested": {"key": "value with 
 newline"}}`,
			expected: `{"nested":{"key":"value with \n newline"}}`,
		},
	}

	for _, test := range tests {
		got := RepairJson(test.input)
		if got != test.expected {
			t.Errorf("For input %q\nexpected %q\n but got %q", test.input, test.expected, got)
		}
	}
}
