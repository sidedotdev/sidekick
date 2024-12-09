package dev

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sidekick/coding/tree_sitter"
	"sidekick/env"
	"sidekick/fflag"
	"sidekick/llm"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateEditBlocksEmptyChatHistory(t *testing.T) {
	// Create an empty chat history
	chatHistory := []llm.ChatMessage{}

	// Create a slice of EditBlocks with various scenarios
	editBlocks := []EditBlock{
		{
			OldLines:          []string{"func oldFunction() {"},
			NewLines:          []string{"func newFunction() {"},
			VisibleCodeBlocks: extractAllCodeBlocks(chatHistory),
		},
		{
			OldLines:          []string{},
			NewLines:          []string{"package main"},
			VisibleCodeBlocks: extractAllCodeBlocks(chatHistory),
		},
		{
			OldLines:          []string{"type OldStruct struct {"},
			NewLines:          []string{"type NewStruct struct {"},
			VisibleCodeBlocks: extractAllCodeBlocks(chatHistory),
		},
	}

	// Call validateEditBlocks
	validEditBlocks, invalidReports := validateEditBlocks(editBlocks)

	assert.Equal(t, 1, len(validEditBlocks), "Expected edit block with empty old lines to be valid")
	assert.Equal(t, len(editBlocks)-1, len(invalidReports), "Expected all edit blocks with old lines to be invalid")
}

func TestValidateEditBlocksWithValidBlocks(t *testing.T) {
	// Create a chat history that includes the old lines from our edit blocks
	chatHistory := []llm.ChatMessage{
		{
			Role:    llm.ChatMessageRoleTool,
			Content: "Here's some code:\n```go\nfunc oldFunction() {\n    // Some code\n}\n```",
		},
		{
			Role:    llm.ChatMessageRoleTool,
			Content: "And here's another struct:\n```go\ntype OldStruct struct {\n    // Some fields\n}\n```",
		},
	}

	// Create a slice of EditBlocks with various scenarios
	editBlocks := []EditBlock{
		{
			OldLines:          []string{"func oldFunction() {"},
			NewLines:          []string{"func newFunction() {"},
			VisibleCodeBlocks: extractAllCodeBlocks(chatHistory),
		},
		{
			OldLines:          []string{},
			NewLines:          []string{"package main"},
			VisibleCodeBlocks: extractAllCodeBlocks(chatHistory),
		},
		{
			OldLines:          []string{"type OldStruct struct {"},
			NewLines:          []string{"type NewStruct struct {"},
			VisibleCodeBlocks: extractAllCodeBlocks(chatHistory),
		},
	}

	// Call validateEditBlocks
	validEditBlocks, invalidReports := validateEditBlocks(editBlocks)
	fmt.Printf("invalidReports: %v\n", invalidReports)

	// Assert that the correct edit blocks are marked as valid
	assert.Equal(t, 3, len(validEditBlocks), "Expected all valid edit blocks")
	assert.Equal(t, 0, len(invalidReports), "Expected no invalid edit blocks")
}

func TestValidateEditBlocksWithInvalidBlocks(t *testing.T) {
	// Create a chat history that doesn't include the old lines from our edit blocks
	chatHistory := []llm.ChatMessage{
		{
			Content: "Here's some unrelated code:\n```go\nfunc someOtherFunction() {\n    // Some code\n}\n```",
		},
		{
			Content: "And here's another unrelated struct:\n```go\ntype SomeOtherStruct struct {\n    // Some fields\n}\n```",
		},
	}

	// Create a slice of EditBlocks with old lines not present in chat history
	editBlocks := []EditBlock{
		{
			OldLines:          []string{"func nonExistentFunction() {"},
			NewLines:          []string{"func newFunction() {"},
			VisibleCodeBlocks: extractAllCodeBlocks(chatHistory),
		},
		{
			OldLines:          []string{"type NonExistentStruct struct {"},
			NewLines:          []string{"type NewStruct struct {"},
			VisibleCodeBlocks: extractAllCodeBlocks(chatHistory),
		},
	}

	// Call validateEditBlocks
	validEditBlocks, invalidReports := validateEditBlocks(editBlocks)

	// Assert that all edit blocks are marked as invalid
	assert.Equal(t, 0, len(validEditBlocks), "Expected 0 valid edit blocks")
	assert.Equal(t, len(editBlocks), len(invalidReports), "Expected all edit blocks to be invalid")

	// Check that the error messages are correct
	for _, report := range invalidReports {
		assert.Equal(t, "No code context found in the chat history that matches this edit block's old lines. You must ensure the old lines are present in the code context by using one of the tools before making an edit block.", report.Error)
	}
}

