package dev

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sidekick/env"
	"sidekick/flow_action"
	"sidekick/llm"
	"sidekick/llm2"
	"sidekick/persisted_ai"
	"sidekick/srv/sqlite"
	"sidekick/utils"
	"strings"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

func TestReadFileActivity(t *testing.T) {
	tempDir := t.TempDir()

	tests := []struct {
		name             string
		fileContent      string
		relativeFilePath string
		params           ReadFileActivityInput
		want             string
		wantErr          bool
	}{
		{
			name:        "LineNumber less than 1",
			fileContent: "line1\nline2\nline3",
			params:      ReadFileActivityInput{LineNumber: 0, WindowSize: 1},
			wantErr:     true,
		},
		{
			name:        "WindowSize less than 0",
			fileContent: "line1\nline2\nline3",
			params:      ReadFileActivityInput{LineNumber: 1, WindowSize: -1},
			wantErr:     true,
		},
		{
			name:        "Line number exceeds total number of lines",
			fileContent: "line1\nline2\nline3",
			params:      ReadFileActivityInput{LineNumber: 4, WindowSize: 0},
			wantErr:     true,
		},
		{
			name:        "lines found in window but not on the line",
			fileContent: "line1\nline2\nline3",
			params:      ReadFileActivityInput{LineNumber: 4, WindowSize: 2},
			want:        "File: placeholder\nLines: 2-3\n```\nline2\nline3\n```",
		},
		{
			name:        "Successful read",
			fileContent: "line1\nline2\nline3\nline4\nline5",
			params:      ReadFileActivityInput{LineNumber: 3, WindowSize: 1},
			want:        "File: placeholder\nLines: 2-4\n```\nline2\nline3\nline4\n```",
		},
		{
			name:        "Successful read - first line",
			fileContent: "line1\nline2\nline3\nline4\nline5",
			params:      ReadFileActivityInput{LineNumber: 1, WindowSize: 1},
			want:        "File: placeholder\nLines: 1-2\n```\nline1\nline2\n```",
		},
		{
			name:        "Successful read - last line",
			fileContent: "line1\nline2\nline3\nline4\nline5",
			params:      ReadFileActivityInput{LineNumber: 5, WindowSize: 1},
			want:        "File: placeholder\nLines: 4-5\n```\nline4\nline5\n```",
		},
		{
			name:        "Successful read - no window size",
			fileContent: "line1\nline2\nline3\nline4\nline5",
			params:      ReadFileActivityInput{LineNumber: 3, WindowSize: 0},
			want:        "File: placeholder\nLines: 3-3\n```\nline3\n```",
		},
		{
			name:        "Successful read - larger window size",
			fileContent: "line1\nline2\nline3\nline4\nline5\nline6",
			params:      ReadFileActivityInput{LineNumber: 3, WindowSize: 2},
			want:        "File: placeholder\nLines: 1-5\n```\nline1\nline2\nline3\nline4\nline5\n```",
		},
		{
			name:        "Successful read - oversized window size",
			fileContent: "line1\nline2\nline3\nline4\nline5",
			params:      ReadFileActivityInput{LineNumber: 3, WindowSize: 100},
			want:        "File: placeholder\nLines: 1-5\n```\nline1\nline2\nline3\nline4\nline5\n```",
		},
		{
			name:             "Read file from subdirectory",
			fileContent:      "line1\nline2\nline3",
			relativeFilePath: "subdir/nested.txt",
			params:           ReadFileActivityInput{LineNumber: 2, WindowSize: 1},
			want:             "File: subdir/nested.txt\nLines: 1-3\n```\nline1\nline2\nline3\n```",
		},
	}

	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			relativeFilePath := tt.relativeFilePath
			if relativeFilePath == "" {
				relativeFilePath = fmt.Sprintf("test%d.txt", i)
			}
			tt.params.FilePath = relativeFilePath
			absoluteFilePath := filepath.Join(tempDir, relativeFilePath)
			err := os.MkdirAll(filepath.Dir(absoluteFilePath), 0755)
			require.NoError(t, err)

			err = os.WriteFile(absoluteFilePath, []byte(tt.fileContent), 0644)
			require.NoError(t, err)

			got, err := ReadFileActivity(tempDir, tt.params)
			if (err != nil) != tt.wantErr {
				t.Errorf("ReadFile() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			tt.want = strings.ReplaceAll(tt.want, "placeholder", tt.params.FilePath)
			if got != tt.want {
				t.Errorf("ReadFile() = %v, want %v", got, tt.want)
				t.Errorf("\n Got:\n%s\nWant: \n%s", utils.PanicJSON(got), utils.PanicJSON(tt.want))
			}
		})
	}
}

