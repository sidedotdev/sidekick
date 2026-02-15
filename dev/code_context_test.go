package dev

import (
	"encoding/json"
	"fmt"
	"testing"

	"sidekick/coding"
	"sidekick/llm"
	"sidekick/llm2"

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

func TestToolCallWithCodeContext(t *testing.T) {
	t.Parallel()

	t.Run("single valid tool call", func(t *testing.T) {
		t.Parallel()
		toolCalls := []ToolCallWithCodeContext{
			{
				ToolCall: llm.ToolCall{
					Id:   "call_1",
					Name: "get_symbol_definitions",
				},
				RequiredCodeContext: RequiredCodeContext{
					Analysis: "Test analysis",
					Requests: []coding.FileSymDefRequest{
						{FilePath: "foo/bar.go", SymbolNames: []string{"Func1"}},
					},
				},
			},
		}

		// Verify the structure is correct
		assert.Len(t, toolCalls, 1)
		assert.Equal(t, "call_1", toolCalls[0].ToolCall.Id)
		assert.Nil(t, toolCalls[0].Err)
		assert.Len(t, toolCalls[0].RequiredCodeContext.Requests, 1)
		assert.Equal(t, "foo/bar.go", toolCalls[0].RequiredCodeContext.Requests[0].FilePath)
	})

	t.Run("multiple valid tool calls", func(t *testing.T) {
		t.Parallel()
		toolCalls := []ToolCallWithCodeContext{
			{
				ToolCall: llm.ToolCall{
					Id:   "call_1",
					Name: "get_symbol_definitions",
				},
				RequiredCodeContext: RequiredCodeContext{
					Analysis: "First analysis",
					Requests: []coding.FileSymDefRequest{
						{FilePath: "foo/bar.go", SymbolNames: []string{"Func1"}},
					},
				},
			},
			{
				ToolCall: llm.ToolCall{
					Id:   "call_2",
					Name: "get_symbol_definitions",
				},
				RequiredCodeContext: RequiredCodeContext{
					Analysis: "Second analysis",
					Requests: []coding.FileSymDefRequest{
						{FilePath: "baz/qux.go", SymbolNames: []string{"Func2", "Func3"}},
					},
				},
			},
		}

		// Verify multiple tool calls are preserved in order
		assert.Len(t, toolCalls, 2)
		assert.Equal(t, "call_1", toolCalls[0].ToolCall.Id)
		assert.Equal(t, "call_2", toolCalls[1].ToolCall.Id)
		assert.Equal(t, "foo/bar.go", toolCalls[0].RequiredCodeContext.Requests[0].FilePath)
		assert.Equal(t, "baz/qux.go", toolCalls[1].RequiredCodeContext.Requests[0].FilePath)
	})

	t.Run("second tool call has error", func(t *testing.T) {
		t.Parallel()
		toolCalls := []ToolCallWithCodeContext{
			{
				ToolCall: llm.ToolCall{
					Id:   "call_1",
					Name: "get_symbol_definitions",
				},
				RequiredCodeContext: RequiredCodeContext{
					Analysis: "Valid analysis",
					Requests: []coding.FileSymDefRequest{
						{FilePath: "foo/bar.go", SymbolNames: []string{"Func1"}},
					},
				},
			},
			{
				ToolCall: llm.ToolCall{
					Id:   "call_2",
					Name: "get_symbol_definitions",
				},
				Err: llm.ErrToolCallUnmarshal,
			},
		}

		// Verify error is captured for specific tool call
		assert.Len(t, toolCalls, 2)
		assert.Nil(t, toolCalls[0].Err)
		assert.NotNil(t, toolCalls[1].Err)
		assert.ErrorIs(t, toolCalls[1].Err, llm.ErrToolCallUnmarshal)
		assert.Equal(t, "call_2", toolCalls[1].ToolCall.Id)
	})

	t.Run("merge requests preserves order", func(t *testing.T) {
		t.Parallel()
		toolCalls := []ToolCallWithCodeContext{
			{
				ToolCall: llm.ToolCall{Id: "call_1", Name: "get_symbol_definitions"},
				RequiredCodeContext: RequiredCodeContext{
					Analysis: "First",
					Requests: []coding.FileSymDefRequest{
						{FilePath: "a.go", SymbolNames: []string{"A"}},
						{FilePath: "b.go", SymbolNames: []string{"B"}},
					},
				},
			},
			{
				ToolCall: llm.ToolCall{Id: "call_2", Name: "get_symbol_definitions"},
				RequiredCodeContext: RequiredCodeContext{
					Analysis: "Second",
					Requests: []coding.FileSymDefRequest{
						{FilePath: "c.go", SymbolNames: []string{"C"}},
					},
				},
			},
			{
				ToolCall: llm.ToolCall{Id: "call_3", Name: "get_symbol_definitions"},
				RequiredCodeContext: RequiredCodeContext{
					Analysis: "Third",
					Requests: []coding.FileSymDefRequest{
						{FilePath: "d.go", SymbolNames: []string{"D"}},
					},
				},
			},
		}

		// Use the actual mergeToolCallRequests function
		merged := mergeToolCallRequests(toolCalls)

		// Verify order is preserved
		require.Len(t, merged.Requests, 4)
		assert.Equal(t, "a.go", merged.Requests[0].FilePath)
		assert.Equal(t, "b.go", merged.Requests[1].FilePath)
		assert.Equal(t, "c.go", merged.Requests[2].FilePath)
		assert.Equal(t, "d.go", merged.Requests[3].FilePath)
		assert.Equal(t, "First\nSecond\nThird", merged.Analysis)
	})
}

func TestToolCallResponseInfoForMultipleToolCalls(t *testing.T) {
	t.Parallel()

	t.Run("feedback references correct tool call id", func(t *testing.T) {
		t.Parallel()
		// Simulate creating feedback for a specific malformed tool call
		toolCall := llm.ToolCall{
			Id:   "call_malformed",
			Name: "get_symbol_definitions",
		}
		err := llm.ErrToolCallUnmarshal

		response := err.Error() + "\n\nHint: To fix this, follow the json schema correctly."
		toolCallResponseInfo := ToolCallResponseInfo{
			ToolResultContent: llm2.TextContentBlocks(response),
			ToolCallId:        toolCall.Id,
			FunctionName:      toolCall.Name,
		}

		assert.Equal(t, "call_malformed", toolCallResponseInfo.ToolCallId)
		assert.Equal(t, "get_symbol_definitions", toolCallResponseInfo.FunctionName)
		assert.Contains(t, toolCallResponseInfo.TextResponse(), "failed to unmarshal json")
	})
}

func TestParseToolCallsToCodeContext(t *testing.T) {
	t.Parallel()

	t.Run("parses multiple valid tool calls preserving order", func(t *testing.T) {
		t.Parallel()
		toolCalls := []llm.ToolCall{
			{
				Id:        "call_1",
				Name:      "get_symbol_definitions",
				Arguments: `{"analysis": "First", "requests": [{"file_path": "foo/bar.go", "symbol_names": ["Func1"]}]}`,
			},
			{
				Id:        "call_2",
				Name:      "get_symbol_definitions",
				Arguments: `{"analysis": "Second", "requests": [{"file_path": "baz/qux.go", "symbol_names": ["Func2"]}]}`,
			},
		}

		results := parseToolCallsToCodeContext(toolCalls)

		require.Len(t, results, 2)
		assert.Equal(t, "call_1", results[0].ToolCall.Id)
		assert.Equal(t, "call_2", results[1].ToolCall.Id)
		assert.Nil(t, results[0].Err)
		assert.Nil(t, results[1].Err)
		assert.Equal(t, "foo/bar.go", results[0].RequiredCodeContext.Requests[0].FilePath)
		assert.Equal(t, "baz/qux.go", results[1].RequiredCodeContext.Requests[0].FilePath)
	})

	t.Run("second tool call malformed sets error on that call only", func(t *testing.T) {
		t.Parallel()
		toolCalls := []llm.ToolCall{
			{
				Id:        "call_1",
				Name:      "get_symbol_definitions",
				Arguments: `{"analysis": "Valid", "requests": [{"file_path": "foo/bar.go", "symbol_names": ["Func1"]}]}`,
			},
			{
				Id:        "call_2_malformed",
				Name:      "get_symbol_definitions",
				Arguments: `{"analysis": "Invalid JSON missing requests`,
			},
		}

		results := parseToolCallsToCodeContext(toolCalls)

		require.Len(t, results, 2)
		assert.Nil(t, results[0].Err, "first tool call should have no error")
		assert.NotNil(t, results[1].Err, "second tool call should have error")
		assert.Equal(t, "call_2_malformed", results[1].ToolCall.Id)
		assert.ErrorIs(t, results[1].Err, llm.ErrToolCallUnmarshal)
	})

	t.Run("tool call missing requests field sets error", func(t *testing.T) {
		t.Parallel()
		toolCalls := []llm.ToolCall{
			{
				Id:        "call_missing_requests",
				Name:      "get_symbol_definitions",
				Arguments: `{"analysis": "No requests field"}`,
			},
		}

		results := parseToolCallsToCodeContext(toolCalls)

		require.Len(t, results, 1)
		assert.NotNil(t, results[0].Err)
		assert.ErrorIs(t, results[0].Err, llm.ErrToolCallUnmarshal)
		assert.Contains(t, results[0].Err.Error(), "missing requests")
	})

	t.Run("merging requests preserves order across tool calls", func(t *testing.T) {
		t.Parallel()
		results := []ToolCallWithCodeContext{
			{
				ToolCall: llm.ToolCall{Id: "call_1"},
				RequiredCodeContext: RequiredCodeContext{
					Requests: []coding.FileSymDefRequest{
						{FilePath: "a.go"},
						{FilePath: "b.go"},
					},
				},
			},
			{
				ToolCall: llm.ToolCall{Id: "call_2"},
				RequiredCodeContext: RequiredCodeContext{
					Requests: []coding.FileSymDefRequest{
						{FilePath: "c.go"},
					},
				},
			},
		}

		merged := mergeToolCallRequests(results)

		require.Len(t, merged.Requests, 3)
		assert.Equal(t, "a.go", merged.Requests[0].FilePath)
		assert.Equal(t, "b.go", merged.Requests[1].FilePath)
		assert.Equal(t, "c.go", merged.Requests[2].FilePath)
	})

	t.Run("end-to-end: parse multiple tool calls and merge", func(t *testing.T) {
		t.Parallel()
		// Simulates what ForceToolRetrieveCodeContext does: parse tool calls,
		// then what codeContextLoop does: merge the results
		toolCalls := []llm.ToolCall{
			{
				Id:        "call_1",
				Name:      "get_symbol_definitions",
				Arguments: `{"analysis": "First analysis", "requests": [{"file_path": "foo/bar.go", "symbol_names": ["Func1"]}]}`,
			},
			{
				Id:        "call_2",
				Name:      "get_symbol_definitions",
				Arguments: `{"analysis": "Second analysis", "requests": [{"file_path": "baz/qux.go", "symbol_names": ["Func2", "Func3"]}]}`,
			},
			{
				Id:        "call_3",
				Name:      "get_symbol_definitions",
				Arguments: `{"analysis": "Third analysis", "requests": [{"file_path": "pkg/util.go", "symbol_names": ["Helper"]}]}`,
			},
		}

		// Parse all tool calls (as ForceToolRetrieveCodeContext does)
		results := parseToolCallsToCodeContext(toolCalls)

		// Verify all tool calls were parsed successfully
		require.Len(t, results, 3)
		for i, result := range results {
			assert.Nil(t, result.Err, "tool call %d should have no error", i)
		}

		// Merge the results (as codeContextLoop does)
		merged := mergeToolCallRequests(results)

		// Verify merged requests preserve order
		require.Len(t, merged.Requests, 3)
		assert.Equal(t, "foo/bar.go", merged.Requests[0].FilePath)
		assert.Equal(t, "baz/qux.go", merged.Requests[1].FilePath)
		assert.Equal(t, "pkg/util.go", merged.Requests[2].FilePath)

		// Verify analysis is merged
		assert.Equal(t, "First analysis\nSecond analysis\nThird analysis", merged.Analysis)
	})

	t.Run("end-to-end: second tool call malformed generates per-call feedback", func(t *testing.T) {
		t.Parallel()
		// Simulates what happens when one tool call is malformed
		toolCalls := []llm.ToolCall{
			{
				Id:        "call_valid",
				Name:      "get_symbol_definitions",
				Arguments: `{"analysis": "Valid", "requests": [{"file_path": "valid.go", "symbol_names": ["Valid"]}]}`,
			},
			{
				Id:        "call_malformed",
				Name:      "get_symbol_definitions",
				Arguments: `{"analysis": "Invalid JSON`,
			},
			{
				Id:        "call_also_valid",
				Name:      "get_symbol_definitions",
				Arguments: `{"analysis": "Also valid", "requests": [{"file_path": "also_valid.go", "symbol_names": ["AlsoValid"]}]}`,
			},
		}

		// Parse all tool calls
		results := parseToolCallsToCodeContext(toolCalls)

		// Verify parsing results
		require.Len(t, results, 3)
		assert.Nil(t, results[0].Err, "first tool call should succeed")
		assert.NotNil(t, results[1].Err, "second tool call should fail")
		assert.Nil(t, results[2].Err, "third tool call should succeed")

		// Verify the malformed tool call has the correct ID for feedback
		assert.Equal(t, "call_malformed", results[1].ToolCall.Id)
		assert.ErrorIs(t, results[1].Err, llm.ErrToolCallUnmarshal)

		// Use the helper function to generate feedback (as codeContextLoop does)
		feedbacks, hasUnmarshalError, fatalErr := checkToolCallUnmarshalErrors(results)

		// Verify feedback references the correct tool call
		assert.Nil(t, fatalErr)
		assert.True(t, hasUnmarshalError)
		require.Len(t, feedbacks, 1)
		assert.Equal(t, "call_malformed", feedbacks[0].ToolCallId)
		assert.Equal(t, "get_symbol_definitions", feedbacks[0].FunctionName)
		assert.Contains(t, feedbacks[0].TextResponse(), "failed to unmarshal json")
	})

	t.Run("codeContextLoop behavior: errors trigger feedback, valid calls are merged", func(t *testing.T) {
		t.Parallel()
		// This test verifies the exact behavior of codeContextLoop when processing
		// multiple tool calls where some are malformed:
		// 1. parseToolCallsToCodeContext (used by ForceToolRetrieveCodeContext) parses all tool calls
		// 2. checkToolCallUnmarshalErrors checks for errors and generates feedback
		// 3. If any have errors, the loop retries
		// 4. If all are valid, mergeToolCallRequests combines them

		toolCalls := []llm.ToolCall{
			{
				Id:        "call_1",
				Name:      "get_symbol_definitions",
				Arguments: `{"analysis": "First", "requests": [{"file_path": "a.go", "symbol_names": ["A"]}]}`,
			},
			{
				Id:        "call_2_bad",
				Name:      "get_symbol_definitions",
				Arguments: `{"analysis": "Missing requests field"}`, // Missing requests
			},
			{
				Id:        "call_3",
				Name:      "get_symbol_definitions",
				Arguments: `{"analysis": "Third", "requests": [{"file_path": "c.go", "symbol_names": ["C"]}]}`,
			},
		}

		results := parseToolCallsToCodeContext(toolCalls)

		// Use the helper function (as codeContextLoop does)
		feedbacks, hasUnmarshalError, fatalErr := checkToolCallUnmarshalErrors(results)

		// Verify that only the malformed tool call generates feedback
		assert.Nil(t, fatalErr, "should not have fatal error")
		assert.True(t, hasUnmarshalError, "should detect unmarshal error")
		require.Len(t, feedbacks, 1, "only one feedback should be generated")
		assert.Equal(t, "call_2_bad", feedbacks[0].ToolCallId, "feedback should reference the malformed tool call")
		assert.Contains(t, feedbacks[0].TextResponse(), "missing requests")

		// When there are no errors, mergeToolCallRequests is called
		// Simulate a retry where all tool calls are valid
		validToolCalls := []llm.ToolCall{
			{
				Id:        "call_1",
				Name:      "get_symbol_definitions",
				Arguments: `{"analysis": "First", "requests": [{"file_path": "a.go", "symbol_names": ["A"]}]}`,
			},
			{
				Id:        "call_2_fixed",
				Name:      "get_symbol_definitions",
				Arguments: `{"analysis": "Second", "requests": [{"file_path": "b.go", "symbol_names": ["B"]}]}`,
			},
			{
				Id:        "call_3",
				Name:      "get_symbol_definitions",
				Arguments: `{"analysis": "Third", "requests": [{"file_path": "c.go", "symbol_names": ["C"]}]}`,
			},
		}

		validResults := parseToolCallsToCodeContext(validToolCalls)

		// Verify no errors using the helper
		feedbacks, hasUnmarshalError, fatalErr = checkToolCallUnmarshalErrors(validResults)
		assert.Nil(t, fatalErr)
		assert.False(t, hasUnmarshalError)
		assert.Empty(t, feedbacks)

		// Merge all requests (as codeContextLoop does when all are valid)
		merged := mergeToolCallRequests(validResults)

		// Verify all requests are merged in order
		require.Len(t, merged.Requests, 3)
		assert.Equal(t, "a.go", merged.Requests[0].FilePath)
		assert.Equal(t, "b.go", merged.Requests[1].FilePath)
		assert.Equal(t, "c.go", merged.Requests[2].FilePath)
		assert.Equal(t, "First\nSecond\nThird", merged.Analysis)
	})
}

func TestCheckToolCallUnmarshalErrors(t *testing.T) {
	t.Parallel()

	t.Run("no errors returns empty feedbacks", func(t *testing.T) {
		t.Parallel()
		results := []ToolCallWithCodeContext{
			{
				ToolCall: llm.ToolCall{Id: "call_1", Name: "get_symbol_definitions"},
				RequiredCodeContext: RequiredCodeContext{
					Requests: []coding.FileSymDefRequest{{FilePath: "a.go"}},
				},
			},
			{
				ToolCall: llm.ToolCall{Id: "call_2", Name: "get_symbol_definitions"},
				RequiredCodeContext: RequiredCodeContext{
					Requests: []coding.FileSymDefRequest{{FilePath: "b.go"}},
				},
			},
		}

		feedbacks, hasUnmarshalError, fatalErr := checkToolCallUnmarshalErrors(results)

		assert.Nil(t, fatalErr)
		assert.False(t, hasUnmarshalError)
		assert.Empty(t, feedbacks)
	})

	t.Run("unmarshal error generates feedback for that tool call", func(t *testing.T) {
		t.Parallel()
		results := []ToolCallWithCodeContext{
			{
				ToolCall: llm.ToolCall{Id: "call_1", Name: "get_symbol_definitions"},
				RequiredCodeContext: RequiredCodeContext{
					Requests: []coding.FileSymDefRequest{{FilePath: "a.go"}},
				},
			},
			{
				ToolCall: llm.ToolCall{Id: "call_2_bad", Name: "get_symbol_definitions"},
				Err:      fmt.Errorf("%w: invalid json", llm.ErrToolCallUnmarshal),
			},
		}

		feedbacks, hasUnmarshalError, fatalErr := checkToolCallUnmarshalErrors(results)

		assert.Nil(t, fatalErr)
		assert.True(t, hasUnmarshalError)
		require.Len(t, feedbacks, 1)
		assert.Equal(t, "call_2_bad", feedbacks[0].ToolCallId)
		assert.Equal(t, "get_symbol_definitions", feedbacks[0].FunctionName)
		assert.Contains(t, feedbacks[0].TextResponse(), "failed to unmarshal json")
		assert.Contains(t, feedbacks[0].TextResponse(), "Hint:")
	})

	t.Run("multiple unmarshal errors generate multiple feedbacks", func(t *testing.T) {
		t.Parallel()
		results := []ToolCallWithCodeContext{
			{
				ToolCall: llm.ToolCall{Id: "call_1_bad", Name: "get_symbol_definitions"},
				Err:      fmt.Errorf("%w: first error", llm.ErrToolCallUnmarshal),
			},
			{
				ToolCall: llm.ToolCall{Id: "call_2", Name: "get_symbol_definitions"},
				RequiredCodeContext: RequiredCodeContext{
					Requests: []coding.FileSymDefRequest{{FilePath: "a.go"}},
				},
			},
			{
				ToolCall: llm.ToolCall{Id: "call_3_bad", Name: "get_symbol_definitions"},
				Err:      fmt.Errorf("%w: second error", llm.ErrToolCallUnmarshal),
			},
		}

		feedbacks, hasUnmarshalError, fatalErr := checkToolCallUnmarshalErrors(results)

		assert.Nil(t, fatalErr)
		assert.True(t, hasUnmarshalError)
		require.Len(t, feedbacks, 2)
		assert.Equal(t, "call_1_bad", feedbacks[0].ToolCallId)
		assert.Equal(t, "call_3_bad", feedbacks[1].ToolCallId)
	})

	t.Run("non-unmarshal error returns fatal error", func(t *testing.T) {
		t.Parallel()
		results := []ToolCallWithCodeContext{
			{
				ToolCall: llm.ToolCall{Id: "call_1", Name: "get_symbol_definitions"},
				RequiredCodeContext: RequiredCodeContext{
					Requests: []coding.FileSymDefRequest{{FilePath: "a.go"}},
				},
			},
			{
				ToolCall: llm.ToolCall{Id: "call_2", Name: "get_symbol_definitions"},
				Err:      fmt.Errorf("some other error"),
			},
		}

		feedbacks, hasUnmarshalError, fatalErr := checkToolCallUnmarshalErrors(results)

		assert.NotNil(t, fatalErr)
		assert.Contains(t, fatalErr.Error(), "some other error")
		assert.False(t, hasUnmarshalError)
		assert.Nil(t, feedbacks)
	})
}