func TestApplyEditBlockActivity_basicCRUD(t *testing.T) {
	tests := []struct {
		name            string
		isExistingFile  bool
		existingContent string
		editBlock       EditBlock
		wantErr         bool
		expectedContent string
	}{
		{
			name:            "File does not exist & OldLines are empty",
			isExistingFile:  false,
			editBlock:       EditBlock{EditType: "create", FilePath: "nonexistent.txt", NewLines: []string{"New content"}},
			wantErr:         false,
			expectedContent: "New content",
		},
		{
			name:           "File exists but we're trying to create it",
			isExistingFile: true,
			editBlock:      EditBlock{EditType: "create", FilePath: "existing.txt", NewLines: []string{"New content"}},
			wantErr:        true,
		},
		{
			name:           "File does not exist & OldLines are not empty should fail",
			isExistingFile: false,
			editBlock:      EditBlock{EditType: "update", FilePath: "nonexistent.txt", OldLines: []string{"Old content"}, NewLines: []string{"New content"}},
			wantErr:        true,
		},
		//{
		//	name:            "Update when file exists but is empty and old lines is empty",
		//	isExistingFile:  true,
		//	existingContent: "",
		//	editBlock:      EditBlock{EditType: "update", FilePath: "existing.txt", OldLines: []string{}, NewLines: []string{"Updated content"}},
		//	wantErr:         false,
		//	expectedContent: "Updated content",
		//},
		{
			name:            "Append when file exists but is empty",
			isExistingFile:  true,
			existingContent: "",
			editBlock:       EditBlock{EditType: "append", FilePath: "existing.txt", OldLines: []string{}, NewLines: []string{"Updated content"}},
			wantErr:         false,
			expectedContent: "Updated content",
		},
		{
			name:            "Append when file exists with content",
			isExistingFile:  true,
			existingContent: "Old content",
			editBlock:       EditBlock{EditType: "append", FilePath: "existing.txt", OldLines: []string{}, NewLines: []string{"Updated content"}},
			wantErr:         false,
			expectedContent: "Old content\nUpdated content",
		},
		{
			name:            "File exists and OldLines match existing lines",
			isExistingFile:  true,
			existingContent: "Old content",
			editBlock:       EditBlock{EditType: "update", FilePath: "existing.txt", OldLines: []string{"Old content"}, NewLines: []string{"New content"}},
			wantErr:         false,
		},
		{
			name:            "File exists and OldLines do not match existing lines",
			isExistingFile:  true,
			existingContent: "Old content",
			editBlock:       EditBlock{EditType: "update", FilePath: "existing.txt", OldLines: []string{"Non-matching content"}, NewLines: []string{"New content"}},
			wantErr:         true,
		},
		{
			name:           "File exists and EditType is delete",
			isExistingFile: true,
			editBlock:      EditBlock{EditType: "delete", FilePath: "existing.txt", OldLines: []string{}, NewLines: []string{}},
			wantErr:        false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir, err := os.MkdirTemp("", "base")
			assert.Nil(t, err)
			defer os.RemoveAll(tmpDir)

			if tt.isExistingFile {
				filePath := filepath.Join(tmpDir, "existing.txt")
				err = os.WriteFile(filePath, []byte(tt.existingContent), 0644)
				assert.Nil(t, err)
			}

			envContainer := env.EnvContainer{
				Env: &env.LocalEnv{
					WorkingDirectory: tmpDir,
				},
			}

			reports, err := ApplyEditBlocksActivity(
				ApplyEditBlockActivityInput{
					EnvContainer: envContainer,
					EditBlocks:   []EditBlock{tt.editBlock},
				},
			)

			if tt.wantErr {
				assert.Nil(t, err)
				assert.NotNil(t, reports[0].Error)
				assert.NotEmpty(t, reports[0].Error)
			} else {
				assert.Nil(t, err)
				assert.Equal(t, "", reports[0].Error)
			}

			if tt.expectedContent != "" {
				filePath := filepath.Join(tmpDir, tt.editBlock.FilePath)
				content, _ := os.ReadFile(filePath)
				assert.Equal(t, tt.expectedContent, string(content))
			}

			if tt.editBlock.EditType == "delete" {
				filePath := filepath.Join(tmpDir, tt.editBlock.FilePath)
				_, err = os.Stat(filePath)
				assert.True(t, os.IsNotExist(err))
			}
		})
	}
}