func TestReadFileActivity_PathTraversal(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()

	tests := []struct {
		name     string
		filePath string
	}{
		{
			name:     "parent directory prefix",
			filePath: "../etc/passwd",
		},
		{
			name:     "parent directory in middle",
			filePath: "subdir/../../etc/passwd",
		},
		{
			name:     "bare parent directory",
			filePath: "..",
		},
		{
			name:     "absolute path",
			filePath: "/etc/passwd",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			params := ReadFileActivityInput{
				FilePath:   tt.filePath,
				LineNumber: 1,
				WindowSize: 1,
			}
			_, err := ReadFileActivity(tempDir, params)
			require.Error(t, err)
			require.Contains(t, err.Error(), "file path")
		})
	}
}

func TestReadFileActivity_PathTraversal_AllowAnyPath(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()

	// Create a file in a subdirectory to traverse into
	subDir := filepath.Join(tempDir, "sub")
	require.NoError(t, os.MkdirAll(subDir, 0755))
	targetFile := filepath.Join(subDir, "target.txt")
	require.NoError(t, os.WriteFile(targetFile, []byte("line1\nline2\nline3\n"), 0644))

	// With AllowAnyPath, relative paths with ".." should succeed if the file exists
	params := ReadFileActivityInput{
		FilePath:     "sub/../sub/target.txt",
		LineNumber:   1,
		WindowSize:   1,
		AllowAnyPath: true,
	}
	result, err := ReadFileActivity(tempDir, params)
	require.NoError(t, err)
	require.Contains(t, result, "line1")

	// Absolute paths should also work with AllowAnyPath
	params = ReadFileActivityInput{
		FilePath:     targetFile,
		LineNumber:   1,
		WindowSize:   1,
		AllowAnyPath: true,
	}
	result, err = ReadFileActivity(tempDir, params)
	require.NoError(t, err)
	require.Contains(t, result, "line1")
}

func TestReadFileActivity_NonExistentFile(t *testing.T) {
	tempDir := t.TempDir()

	params := ReadFileActivityInput{
		FilePath:   "nonexistent.txt",
		LineNumber: 1,
		WindowSize: 1,
	}

	_, err := ReadFileActivity(tempDir, params)
	if err == nil || !strings.Contains(err.Error(), "no file exists at the given file path: nonexistent.txt") {
		t.Errorf("Expected error for non-existent file, got %v", err)
	}
}

