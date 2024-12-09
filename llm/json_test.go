package llm

import (
	"testing"
)

func TestRepairJson(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{
			input: `{"key": "value with 
 newline"}`,
			expected: `{"key": "value with \n newline"}`,
		},
		{
			input:    "{\"key\": \"value with \r\n newline\"}",
			expected: `{"key": "value with \r\n newline"}`,
		},
		{
			input:    `{"key": "value without newline"}`,
			expected: `{"key": "value without newline"}`,
		},
		{
			input:    `{"key": "value with \n escaped newline"}`,
			expected: `{"key": "value with \n escaped newline"}`,
		},
		{
			input: `{"key": "value with 
 multiple 
 newlines"}`,
			expected: `{"key": "value with \n multiple \n newlines"}`,
		},
		{
			input: `{"key": "value with valid escape: \" and 
 newline"}`,
			expected: `{"key": "value with valid escape: \" and \n newline"}`,
		},
		{
			input: `{"key1": "value1",
"key2": "value2"}`,
			expected: `{"key1": "value1",
"key2": "value2"}`,
		},
		{
			input: `{"nested": {"key": "value with 
 newline"}}`,
			expected: `{"nested": {"key": "value with \n newline"}}`,
		},
	}

	for _, test := range tests {
		got := RepairJson(test.input)
		if got != test.expected {
			t.Errorf("For input %q, expected %q, but got %q", test.input, test.expected, got)
		}
	}
}