func TestGetUpdatedContents(t *testing.T) {
	tests := []struct {
		name             string
		block            EditBlock
		originalContents string
		expectedContents string
		expectedError    error
	}{
		{
			name: "Successful Edit Middle",
			block: EditBlock{
				FilePath: "test.txt",
				OldLines: []string{"line2", "line3"},
				NewLines: []string{"newLine2", "newLine3"},
			},
			originalContents: "line1\nline2\nline3\nline4",
			expectedContents: "line1\nnewLine2\nnewLine3\nline4",
			expectedError:    nil,
		},
		{
			name: "No Match",
			block: EditBlock{
				FilePath: "test.txt",
				OldLines: []string{"line3", "line2"},
				NewLines: []string{"newNoMatch1", "newNoMatch2"},
			},
			originalContents: "line1\nline2\nline3\nline4",
			expectedContents: "",
			expectedError:    fmt.Errorf("no good match found for the following edit block old lines:\n\n%s\n\nFailed to match these lines:\n\n%s\n\nInstead, found these lines:\n\n%s\n", "line3\nline2", "line2", "line4"),
		},
		{
			name: "Successful Edit Start",
			block: EditBlock{
				FilePath: "test.txt",
				OldLines: []string{"line1"},
				NewLines: []string{"newLine1"},
			},
			originalContents: "line1\nline2\nline3\nline4",
			expectedContents: "newLine1\nline2\nline3\nline4",
			expectedError:    nil,
		},
		{
			name: "Successful Edit End",
			block: EditBlock{
				FilePath: "test.txt",
				OldLines: []string{"line4"},
				NewLines: []string{"newLine4"},
			},
			originalContents: "line1\nline2\nline3\nline4",
			expectedContents: "line1\nline2\nline3\nnewLine4",
			expectedError:    nil,
		},
		{
			name: "More lines after",
			block: EditBlock{
				FilePath: "test.txt",
				OldLines: []string{"line2", "line3"},
				NewLines: []string{"line2", "newLineA", "newLineB", "line3"},
			},
			originalContents: "line1\nline2\nline3\nline4\nline5",
			expectedContents: "line1\nline2\nnewLineA\nnewLineB\nline3\nline4\nline5",
			expectedError:    nil,
		},
		{
			name: "Less lines after",
			block: EditBlock{
				FilePath: "test.txt",
				OldLines: []string{"line2", "line3"},
				NewLines: []string{},
			},
			originalContents: "line1\nline2\nline3\nline4",
			expectedContents: "line1\nline4",
		},
		{
			name: "Lines don't match indentation of original contents",
			block: EditBlock{
				FilePath: "test.txt",
				OldLines: []string{"    line2", "  line3"},
				NewLines: []string{"line2-x", "line3-x"},
			},
			originalContents: "line1\n\tline2\n\tline3\nline4",
			expectedContents: "line1\nline2-x\nline3-x\nline4",
		},
		{
			name: "Multiple matches",
			block: EditBlock{
				FilePath: "test.txt",
				OldLines: []string{"1"},
				NewLines: []string{"2"},
			},
			originalContents: "a\n1\nx\ny\nz\nb\n1",
			expectedContents: "",
			expectedError:    fmt.Errorf(multipleMatchesMessage, search, "File: test.txt\nLines: 1-4\n```\na\n1\nx\ny\n```\n\nFile: test.txt\nLines: 5-7\n```\nz\nb\n1\n```"),
		},

		{
			name: "Extra Empty Lines in Original",
			block: EditBlock{
				OldLines: []string{"line2", "line3"},
				NewLines: []string{"line2-x", "line3-x"},
			},
			originalContents: "line1\nline2\n\n \nline3\nline4",
			expectedContents: "line1\nline2-x\nline3-x\nline4",
			expectedError:    nil,
		},
		{
			name: "Extra Empty Lines in Block",
			block: EditBlock{
				OldLines: []string{"line2", "", " ", "line3"},
				NewLines: []string{"line2-x", "", " ", "line3-x"},
			},
			originalContents: "line1\nline2\nline3\nline4",
			expectedContents: "line1\nline2-x\n\n \nline3-x\nline4",
			expectedError:    nil,
		},
		{
			name: "Minor Differences Still a Match",
			block: EditBlock{
				OldLines: []string{"line2 ", " line3"},
				NewLines: []string{"line2-x ", " line-x"},
			},
			originalContents: "line1\n line2\nline3 \nline4",
			expectedContents: "line1\nline2-x \n line-x\nline4",
			expectedError:    nil,
		},
		// FIXME this case isn't yet handled properly
		//{
		//	name: "Multiple matches but not really cos one has many more lines with comments matching and the other doesn't",
		//	block: EditBlock{
		//		FilePath: "test.txt",
		//		OldLines: []string{"regular code line", "// comment1", "// comment2", "// comment3"},
		//		NewLines: []string{"regular code line", "// comment1", "// comment2", "// comment3", "new code line"},
		//	},
		//	originalContents: "regular code line\n// comment1\n// comment2\n// comment3\nregular code line",
		//	expectedContents: "regular code line\n// comment1\n// comment2\n// comment3\nnew code line\nregular code line",
		//	expectedError:    nil,
		//},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := getUpdatedContents(tt.block, tt.originalContents)
			if result != tt.expectedContents {
				t.Errorf("Expected content:\n%s\n\nGot:\n%s", tt.expectedContents, result)
			}
			if (err != nil && tt.expectedError == nil) ||
				(err == nil && tt.expectedError != nil) ||
				(err != nil && err.Error() != tt.expectedError.Error()) {
				t.Errorf("Expected error: %v, got: %v", tt.expectedError, err)
			}
		})
	}
}