func TestBulkReadFileActivity(t *testing.T) {
	// Create a temporary directory and test files
	tempDir := t.TempDir()

	// Create test file with known content
	testFile1 := filepath.Join(tempDir, "test1.go")
	content1 := `package main

import "fmt"

func main() {
	fmt.Println("Hello, World!")
	fmt.Println("Line 7")
	fmt.Println("Line 8")
	fmt.Println("Line 9")
	fmt.Println("Line 10")
}
`
	err := os.WriteFile(testFile1, []byte(content1), 0644)
	require.NoError(t, err)

	testFile2 := filepath.Join(tempDir, "test2.py")
	content2 := `def hello():
    print("Hello from Python")
    return True

def goodbye():
    print("Goodbye")
`
	err = os.WriteFile(testFile2, []byte(content2), 0644)
	require.NoError(t, err)

	type expectedBlock struct {
		filePath  string
		startLine int
		endLine   int
	}

	tests := []struct {
		name           string
		params         BulkReadFileParams
		expectedBlocks []expectedBlock
		expectedErrors int
		description    string
	}{
		{
			name: "overlapping windows merged",
			params: BulkReadFileParams{
				FileLines: []FileLine{
					{FilePath: "test1.go", LineNumber: 5},
					{FilePath: "test1.go", LineNumber: 6},
					{FilePath: "test1.go", LineNumber: 7},
				},
				WindowSize: 2,
			},
			expectedBlocks: []expectedBlock{
				{filePath: "test1.go", startLine: 3, endLine: 9},
			},
			expectedErrors: 0,
			description:    "Windows [3-7], [4-8], [5-9] should merge into one block [3-9]",
		},
		{
			name: "exactly adjacent windows merged",
			params: BulkReadFileParams{
				FileLines: []FileLine{
					{FilePath: "test1.go", LineNumber: 3},
					{FilePath: "test1.go", LineNumber: 6},
				},
				WindowSize: 1,
			},
			expectedBlocks: []expectedBlock{
				{filePath: "test1.go", startLine: 2, endLine: 7},
			},
			expectedErrors: 0,
			description:    "Windows [2-4] and [5-7] should merge since 5 <= 4+1",
		},
		{
			name: "non-contiguous windows not merged",
			params: BulkReadFileParams{
				FileLines: []FileLine{
					{FilePath: "test1.go", LineNumber: 2},
					{FilePath: "test1.go", LineNumber: 8},
				},
				WindowSize: 1,
			},
			expectedBlocks: []expectedBlock{
				{filePath: "test1.go", startLine: 1, endLine: 3},
				{filePath: "test1.go", startLine: 7, endLine: 9},
			},
			expectedErrors: 0,
			description:    "Windows [1-3] and [7-9] should remain separate",
		},
		{
			name: "duplicate requests deduplicated",
			params: BulkReadFileParams{
				FileLines: []FileLine{
					{FilePath: "test1.go", LineNumber: 5},
					{FilePath: "test1.go", LineNumber: 5},
					{FilePath: "test1.go", LineNumber: 5},
				},
				WindowSize: 1,
			},
			expectedBlocks: []expectedBlock{
				{filePath: "test1.go", startLine: 4, endLine: 6},
			},
			expectedErrors: 0,
			description:    "Duplicate requests should result in one merged block",
		},
		{
			name: "windows near file start clamped",
			params: BulkReadFileParams{
				FileLines: []FileLine{
					{FilePath: "test1.go", LineNumber: 1},
					{FilePath: "test1.go", LineNumber: 2},
				},
				WindowSize: 3,
			},
			expectedBlocks: []expectedBlock{
				{filePath: "test1.go", startLine: 1, endLine: 5},
			},
			expectedErrors: 0,
			description:    "Windows should be clamped to file boundaries",
		},
		{
			name: "windows near file end clamped",
			params: BulkReadFileParams{
				FileLines: []FileLine{
					{FilePath: "test1.go", LineNumber: 11},
					{FilePath: "test1.go", LineNumber: 12},
				},
				WindowSize: 2,
			},
			expectedBlocks: []expectedBlock{
				{filePath: "test1.go", startLine: 9, endLine: 11},
			},
			expectedErrors: 0,
			description:    "Windows should be clamped to file end",
		},
		{
			name: "multiple files ordered by first appearance",
			params: BulkReadFileParams{
				FileLines: []FileLine{
					{FilePath: "test2.py", LineNumber: 2},
					{FilePath: "test1.go", LineNumber: 5},
					{FilePath: "test2.py", LineNumber: 5},
				},
				WindowSize: 1,
			},
			expectedBlocks: []expectedBlock{
				{filePath: "test2.py", startLine: 1, endLine: 6},
				{filePath: "test1.go", startLine: 4, endLine: 6},
			},
			expectedErrors: 0,
			description:    "Files should appear in order of first appearance",
		},
		{
			name: "missing file error",
			params: BulkReadFileParams{
				FileLines: []FileLine{
					{FilePath: "nonexistent.txt", LineNumber: 1},
				},
				WindowSize: 1,
			},
			expectedBlocks: []expectedBlock{},
			expectedErrors: 1,
			description:    "Missing file should produce one error",
		},
		{
			name: "all windows out of range",
			params: BulkReadFileParams{
				FileLines: []FileLine{
					{FilePath: "test1.go", LineNumber: 100},
					{FilePath: "test1.go", LineNumber: 200},
				},
				WindowSize: 1,
			},
			expectedBlocks: []expectedBlock{},
			expectedErrors: 1,
			description:    "All windows out of range should produce one error",
		},
		{
			name: "invalid line number",
			params: BulkReadFileParams{
				FileLines: []FileLine{
					{FilePath: "test1.go", LineNumber: 0},
				},
				WindowSize: 1,
			},
			expectedBlocks: []expectedBlock{},
			expectedErrors: 0,
			description:    "Invalid line number should return error from activity",
		},
		{
			name: "negative window size",
			params: BulkReadFileParams{
				FileLines: []FileLine{
					{FilePath: "test1.go", LineNumber: 5},
				},
				WindowSize: -1,
			},
			expectedBlocks: []expectedBlock{},
			expectedErrors: 0,
			description:    "Negative window size should return error from activity",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := BulkReadFileActivity(tempDir, tt.params)

			// For invalid parameters, expect an error from the activity itself
			if tt.name == "invalid line number" || tt.name == "negative window size" {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			require.Equal(t, len(tt.expectedBlocks), len(result.CodeBlocks), "Expected %d blocks, got %d. %s", len(tt.expectedBlocks), len(result.CodeBlocks), tt.description)
			require.Equal(t, tt.expectedErrors, len(result.Errors), "Expected %d errors, got %d. %s", tt.expectedErrors, len(result.Errors), tt.description)

			// Assert exact file path, start line, and end line for each expected block
			for i, expectedBlock := range tt.expectedBlocks {
				require.Less(t, i, len(result.CodeBlocks), "Expected block %d not found in results", i)
				actualBlock := result.CodeBlocks[i]
				require.Equal(t, expectedBlock.filePath, actualBlock.FilePath, "Block %d: expected file path %s, got %s", i, expectedBlock.filePath, actualBlock.FilePath)
				require.Equal(t, expectedBlock.startLine, actualBlock.StartLine, "Block %d (%s): expected start line %d, got %d", i, expectedBlock.filePath, expectedBlock.startLine, actualBlock.StartLine)
				require.Equal(t, expectedBlock.endLine, actualBlock.EndLine, "Block %d (%s): expected end line %d, got %d", i, expectedBlock.filePath, expectedBlock.endLine, actualBlock.EndLine)
			}

			// Verify blocks are ordered correctly
			if len(result.CodeBlocks) > 1 {
				for i := 1; i < len(result.CodeBlocks); i++ {
					prev := result.CodeBlocks[i-1]
					curr := result.CodeBlocks[i]

					// If same file, should be ordered by start line
					if prev.FilePath == curr.FilePath {
						require.LessOrEqual(t, prev.StartLine, curr.StartLine, "Blocks within same file should be ordered by start line")
					}
				}
			}

			// Verify content is not empty for valid blocks
			for _, block := range result.CodeBlocks {
				require.NotEmpty(t, block.Content, "Block content should not be empty")
				require.Greater(t, block.EndLine, 0, "End line should be positive")
				require.GreaterOrEqual(t, block.EndLine, block.StartLine, "End line should be >= start line")
			}
		})
	}
}

