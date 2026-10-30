package dev

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sidekick/domain"
	"sidekick/env"
	"sidekick/flow_action"
	"sidekick/llm"
	"sidekick/llm2"
	"sidekick/persisted_ai"
	"sidekick/srv/sqlite"
	"sidekick/temporalmeta"
	"sidekick/utils"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

// TestHandleToolCalls_NullStringArgsNoteReachesChatHistory runs handleToolCalls
// end to end (sequential and parallel versions) and hydrates the resulting
// llm2 chat history from storage, asserting the "null" string hint is visible
// to the model for both a ref-backed success and an inline error result.
func TestHandleToolCalls_NullStringArgsNoteReachesChatHistory(t *testing.T) {
	t.Parallel()

	for _, parallelVersion := range []workflow.Version{workflow.DefaultVersion, 1} {
		parallelVersion := parallelVersion
		name := "sequential"
		if parallelVersion == 1 {
			name = "parallel"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			repoDir := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(repoDir, "small.go"), []byte("line1\nline2\nline3"), 0644))
			storage := sqlite.NewTestSqliteStorage(t, "handle_tool_calls_null_note")
			const workspaceId = "ws-null-note"

			var suite testsuite.WorkflowTestSuite
			testEnv := suite.NewTestWorkflowEnvironment()
			testEnv.OnGetVersion("handle-parallel-tool-calls", workflow.DefaultVersion, 1).Return(parallelVersion)
			testEnv.RegisterActivity(&BulkReadFileActivities{Storage: storage})
			testEnv.RegisterActivity(&persisted_ai.ChatHistoryActivities{Storage: storage})
			var fa *flow_action.FlowActivities
			testEnv.OnActivity(fa.PersistFlowAction, mock.Anything, mock.Anything).Return(nil).Maybe()
			var meta *temporalmeta.TemporalMetaActivities
			testEnv.OnActivity(meta.FetchFlowActionActivities, mock.Anything, mock.Anything).Return([]domain.TemporalActivityRef{}, nil).Maybe()

			readFileCall := llm.ToolCall{
				Id:        "call-read",
				Name:      bulkReadFileTool.Name,
				Arguments: `{"file_lines":[{"file_path":"small.go","line_number":2}],"window_size":1,"line_range":"null"}`,
			}
			customCall := llm.ToolCall{
				Id:        "call-custom",
				Name:      "custom_tool",
				Arguments: `{"target":"null"}`,
			}
			customHandlers := map[string]func(DevContext, llm.ToolCall) (llm2.ToolResultBlock, error){
				customCall.Name: func(DevContext, llm.ToolCall) (llm2.ToolResultBlock, error) {
					return llm2.ToolResultBlock{}, errors.New("custom tool failed")
				},
			}

			type result struct {
				FlowId  string
				History json.RawMessage
			}
			wf := func(ctx workflow.Context) (result, error) {
				ctx = utils.NoRetryCtx(ctx)
				flowId := workflow.GetInfo(ctx).WorkflowExecution.ID
				dCtx := DevContext{ExecContext: flow_action.ExecContext{
					Context:      ctx,
					GlobalState:  &flow_action.GlobalState{},
					FlowScope:    &flow_action.FlowScope{SubflowName: "null-note"},
					EnvContainer: &env.EnvContainer{Env: &env.LocalEnv{WorkingDirectory: repoDir}},
					WorkspaceId:  workspaceId,
				}}
				history := &persisted_ai.ChatHistoryContainer{
					History: persisted_ai.NewLlm2ChatHistory(flowId, workspaceId),
				}
				// The legacy sequential version only handles the first tool
				// call per invocation.
				batches := [][]llm.ToolCall{{readFileCall, customCall}}
				if parallelVersion == workflow.DefaultVersion {
					batches = [][]llm.ToolCall{{readFileCall}, {customCall}}
				}
				for _, batch := range batches {
					if _, err := handleToolCalls(dCtx, batch, history, customHandlers); err != nil {
						return result{}, err
					}
				}
				raw, err := json.Marshal(history)
				if err != nil {
					return result{}, err
				}
				return result{FlowId: flowId, History: raw}, nil
			}
			testEnv.RegisterWorkflow(wf)
			testEnv.ExecuteWorkflow(wf)
			require.NoError(t, testEnv.GetWorkflowError())
			var res result
			require.NoError(t, testEnv.GetWorkflowResult(&res))

			var history persisted_ai.ChatHistoryContainer
			require.NoError(t, json.Unmarshal(res.History, &history))
			llm2History, ok := history.History.(*persisted_ai.Llm2ChatHistory)
			require.True(t, ok)
			llm2History.SetFlowId(res.FlowId)
			llm2History.SetWorkspaceId(workspaceId)
			require.NoError(t, history.Hydrate(context.Background(), storage))

			messages := history.Llm2Messages()
			require.Len(t, messages, 2)
			resultsByCallId := map[string]*llm2.ToolResultBlock{}
			for _, msg := range messages {
				require.Len(t, msg.Content, 1)
				require.NotNil(t, msg.Content[0].ToolResult)
				resultsByCallId[msg.Content[0].ToolResult.ToolCallId] = msg.Content[0].ToolResult
			}

			readResult := resultsByCallId[readFileCall.Id]
			require.NotNil(t, readResult)
			assert.False(t, readResult.IsError)
			readText := readResult.TextContent()
			assert.Contains(t, readText, "line2")
			assert.Contains(t, readText, `"null"`)
			assert.Contains(t, readText, "line_range")
			assert.Less(t, strings.Index(readText, "line2"), strings.Index(readText, "line_range"))

			customResult := resultsByCallId[customCall.Id]
			require.NotNil(t, customResult)
			assert.True(t, customResult.IsError)
			customText := customResult.TextContent()
			assert.Contains(t, customText, "custom tool failed")
			assert.Contains(t, customText, `"null"`)
			assert.Contains(t, customText, "target")
		})
	}
}