func TestGetUpdatedContentsWithVisibleRanges(t *testing.T) {
	minimumFileRangeVisibilityMargin = 0 // we don't want any margin for these tests since that adds a lot of unnecessary whitespace and thinking
	tests := []struct {
		name             string
		block            EditBlock
		originalContents string
		expectedContents string
		expectedError    error
	}{
		{
			name: "Edit within Visible range",
			block: EditBlock{
				FilePath: "test.txt",
				OldLines: []string{"line2", "line3"},
				NewLines: []string{"newLine2", "newLine3"},
				VisibleFileRanges: []FileRange{
					{FilePath: "test.txt", StartLine: 1, EndLine: 4},
				},
			},
			originalContents: "line1\nline2\nline3\nline4\nline5",
			expectedContents: "line1\nnewLine2\nnewLine3\nline4\nline5",
			expectedError:    nil,
		},
		{
			name: "Edit exactly matching Visible range",
			block: EditBlock{
				FilePath: "test.txt",
				OldLines: []string{"line2", "line3"},
				NewLines: []string{"newLine2", "newLine3"},
				VisibleFileRanges: []FileRange{
					{FilePath: "test.txt", StartLine: 2, EndLine: 3},
				},
			},
			originalContents: "line1\nline2\nline3\nline4\nline5",
			expectedContents: "line1\nnewLine2\nnewLine3\nline4\nline5",
			expectedError:    nil,
		},
		{
			name: "Edit outside Visible range",
			block: EditBlock{
				FilePath: "test.txt",
				OldLines: []string{"line4", "line5"},
				NewLines: []string{"newLine4", "newLine5"},
				VisibleFileRanges: []FileRange{
					{FilePath: "test.txt", StartLine: 1, EndLine: 3},
				},
			},
			originalContents: "line1\nline2\nline3\nline4\nline5",
			expectedContents: "",
			expectedError:    fmt.Errorf("no good match found for the following edit block old lines:\n\nline4\nline5\n"),
		},
		{
			name: "Multiple non-adjacent visible ranges",
			block: EditBlock{
				FilePath: "test.txt",
				OldLines: []string{"line3", "line4"},
				NewLines: []string{"newLine3", "newLine4"},
				VisibleFileRanges: []FileRange{
					{FilePath: "test.txt", StartLine: 1, EndLine: 1},
					{FilePath: "test.txt", StartLine: 3, EndLine: 4},
				},
			},
			originalContents: "line1\nline2\nline3\nline4\nline5",
			expectedContents: "line1\nline2\nnewLine3\nnewLine4\nline5",
			expectedError:    nil,
		},
		{
			name: "Multiple matches but only first is visible",
			block: EditBlock{
				FilePath: "test.txt",
				OldLines: []string{"x"},
				NewLines: []string{"x_x"},
				VisibleFileRanges: []FileRange{
					{FilePath: "test.txt", StartLine: 1, EndLine: 1},
				},
			},
			originalContents: "x\n2\n3\n4\n5\nx\n7\n8\n9\nx\n11",
			expectedContents: "x_x\n2\n3\n4\n5\nx\n7\n8\n9\nx\n11",
			expectedError:    nil,
		},
		{
			name: "Multiple matches but only middle is visible",
			block: EditBlock{
				FilePath: "test.txt",
				OldLines: []string{"x"},
				NewLines: []string{"x_x"},
				VisibleFileRanges: []FileRange{
					{FilePath: "test.txt", StartLine: 6, EndLine: 6},
				},
			},
			originalContents: "x\n2\n3\n4\n5\nx\n7\n8\n9\nx\n11",
			expectedContents: "x\n2\n3\n4\n5\nx_x\n7\n8\n9\nx\n11",
			expectedError:    nil,
		},
		{
			name: "Multiple matches but only last is visible",
			block: EditBlock{
				FilePath: "test.txt",
				OldLines: []string{"x"},
				NewLines: []string{"x_x"},
				VisibleFileRanges: []FileRange{
					{FilePath: "test.txt", StartLine: 10, EndLine: 10},
				},
			},
			originalContents: "x\n2\n3\n4\n5\nx\n7\n8\n9\nx\n11",
			expectedContents: "x\n2\n3\n4\n5\nx\n7\n8\n9\nx_x\n11",
			expectedError:    nil,
		},
		{
			name: "Multiple matches which are both visible in one range",
			block: EditBlock{
				FilePath: "test.txt",
				OldLines: []string{"x"},
				NewLines: []string{"x_x"},
				VisibleFileRanges: []FileRange{
					{FilePath: "test.txt", StartLine: 1, EndLine: 6},
				},
			},
			originalContents: "x\n2\n3\n4\n5\nx\n7\n8\n9\nx\n11",
			expectedContents: "",
			expectedError:    fmt.Errorf(multipleMatchesMessage, search, "File: test.txt\nLines: 1-3\n```\nx\n2\n3\n```\n\nFile: test.txt\nLines: 4-8\n```\n4\n5\nx\n7\n8\n```"),
		},
		{
			name: "Multiple matches which are both visible across 2 ranges",
			block: EditBlock{
				FilePath: "test.txt",
				OldLines: []string{"x"},
				NewLines: []string{"x_x"},
				VisibleFileRanges: []FileRange{
					{FilePath: "test.txt", StartLine: 1, EndLine: 1},
					{FilePath: "test.txt", StartLine: 6, EndLine: 6},
				},
			},
			originalContents: "x\n2\n3\n4\n5\nx\n7\n8\n9\nx\n11",
			expectedContents: "",
			expectedError:    fmt.Errorf(multipleMatchesMessage, search, "File: test.txt\nLines: 1-3\n```\nx\n2\n3\n```\n\nFile: test.txt\nLines: 4-8\n```\n4\n5\nx\n7\n8\n```"),
		},
		{
			name: "Edit partially overlapping Visible range start",
			block: EditBlock{
				FilePath: "test.txt",
				OldLines: []string{"line3", "line4"},
				NewLines: []string{"newLine3", "newLine4"},
				VisibleFileRanges: []FileRange{
					{FilePath: "test.txt", StartLine: 1, EndLine: 3},
				},
			},
			originalContents: "line1\nline2\nline3\nline4\nline5",
			expectedContents: "",
			expectedError:    fmt.Errorf("no good match found for the following edit block old lines:\n\nline3\nline4\n"),
		},
		{
			name: "Edit partially overlapping Visible range end",
			block: EditBlock{
				FilePath: "test.txt",
				OldLines: []string{"line3", "line4"},
				NewLines: []string{"newLine3", "newLine4"},
				VisibleFileRanges: []FileRange{
					{FilePath: "test.txt", StartLine: 4, EndLine: 5},
				},
			},
			originalContents: "line1\nline2\nline3\nline4\nline5",
			expectedContents: "",
			expectedError:    fmt.Errorf("no good match found for the following edit block old lines:\n\nline3\nline4\n"),
		},
		{
			name: "Nil Visible ranges: acts like everything is Visible",
			block: EditBlock{
				FilePath:          "test.txt",
				OldLines:          []string{"line2", "line3"},
				NewLines:          []string{"newLine2", "newLine3"},
				VisibleFileRanges: nil,
			},
			originalContents: "line1\nline2\nline3\nline4\nline5",
			expectedContents: "line1\nnewLine2\nnewLine3\nline4\nline5",
			expectedError:    nil,
		},
		{
			name: "Empty Visible ranges: nothing is Visible",
			block: EditBlock{
				FilePath:          "test.txt",
				OldLines:          []string{"line2", "line3"},
				NewLines:          []string{"newLine2", "newLine3"},
				VisibleFileRanges: []FileRange{},
			},
			originalContents: "line1\nline2\nline3\nline4\nline5",
			expectedContents: "",
			expectedError:    fmt.Errorf("no good match found for the following edit block old lines:\n\nline2\nline3\n"),
		},
		{
			name: "Edit spanning multiple Visible ranges and invisible range",
			block: EditBlock{
				FilePath: "test.txt",
				OldLines: []string{"line2", "line3", "line4"},
				NewLines: []string{"newLine2", "newLine3", "newLine4"},
				VisibleFileRanges: []FileRange{
					{FilePath: "test.txt", StartLine: 1, EndLine: 2},
					{FilePath: "test.txt", StartLine: 4, EndLine: 5},
				},
			},
			originalContents: "line1\nline2\nline3\nline4\nline5",
			expectedContents: "",
			expectedError:    fmt.Errorf("no good match found for the following edit block old lines:\n\nline2\nline3\nline4\n"),
		},
		{
			name: "Edit spanning multiple adjacent Visible ranges",
			block: EditBlock{
				FilePath: "test.txt",
				OldLines: []string{"line2", "line3", "line4"},
				NewLines: []string{"newLine2", "newLine3", "newLine4"},
				VisibleFileRanges: []FileRange{
					{FilePath: "test.txt", StartLine: 1, EndLine: 2},
					{FilePath: "test.txt", StartLine: 3, EndLine: 5},
				},
			},
			originalContents: "line1\nline2\nline3\nline4\nline5",
			expectedContents: "line1\nnewLine2\nnewLine3\nnewLine4\nline5",
			expectedError:    nil,
		},
		{
			name: "Edit spanning multiple overlapping Visible ranges",
			block: EditBlock{
				FilePath: "test.txt",
				OldLines: []string{"line2", "line3", "line4"},
				NewLines: []string{"newLine2", "newLine3", "newLine4"},
				VisibleFileRanges: []FileRange{
					{FilePath: "test.txt", StartLine: 1, EndLine: 3},
					{FilePath: "test.txt", StartLine: 3, EndLine: 5},
				},
			},
			originalContents: "line1\nline2\nline3\nline4\nline5",
			expectedContents: "line1\nnewLine2\nnewLine3\nnewLine4\nline5",
			expectedError:    nil,
		},
		{
			name: "Edit entirely outside Visible ranges",
			block: EditBlock{
				FilePath: "test.txt",
				OldLines: []string{"line2", "line3"},
				NewLines: []string{"newLine2", "newLine3"},
				VisibleFileRanges: []FileRange{
					{FilePath: "test.txt", StartLine: 4, EndLine: 5},
				},
			},
			originalContents: "line1\nline2\nline3\nline4\nline5",
			expectedContents: "",
			expectedError:    fmt.Errorf("no good match found for the following edit block old lines:\n\nline2\nline3\n"),
		},
		{
			name: "Empty edit block (no changes)",
			block: EditBlock{
				FilePath: "test.txt",
				OldLines: []string{"line2", "line3"},
				NewLines: []string{"line2", "line3"},
				VisibleFileRanges: []FileRange{
					{FilePath: "test.txt", StartLine: 1, EndLine: 5},
				},
			},
			originalContents: "line1\nline2\nline3\nline4\nline5",
			expectedContents: "line1\nline2\nline3\nline4\nline5",
			expectedError:    nil,
		},
		{
			name: "Edit affecting last line of the file",
			block: EditBlock{
				FilePath: "test.txt",
				OldLines: []string{"line4", "line5"},
				NewLines: []string{"line4", "newLine5", "newLine6"},
				VisibleFileRanges: []FileRange{
					{FilePath: "test.txt", StartLine: 1, EndLine: 5},
				},
			},
			originalContents: "line1\nline2\nline3\nline4\nline5",
			expectedContents: "line1\nline2\nline3\nline4\nnewLine5\nnewLine6",
			expectedError:    nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := getUpdatedContents(tt.block, tt.originalContents)
			if result != tt.expectedContents {
				t.Errorf("Expected content:\n%s\n\nGot:\n%s", tt.expectedContents, result)
			}
			if (err != nil && tt.expectedError == nil) ||
				(err == nil && tt.expectedError != nil) ||
				(err != nil && err.Error() != tt.expectedError.Error()) {
				t.Errorf("Expected error: %v, got: %v", tt.expectedError, err)
			}
		})
	}
}