func TestBulkReadFileV2Formatting(t *testing.T) {
	// Create a temporary directory and test files
	tempDir := t.TempDir()

	// Create test file 1
	testFile1 := filepath.Join(tempDir, "test.go")
	content1 := `package main

import "fmt"

func main() {
	fmt.Println("Hello")
}
`
	err := os.WriteFile(testFile1, []byte(content1), 0644)
	require.NoError(t, err)

	// Create test file 2
	testFile2 := filepath.Join(tempDir, "test.py")
	content2 := `def hello():
    print("Hello from Python")
    return True
`
	err = os.WriteFile(testFile2, []byte(content2), 0644)
	require.NoError(t, err)

	// Test with adjacent windows that should merge, plus a separate file
	params := BulkReadFileParams{
		FileLines: []FileLine{
			{FilePath: "test.go", LineNumber: 3}, // [2-4]
			{FilePath: "test.go", LineNumber: 5}, // [4-6] - will merge with above
			{FilePath: "test.py", LineNumber: 2}, // [1-3] - separate file
		},
		WindowSize: 1,
	}

	result, err := BulkReadFileActivity(tempDir, params)
	require.NoError(t, err)
	require.Equal(t, 2, len(result.CodeBlocks), "Should have exactly 2 blocks")
	require.Equal(t, 0, len(result.Errors), "Should have no errors")

	// Test the exact final formatted output using the helper function
	var formattedBlocks []string
	for _, block := range result.CodeBlocks {
		formattedBlocks = append(formattedBlocks, formatCodeBlock(block))
	}
	finalOutput := strings.Join(formattedBlocks, "\n\n")

	expectedOutput := `File: test.go
Lines: 2-6
` + "```" + `go

import "fmt"

func main() {
	fmt.Println("Hello")
` + "```" + `

File: test.py
Lines: 1-3
` + "```" + `python
def hello():
    print("Hello from Python")
    return True
` + "```"

	require.Equal(t, expectedOutput, finalOutput)
}