func TestNullStringArgPaths(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		arguments string
		expected  []string
	}{
		{
			name:      "no null strings",
			arguments: `{"file_path": "main.go", "line_number": 10}`,
			expected:  nil,
		},
		{
			name:      "json null is not flagged",
			arguments: `{"file_path": null}`,
			expected:  nil,
		},
		{
			name:      "string containing null as substring is not flagged",
			arguments: `{"search_term": "nullable"}`,
			expected:  nil,
		},
		{
			name:      "top-level null string",
			arguments: `{"file_path": "null"}`,
			expected:  []string{"file_path"},
		},
		{
			name:      "nested null strings in objects and arrays, sorted by key",
			arguments: `{"requests": [{"file_path": "a.go", "symbols": [{"name": "Foo"}, {"name": "null"}]}, {"file_path": "null", "symbols": []}], "analysis": "null"}`,
			expected:  []string{"analysis", "requests[0].symbols[1].name", "requests[1].file_path"},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.expected, nullStringArgPaths(tt.arguments))
		})
	}
}

func TestAppendNullStringArgsNote(t *testing.T) {
	t.Parallel()

	t.Run("unchanged when no null string arguments", func(t *testing.T) {
		t.Parallel()
		tc := llm.ToolCall{Id: "call_1", Name: "read_file_lines", Arguments: `{"file_path": "main.go"}`}
		result := llm2.ToolResultBlock{ToolCallId: "call_1", Content: llm2.TextContentBlocks("file contents")}
		got := appendNullStringArgsNote(tc, result)
		assert.Equal(t, result, got)
	})

	t.Run("appends note to successful result text", func(t *testing.T) {
		t.Parallel()
		tc := llm.ToolCall{Id: "call_1", Name: "read_file_lines", Arguments: `{"file_path": "null"}`}
		result := llm2.ToolResultBlock{ToolCallId: "call_1", Content: llm2.TextContentBlocks("file contents")}
		got := appendNullStringArgsNote(tc, result)
		require.Len(t, got.Content, 1)
		assert.True(t, strings.HasPrefix(got.Content[0].Text, "file contents"))
		assert.Contains(t, got.Content[0].Text, `"null"`)
		assert.Contains(t, got.Content[0].Text, "file_path")
		assert.False(t, got.IsError)
	})

	t.Run("appends note to error result text", func(t *testing.T) {
		t.Parallel()
		tc := llm.ToolCall{Id: "call_1", Name: "read_file_lines", Arguments: `{"file_path": "null"}`}
		result := llm2.ToolResultBlock{ToolCallId: "call_1", IsError: true, Content: llm2.TextContentBlocks("file not found: null")}
		got := appendNullStringArgsNote(tc, result)
		require.Len(t, got.Content, 1)
		assert.True(t, strings.HasPrefix(got.Content[0].Text, "file not found: null"))
		assert.Contains(t, got.Content[0].Text, "file_path")
		assert.True(t, got.IsError)
	})

	t.Run("adds text block when result has no text content", func(t *testing.T) {
		t.Parallel()
		tc := llm.ToolCall{Id: "call_1", Name: "read_image", Arguments: `{"file_path": "null"}`}
		result := llm2.ToolResultBlock{ToolCallId: "call_1", Content: []llm2.ContentBlock{{Type: llm2.ContentBlockTypeImage, Image: &llm2.ImageRef{Url: "x"}}}}
		got := appendNullStringArgsNote(tc, result)
		require.Len(t, got.Content, 2)
		assert.Equal(t, llm2.ContentBlockTypeImage, got.Content[0].Type)
		assert.Equal(t, llm2.ContentBlockTypeText, got.Content[1].Type)
		assert.Contains(t, got.Content[1].Text, "file_path")
	})
}