func TestApplyEditBlocks_withMultipleEditBlocksAndVisibleFileRanges(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a tempfile under the given tmpDir with the name "side.toml". It can be empty, which is still valid.
	_, err := os.Create(filepath.Join(tmpDir, "side.toml"))
	require.NoError(t, err)

	// Create a temporary file with initial content
	tmpFile, err := os.Create(filepath.Join(tmpDir, "temp.go"))
	require.Nil(t, err)
	_, err = tmpFile.WriteString(`package main

func main() {
	fmt.Println("Hello, world!")
}`)
	require.Nil(t, err)
	tmpFile.Close()

	initCmd := exec.Command("git", "init")
	initCmd.Dir = tmpDir
	err = initCmd.Run()
	require.NoError(t, err)

	// git add + commit when not creating the file, so restoring doesn't remove the file and diff is correct
	addCmd := exec.Command("git", "add", ".")
	addCmd.Dir = tmpDir
	err = addCmd.Run()
	require.NoError(t, err)

	commitCmd := exec.Command("git", "commit", "-m", "Initial commit")
	commitCmd.Dir = tmpDir
	err = commitCmd.Run()
	require.NoError(t, err)

	// Define the edit blocks
	editBlocks := []EditBlock{
		{
			FilePath: "temp.go",
			OldLines: []string{"func main() {"},
			NewLines: []string{
				"func main() {",
				"\tfmt.Println(\"Starting...\")",
			},
			EditType: "update",
			VisibleFileRanges: []FileRange{
				{FilePath: "temp.go", StartLine: 1, EndLine: 5},
			},
		},
		{
			FilePath: "temp.go",
			OldLines: []string{"}"},
			NewLines: []string{
				"\tfmt.Println(\"Hello, again!\")",
				"}",
			},
			EditType: "update",
			VisibleFileRanges: []FileRange{
				{FilePath: "temp.go", StartLine: 1, EndLine: 5},
			},
		},
	}

	// Expected content after applying the edit blocks
	expectedContent := `package main

import "fmt"

func main() {
	fmt.Println("Starting...")
	fmt.Println("Hello, world!")
	fmt.Println("Hello, again!")
}`

	// Run the test
	envContainer := env.EnvContainer{
		Env: &env.LocalEnv{
			WorkingDirectory: tmpDir,
		},
	}

	reports, err := ApplyEditBlocksActivity(
		ApplyEditBlockActivityInput{
			EnvContainer: envContainer,
			EditBlocks:   editBlocks,
			EnabledFlags: []string{fflag.CheckEdits},
		},
	)

	assert.Nil(t, err)
	for _, report := range reports {
		assert.Equal(t, "", report.Error)
		assert.True(t, report.DidApply)
	}

	content, _ := os.ReadFile(filepath.Join(tmpDir, "temp.go"))
	assert.Equal(t, expectedContent, string(content))
}