// BulkReadFileV2TestSuite tests the BulkReadFileV2 workflow helper,
// which dispatches to BulkReadFileActivities.BulkReadFileActivityV2.
type BulkReadFileV2TestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	env *testsuite.TestWorkflowEnvironment
}

func (s *BulkReadFileV2TestSuite) SetupTest() {
	s.env = s.NewTestWorkflowEnvironment()
	s.env.SetWorkerOptions(utils.TestWorkerOptions())
}

func (s *BulkReadFileV2TestSuite) TearDownTest() {
	s.env.AssertExpectations(s.T())
}

func (s *BulkReadFileV2TestSuite) TestBulkReadFileV2_ReturnsRef() {
	tempDir := s.T().TempDir()
	ec := env.EnvContainer{Env: &env.LocalEnv{WorkingDirectory: tempDir}}

	expectedRef := persisted_ai.MessageRef{
		BlockKeys: []string{"test-flow:msg:block1"},
		Role:      "user",
	}

	wrapperWorkflow := func(ctx workflow.Context) (*BulkReadFileActivityV2Output, error) {
		ctx = utils.NoRetryCtx(ctx)
		dCtx := DevContext{
			ExecContext: flow_action.ExecContext{
				Context:      ctx,
				EnvContainer: &ec,
				WorkspaceId:  "test-workspace",
			},
		}
		return BulkReadFileV2(dCtx, BulkReadFileParams{
			FileLines:  []FileLine{{FilePath: "test.go", LineNumber: 1}},
			WindowSize: 5,
		}, llm.ToolCall{Id: "call_123", Name: "read_file"})
	}
	s.env.RegisterWorkflow(wrapperWorkflow)

	// Mock the activity via nil struct pointer — identical to production usage
	var bra *BulkReadFileActivities
	s.env.OnActivity(bra.BulkReadFileActivityV2, mock.Anything, mock.Anything).Return(
		&BulkReadFileActivityV2Output{Ref: expectedRef}, nil,
	)

	s.env.ExecuteWorkflow(wrapperWorkflow)
	s.True(s.env.IsWorkflowCompleted())
	s.NoError(s.env.GetWorkflowError())

	var result BulkReadFileActivityV2Output
	s.NoError(s.env.GetWorkflowResult(&result))
	s.Equal(expectedRef, result.Ref)
}

