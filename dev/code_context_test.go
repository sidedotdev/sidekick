package dev

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRequiredCodeContextUnmarshalJSON(t *testing.T) {
	tests := []struct {
		name             string
		jsonInput        string
		expectNil        bool
		expectedLen      int
		expectedFilePath string
	}{
		{
			name:        "explicit empty requests array",
			jsonInput:   `{"requests": []}`,
			expectNil:   false,
			expectedLen: 0,
		},
		{
			name:        "missing requests field",
			jsonInput:   `{"analysis": "test"}`,
			expectNil:   true,
			expectedLen: 0,
		},
		{
			name:             "requests with one element",
			jsonInput:        `{"requests": [{"file_path": "test.go"}]}`,
			expectNil:        false,
			expectedLen:      1,
			expectedFilePath: "test.go",
		},
		{
			name:        "legacy field explicit empty",
			jsonInput:   `{"code_context_requests": []}`,
			expectNil:   false,
			expectedLen: 0,
		},
		{
			name:             "legacy field with one element",
			jsonInput:        `{"code_context_requests": [{"file_path": "legacy.go"}]}`,
			expectNil:        false,
			expectedLen:      1,
			expectedFilePath: "legacy.go",
		},
		{
			name:             "both fields present - new takes precedence",
			jsonInput:        `{"requests": [{"file_path": "new.go"}], "code_context_requests": [{"file_path": "old.go"}]}`,
			expectNil:        false,
			expectedLen:      1,
			expectedFilePath: "new.go",
		},
		{
			name:        "new field empty, legacy has value - new takes precedence",
			jsonInput:   `{"requests": [], "code_context_requests": [{"file_path": "old.go"}]}`,
			expectNil:   false,
			expectedLen: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var rcc RequiredCodeContext
			err := json.Unmarshal([]byte(tt.jsonInput), &rcc)
			require.NoError(t, err)

			if tt.expectNil {
				assert.Nil(t, rcc.Requests, "expected Requests to be nil")
			} else {
				assert.NotNil(t, rcc.Requests, "expected Requests to be non-nil")
				assert.Len(t, rcc.Requests, tt.expectedLen)
				if tt.expectedLen > 0 && tt.expectedFilePath != "" {
					assert.Equal(t, tt.expectedFilePath, rcc.Requests[0].FilePath)
				}
			}
		})
	}
}