func TestApplyEditBlocks_checkEditsFeatureFlagEnabled_goLang(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "editBlocksTest")
	require.Nil(t, err)
	defer os.RemoveAll(tmpDir)

	tmpFile, err := os.Create(filepath.Join(tmpDir, "temp.go"))
	require.Nil(t, err)
	_, err = tmpFile.WriteString("original content")
	require.Nil(t, err)
	tmpFile.Close()

	// test case 1: good change, resulting a passed check and the file should have the new changes, and be staged in git
	// test case 2: bad change, resulting a non-passing check and the file should NOT have the new changes, and NOT be staged in git
	tests := []struct {
		name            string
		editBlock       EditBlock
		wantErr         bool
		expectedContent string
	}{
		{
			name:            "Change that passes the check - should pass and be staged in git",
			editBlock:       EditBlock{EditType: "update", FilePath: "temp1.go", OldLines: strings.Split("package main\n\nimport \"fmt\"\n\nfunc main() {\n    fmt.Println(\"Hello, world!\")\n}", "\n"), NewLines: strings.Split("package main\n\nimport \"fmt\"\n\nfunc main() {\n    fmt.Println(\"Hello, Go!\")\n}", "\n")},
			wantErr:         false,
			expectedContent: "package main\n\nimport \"fmt\"\n\nfunc main() {\n    fmt.Println(\"Hello, Go!\")\n}",
		},
		{
			name:            "Bad change - should fail check and restore the original content, and NOT be staged in git",
			editBlock:       EditBlock{EditType: "update", FilePath: "temp2.go", OldLines: strings.Split("package main\n\nfunc main() {\n    fmt.Println(\"Hello, world!\")\n}", "\n"), NewLines: []string{"bad content that can't even be autofixed"}},
			wantErr:         true,
			expectedContent: "package main\n\nfunc main() {\n    fmt.Println(\"Hello, world!\")\n}",
		},
		{
			name:      "New Invalid File - should fail check and restore the original content, and NOT be staged in git",
			editBlock: EditBlock{EditType: "create", FilePath: "new_invalid.go", OldLines: []string{}, NewLines: []string{"bad content that can't even be autofixed"}},
			wantErr:   true,
		},
		{
			name:            "New valid File - should pass check and be staged in git",
			editBlock:       EditBlock{EditType: "create", FilePath: "new_valid.go", OldLines: []string{}, NewLines: strings.Split("package main\n\nimport \"fmt\"\n\nfunc main() {\n    fmt.Println(\"Hello, world!\")\n}", "\n")},
			wantErr:         false,
			expectedContent: "package main\n\nimport \"fmt\"\n\nfunc main() {\n    fmt.Println(\"Hello, world!\")\n}",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir, err := os.MkdirTemp("", "editBlocksTest")
			assert.Nil(t, err)
			//defer os.RemoveAll(tmpDir)

			// Create a tempfile under the given tmpDir with the name "side.toml". It can be empty, which is still valid.
			_, err = os.Create(filepath.Join(tmpDir, "side.toml"))
			require.NoError(t, err)

			initCmd := exec.Command("git", "init")
			initCmd.Dir = tmpDir
			err = initCmd.Run()
			require.NoError(t, err)

			if tt.editBlock.EditType == "update" {
				tmpFile, err := os.Create(filepath.Join(tmpDir, tt.editBlock.FilePath))
				assert.Nil(t, err)
				_, err = tmpFile.WriteString(strings.Join(tt.editBlock.OldLines, "\n"))
				assert.Nil(t, err)
				tmpFile.Close()

				// git add + commit when not creating the file, so restoring doesn't remove the file and diff is correct
				addCmd := exec.Command("git", "add", ".")
				addCmd.Dir = tmpDir
				err = addCmd.Run()
				require.NoError(t, err)

				commitCmd := exec.Command("git", "commit", "-m", "Initial commit")
				commitCmd.Dir = tmpDir
				err = commitCmd.Run()
				require.NoError(t, err)
			}

			envContainer := env.EnvContainer{
				Env: &env.LocalEnv{
					WorkingDirectory: tmpDir,
				},
			}

			reports, err := ApplyEditBlocksActivity(
				ApplyEditBlockActivityInput{
					EnvContainer: envContainer,
					EditBlocks:   []EditBlock{tt.editBlock},
					EnabledFlags: []string{fflag.CheckEdits},
				},
			)

			if tt.wantErr {
				assert.NotNil(t, reports[0].Error)
			} else {
				assert.Nil(t, err)
				assert.Equal(t, "", reports[0].Error)
			}

			if tt.expectedContent != "" {
				content, _ := os.ReadFile(filepath.Join(tmpDir, tt.editBlock.FilePath))
				assert.Equal(t, tt.expectedContent, string(content))
			} else {
				_, err := os.Stat(filepath.Join(tmpDir, tt.editBlock.FilePath))
				assert.Error(t, err)
				assert.True(t, os.IsNotExist(err))
			}

			// Check if the file is correctly staged in git
			diffCmd := exec.Command("git", "diff", "--cached", "--name-only")
			diffCmd.Dir = tmpDir
			out, err := diffCmd.Output()
			assert.Nil(t, err)
			if tt.wantErr {
				assert.Empty(t, string(out))
			} else {
				assert.Contains(t, string(out), tt.editBlock.FilePath)
			}
		})
	}
}