func (s *BulkReadFileV2TestSuite) TestBulkReadFileV2_EmptyFileLines() {
	tempDir := s.T().TempDir()
	ec := env.EnvContainer{Env: &env.LocalEnv{WorkingDirectory: tempDir}}

	wrapperWorkflow := func(ctx workflow.Context) (*BulkReadFileActivityV2Output, error) {
		dCtx := DevContext{
			ExecContext: flow_action.ExecContext{
				Context:      ctx,
				EnvContainer: &ec,
				WorkspaceId:  "test-workspace",
			},
		}
		return BulkReadFileV2(dCtx, BulkReadFileParams{
			FileLines:  []FileLine{},
			WindowSize: 5,
		}, llm.ToolCall{Id: "call_123", Name: "read_file"})
	}
	s.env.RegisterWorkflow(wrapperWorkflow)

	s.env.ExecuteWorkflow(wrapperWorkflow)
	s.True(s.env.IsWorkflowCompleted())
	s.Error(s.env.GetWorkflowError())
}

func TestBulkReadFileV2(t *testing.T) {
	suite.Run(t, new(BulkReadFileV2TestSuite))
}

func TestReadFileActivity_OutOfRangeIncludesValidRange(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()

	tests := []struct {
		name         string
		fileContent  string
		params       ReadFileActivityInput
		wantContains string
	}{
		{
			name:         "line number exceeds file length",
			fileContent:  "line1\nline2\nline3",
			params:       ReadFileActivityInput{LineNumber: 10, WindowSize: 0},
			wantContains: "file has 3 lines, valid line numbers are 1-3",
		},
		{
			name:         "line number exceeds file length with window",
			fileContent:  "line1\nline2\nline3\nline4\nline5",
			params:       ReadFileActivityInput{LineNumber: 20, WindowSize: 2},
			wantContains: "file has 5 lines, valid line numbers are 1-5",
		},
		{
			name:         "empty file",
			fileContent:  "",
			params:       ReadFileActivityInput{LineNumber: 1, WindowSize: 0},
			wantContains: "file is empty",
		},
	}

	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			relativeFilePath := fmt.Sprintf("range_test%d.txt", i)
			tt.params.FilePath = relativeFilePath
			absoluteFilePath := filepath.Join(tempDir, relativeFilePath)
			err := os.WriteFile(absoluteFilePath, []byte(tt.fileContent), 0644)
			require.NoError(t, err)

			_, err = ReadFileActivity(tempDir, tt.params)
			require.Error(t, err)
			require.Contains(t, err.Error(), tt.wantContains)
		})
	}
}

func TestBulkReadFileActivity_OutOfRangeIncludesValidRange(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()

	smallFile := filepath.Join(tempDir, "small.go")
	require.NoError(t, os.WriteFile(smallFile, []byte("line1\nline2\nline3"), 0644))

	emptyFile := filepath.Join(tempDir, "empty.go")
	require.NoError(t, os.WriteFile(emptyFile, []byte(""), 0644))

	tests := []struct {
		name         string
		params       BulkReadFileParams
		wantContains string
	}{
		{
			name: "line number exceeds file length",
			params: BulkReadFileParams{
				FileLines:  []FileLine{{FilePath: "small.go", LineNumber: 100}},
				WindowSize: 1,
			},
			wantContains: "file has 3 lines, valid line numbers are 1-3",
		},
		{
			name: "all windows out of range",
			params: BulkReadFileParams{
				FileLines: []FileLine{
					{FilePath: "small.go", LineNumber: 50},
					{FilePath: "small.go", LineNumber: 60},
				},
				WindowSize: 1,
			},
			wantContains: "file has 3 lines, valid line numbers are 1-3",
		},
		{
			name: "empty file",
			params: BulkReadFileParams{
				FileLines:  []FileLine{{FilePath: "empty.go", LineNumber: 1}},
				WindowSize: 1,
			},
			wantContains: "file is empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result, err := BulkReadFileActivity(tempDir, tt.params)
			require.NoError(t, err)
			require.Empty(t, result.CodeBlocks)
			require.Len(t, result.Errors, 1)
			require.Contains(t, result.Errors[0], tt.wantContains)
		})
	}
}

