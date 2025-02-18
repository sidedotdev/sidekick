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
		{
			name:     "basic string to JSON object conversion",
			input:    `{"data": "{\"key\":\"value\",\"num\":42}"}`,
			expected: `{"data":{"key":"value","num":42}}`,
		},
		{
			name:     "string to JSON array conversion",
			input:    `{"items": "[1,2,3,{\"x\":\"y\"}]"}`,
			expected: `{"items":[1,2,3,{"x":"y"}]}`,
		},
		{
			name:     "multiple string conversions",
			input:    `{"obj": "{\"a\":1}", "arr": "[1,2]", "plain": "text"}`,
			expected: `{"obj":{"a":1},"arr":[1,2],"plain":"text"}`,
		},
		{
			name:     "nested string conversions",
			input:    `{"outer": {"inner": "{\"deep\": {\"x\": 1}}", "arr": "[1,2]"}}`,
			expected: `{"outer":{"inner":{"deep":{"x":1}},"arr":[1,2]}}`,
		},
		{
			name:     "invalid JSON remains as string",
			input:    `{"data": "{\"broken\": missing quote}"}`,
			expected: `{"data":"{\"broken\": missing quote}"}`,
		},
		{
			name:     "mixed valid and invalid conversions",
			input:    `{"valid": "{\"x\":1}", "invalid": "{broken}", "also_valid": "[1,2]"}`,
			expected: `{"valid":{"x":1},"invalid":"{broken}","also_valid":[1,2]}`,
		},
		{
			name:     "complex nested structure with mixed conversions",
			input:    `{"a": {"b": "{\"x\":1}", "c": {"d": "[1,2]", "e": "{invalid}", "f": "{\"y\":{\"z\":3}}"}, "g": "plain"}}`,
			expected: `{"a":{"b":{"x":1},"c":{"d":[1,2],"e":"{invalid}","f":{"y":{"z":3}}},"g":"plain"}}`,
		},
	}

	for _, test := range tests {
		got := RepairJson(test.input)

		var expectedJSON, gotJSON interface{}
		if err := json.Unmarshal([]byte(test.expected), &expectedJSON); err != nil {
			t.Errorf("Failed to parse expected JSON %q: %v", test.expected, err)
			continue
		}
		if err := json.Unmarshal([]byte(got), &gotJSON); err != nil {
			t.Errorf("Failed to parse actual JSON %q: %v", got, err)
			continue
		}

		assert.Equal(t, expectedJSON, gotJSON, "For input %q\nexpected JSON equivalent to %q\nbut got %q",
			test.input, test.expected, got)
	}
}