const rstLines = `# Licensed under a 3-clause BSD style license
"""
:Author: Simon Gibbons (simongibbons@gmail.com)
"""


from .core import DefaultSplitter
from .fixedwidth import (
    FixedWidth,
    FixedWidthData,
    FixedWidthHeader,
    FixedWidthTwoLineDataSplitter,
)


class SimpleRSTHeader(FixedWidthHeader):
    position_line = 0
    start_line = 1
    splitter_class = DefaultSplitter
    position_char = "="

    def get_fixedwidth_params(self, line):
        vals, starts, ends = super().get_fixedwidth_params(line)
        # The right hand column can be unbounded
        ends[-1] = None
        return vals, starts, ends


class SimpleRSTData(FixedWidthData):
    start_line = 3
    end_line = -1
    splitter_class = FixedWidthTwoLineDataSplitter


class RST(FixedWidth):
    """reStructuredText simple format table.

    See: https://docutils.sourceforge.io/docs/ref/rst/restructuredtext.html#simple-tables

    Example::

        ==== ===== ======
        Col1  Col2  Col3
        ==== ===== ======
          1    2.3  Hello
          2    4.5  Worlds
        ==== ===== ======

    Currently there is no support for reading tables which utilize continuation lines,
    or for ones which define column spans through the use of an additional
    line of dashes in the header.

    """

    _format_name = "rst"
    _description = "reStructuredText simple table"
    data_class = SimpleRSTData
    header_class = SimpleRSTHeader

    def __init__(self):
        super().__init__(delimiter_pad=None, bookend=False)

    def write(self, lines):
        lines = super().write(lines)
        lines = [lines[1]] + lines + [lines[1]]
        return lines`