func TestRemoveWorkingDirFromPaths(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		workingDir string
		expected   string
	}{
		{
			name:       "simple path replacement",
			input:      "/home/user/project/src/main.go",
			workingDir: "/home/user/project",
			expected:   "src/main.go",
		},
		{
			name:       "multiple paths in string",
			input:      "Error in /home/user/project/src/main.go and /home/user/project/src/util.go",
			workingDir: "/home/user/project",
			expected:   "Error in src/main.go and src/util.go",
		},
		{
			name:       "working dir alone - no replacement",
			input:      "The directory is /home/user/project and it exists",
			workingDir: "/home/user/project",
			expected:   "The directory is /home/user/project and it exists",
		},
		{
			name:       "working dir with trailing slash and space - no replacement",
			input:      "The directory is /home/user/project/ and it exists",
			workingDir: "/home/user/project",
			expected:   "The directory is /home/user/project/ and it exists",
		},
		{
			name:       "working dir followed by double quote - no replacement",
			input:      `path is "/home/user/project/"`,
			workingDir: "/home/user/project",
			expected:   `path is "/home/user/project/"`,
		},
		{
			name:       "working dir followed by single quote - no replacement",
			input:      `path is '/home/user/project/'`,
			workingDir: "/home/user/project",
			expected:   `path is '/home/user/project/'`,
		},
		{
			name:       "path inside quotes should be replaced",
			input:      `Error: file "/home/user/project/src/main.go" not found`,
			workingDir: "/home/user/project",
			expected:   `Error: file "src/main.go" not found`,
		},
		{
			name:       "empty working dir",
			input:      "/home/user/project/src/main.go",
			workingDir: "",
			expected:   "/home/user/project/src/main.go",
		},
		{
			name:       "working dir with trailing slash in input",
			input:      "/home/user/project/src/main.go",
			workingDir: "/home/user/project/",
			expected:   "src/main.go",
		},
		{
			name:       "no match",
			input:      "/other/path/file.go",
			workingDir: "/home/user/project",
			expected:   "/other/path/file.go",
		},
		{
			name:       "path at end of string",
			input:      "File: /home/user/project/main.go",
			workingDir: "/home/user/project",
			expected:   "File: main.go",
		},
		{
			name:       "nested path",
			input:      "/home/user/project/src/pkg/deep/file.go",
			workingDir: "/home/user/project",
			expected:   "src/pkg/deep/file.go",
		},
		{
			name:       "path with special regex chars in working dir",
			input:      "/home/user/project.name/src/main.go",
			workingDir: "/home/user/project.name",
			expected:   "src/main.go",
		},
		{
			name:       "multiline with paths",
			input:      "Error at /home/user/project/a.go:10\nWarning at /home/user/project/b.go:20",
			workingDir: "/home/user/project",
			expected:   "Error at a.go:10\nWarning at b.go:20",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := removeWorkingDirFromPaths(tt.input, tt.workingDir)
			if result != tt.expected {
				t.Errorf("removeWorkingDirFromPaths(%q, %q) = %q, want %q",
					tt.input, tt.workingDir, result, tt.expected)
			}
		})
	}
}