func TestBulkReadFileActivityV2_OutOfRangeIncludesValidRange(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()

	smallFile := filepath.Join(tempDir, "small.go")
	require.NoError(t, os.WriteFile(smallFile, []byte("line1\nline2\nline3"), 0644))

	emptyFile := filepath.Join(tempDir, "empty.go")
	require.NoError(t, os.WriteFile(emptyFile, []byte(""), 0644))

	tests := []struct {
		name         string
		params       BulkReadFileParams
		wantContains string
	}{
		{
			name: "line number exceeds file length",
			params: BulkReadFileParams{
				FileLines:  []FileLine{{FilePath: "small.go", LineNumber: 100}},
				WindowSize: 1,
			},
			wantContains: "file has 3 lines, valid line numbers are 1-3",
		},
		{
			name: "empty file",
			params: BulkReadFileParams{
				FileLines:  []FileLine{{FilePath: "empty.go", LineNumber: 1}},
				WindowSize: 1,
			},
			wantContains: "file is empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			storage := sqlite.NewTestSqliteStorage(t, "bulk_read_v2_range_test")
			activities := &BulkReadFileActivities{Storage: storage}

			ec := env.EnvContainer{Env: &env.LocalEnv{WorkingDirectory: tempDir}}
			input := BulkReadFileActivityV2Input{
				EnvContainer: ec,
				Params:       tt.params,
				FlowId:       "test-flow",
				WorkspaceId:  "test-workspace",
				ToolCall:     llm.ToolCall{Id: "call_1", Name: "read_file"},
			}

			out, err := activities.BulkReadFileActivityV2(context.Background(), input)
			require.NoError(t, err)
			require.NotNil(t, out)
			require.Len(t, out.Ref.BlockKeys, 1)

			storageKey := persisted_ai.StorageKey(input.FlowId, out.Ref.BlockKeys[0])
			values, err := storage.MGet(context.Background(), input.WorkspaceId, []string{storageKey})
			require.NoError(t, err)
			require.Len(t, values, 1)
			require.NotNil(t, values[0])

			var block llm2.ContentBlock
			require.NoError(t, json.Unmarshal(values[0], &block))
			require.NotNil(t, block.ToolResult)

			var combined strings.Builder
			for _, inner := range block.ToolResult.Content {
				combined.WriteString(inner.Text)
			}
			require.Contains(t, combined.String(), tt.wantContains)
		})
	}
}

func TestParseBulkReadFileParamsLenient(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		want    BulkReadFileParams
		wantErr bool
	}{
		{
			name:  "canonical form with numeric fields",
			input: `{"file_lines":[{"file_path":"a.go","line_number":3}],"window_size":5}`,
			want: BulkReadFileParams{
				FileLines:  []FileLine{{FilePath: "a.go", LineNumber: 3}},
				WindowSize: 5,
			},
		},
		{
			name:  "canonical form with string numbers",
			input: `{"file_lines":[{"file_path":"a.go","line_number":"3"}],"window_size":"5"}`,
			want: BulkReadFileParams{
				FileLines:  []FileLine{{FilePath: "a.go", LineNumber: 3}},
				WindowSize: 5,
			},
		},
		{
			name:  "flat single-file form with string line number",
			input: `{"file_path":"dev/git_merge_integration_test.go","line_number":"1","window_size":130}`,
			want: BulkReadFileParams{
				FileLines:  []FileLine{{FilePath: "dev/git_merge_integration_test.go", LineNumber: 1}},
				WindowSize: 130,
			},
		},
		{
			name:  "flat single-file form with numeric line number",
			input: `{"file_path":"dev/git_merge_integration_test.go","line_number":1,"window_size":"130"}`,
			want: BulkReadFileParams{
				FileLines:  []FileLine{{FilePath: "dev/git_merge_integration_test.go", LineNumber: 1}},
				WindowSize: 130,
			},
		},
		{
			name:    "invalid string line number",
			input:   `{"file_path":"a.go","line_number":"abc"}`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := parseBulkReadFileParamsLenient(tt.input)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}