func TestFindPotentialMatchesWithVisibleFileRangeAtEndEdge(t *testing.T) {
	editBlock := EditBlock{
		FilePath: "astropy/io/ascii/rst.py",
		OldLines: []string{
			" def write(self, lines):",
			" lines = super().write(lines)",
			" lines = [lines[1]] + lines + [lines[1]]",
			" return lines",
		},
		NewLines: []string{
			" def write(self, lines):",
			" if self.header_rows is None:",
			" lines = super().write(lines)",
			" lines = [lines[1]] + lines + [lines[1]]",
			" else:",
			" data_lines = super().write(lines)",
			" header_lines = self._create_header_lines(data_lines[0], self.header_rows)",
			" separator = self._create_separator(data_lines[0])",
			" lines = [separator] + header_lines + [separator] + data_lines[2:] + [separator]",
			" return lines",
			"",
			" def _create_header_lines(self, data_line, header_rows):",
			" return [' '.join(f'{col:<{len(part)}}' for col, part in zip(row, data_line.split())) for row in header_rows]",
			"",
			" def _create_separator(self, data_line):",
			" return ''.join('=' * len(part) for part in data_line.split())",
		},
		EditType:       "update",
		SequenceNumber: 2,
		VisibleFileRanges: []FileRange{
			{
				FilePath:  "astropy/io/ascii/rst.py",
				StartLine: 39,
				EndLine:   63, // it's actually on line 66, but we put in a bad value here to test the margin
			},
		},
	}
	minimumFileRangeVisibilityMargin = 5
	bestMatch, matches := FindAcceptableMatch(editBlock, strings.Split(rstLines, "\n"), true)
	assert.Equal(t, 1, len(matches))
	assert.Greater(t, bestMatch.score, 0.0)
}

func TestFindPotentialMatchesWithMissingVisibleFileRangesButWeFigureItOut(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "*.py")
	tmpFile.Write([]byte(rstLines))
	if err != nil {
		t.Fatal(err)
	}

	editBlock := EditBlock{
		FilePath:         tmpFile.Name(),
		AbsoluteFilePath: tmpFile.Name(),
		OldLines: []string{
			" def write(self, lines):",
			" lines = super().write(lines)",
			" lines = [lines[1]] + lines + [lines[1]]",
			" return lines",
		},
		NewLines: []string{
			" def write(self, lines):",
			" if self.header_rows is None:",
			" lines = super().write(lines)",
			" lines = [lines[1]] + lines + [lines[1]]",
			" else:",
			" data_lines = super().write(lines)",
			" header_lines = self._create_header_lines(data_lines[0], self.header_rows)",
			" separator = self._create_separator(data_lines[0])",
			" lines = [separator] + header_lines + [separator] + data_lines[2:] + [separator]",
			" return lines",
			"",
			" def _create_header_lines(self, data_line, header_rows):",
			" return [' '.join(f'{col:<{len(part)}}' for col, part in zip(row, data_line.split())) for row in header_rows]",
			"",
			" def _create_separator(self, data_line):",
			" return ''.join('=' * len(part) for part in data_line.split())",
		},
		EditType:          "update",
		SequenceNumber:    2,
		VisibleFileRanges: []FileRange{},
		VisibleCodeBlocks: []tree_sitter.CodeBlock{
			{
				FilePath: tmpFile.Name(),
				Symbol:   "RST",
			},
		},
	}
	minimumFileRangeVisibilityMargin = 5
	bestMatch, matches := FindAcceptableMatch(editBlock, strings.Split(rstLines, "\n"), true)
	assert.Equal(t, 1, len(matches))
	assert.Greater(t, bestMatch.score, 0.0)
}
