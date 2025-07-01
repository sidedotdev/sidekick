package dev

import (
	"reflect"
	"testing"

	"sidekick/coding/tree_sitter"
	"sidekick/common"
	"sidekick/llm" // Changed from common to llm
	// Added for utils.Filter
	// Added for slices.Clone
)

type EditBlockTestCase struct {
	name           string
	testInput      string
	expectedResult []*EditBlock
}

var basicCase = EditBlockTestCase{
	name: "Basic edit block",
	testInput: ` This is a basic edit block:

` + "```" + `go
edit_block:1
path/to/file.go
` + search + `
	if err != nil {
		return "", err
	}
` + divider + `
	if err != nil {
		return "", err
	}

	// Deposit money.
	var depositOutput string
	depositErr := workflow.ExecuteActivity(ctx, Deposit, input).Get(ctx, &depositOutput)
` + replace + `
` + "```" + `
`,
	expectedResult: []*EditBlock{
		{
			FilePath: "path/to/file.go",
			OldLines: []string{
				"	if err != nil {",
				"		return \"\", err",
				"	}",
			},
			NewLines: []string{
				"	if err != nil {",
				"		return \"\", err",
				"	}",
				"",
				"	// Deposit money.",
				"	var depositOutput string",
				"	depositErr := workflow.ExecuteActivity(ctx, Deposit, input).Get(ctx, &depositOutput)",
			},
			EditType:       "update",
			SequenceNumber: 1,
		},
	},
}

var extraContent = EditBlockTestCase{
	name: "Extra content in the code fence",
	testInput: `
` + "```" + `go
some extra stuff early on
some extra stuff early on
some extra stuff early on
edit_block:2
extra.go
` + search + `
a
` + divider + `
b
` + replace + `
some extra stuff later on
some extra stuff later on
some extra stuff later on
` + "```" + `
`,
	expectedResult: []*EditBlock{
		{
			FilePath:       "extra.go",
			OldLines:       []string{"a"},
			NewLines:       []string{"b"},
			EditType:       "update",
			SequenceNumber: 2,
		},
	},
}

var missingFileNameAndSequenceNumber = EditBlockTestCase{
	name: "Missing File Name and Sequence Number",
	testInput: `This is missing the filename and sequence number:

` + "```" + `go
` + search + `
stuff
` + divider + `
more stuff
` + replace + `
` + "```" + `
`,
	expectedResult: []*EditBlock{
		{
			FilePath: "",
			OldLines: []string{"stuff"},
			NewLines: []string{"more stuff"},
			EditType: "update",
		},
	},
}

var missingFileName = EditBlockTestCase{
	name: "Missing File Name",
	testInput: `This is missing the filename:

` + "```" + `go
edit_block:1
` + search + `
stuff
` + divider + `
more stuff
` + replace + `
` + "```" + `
`,
	expectedResult: []*EditBlock{
		{
			FilePath:       "",
			OldLines:       []string{"stuff"},
			NewLines:       []string{"more stuff"},
			EditType:       "update",
			SequenceNumber: 1,
		},
	},
}

var missingSequenceNumber = EditBlockTestCase{
	name: "Missing Sequence Number",
	testInput: `This is missing the filename and sequence number:

` + "```" + `go
omg.go
` + search + `
stuff
` + divider + `
more stuff
` + replace + `
` + "```" + `
`,
	expectedResult: []*EditBlock{
		{
			FilePath: "omg.go",
			OldLines: []string{"stuff"},
			NewLines: []string{"more stuff"},
			EditType: "update",
		},
	},
}

var newFile = EditBlockTestCase{
	name: "Valid edit to create a new file",
	testInput: `This is a new file:

` + "```" + `go
edit_block:1
new.go
<<<<<<< CREATE_FILE
` + divider + `
new
>>>>>>> NEW_LINES
` + "```" + `
`,
	expectedResult: []*EditBlock{
		{
			FilePath:       "new.go",
			OldLines:       nil,
			NewLines:       []string{"new"},
			EditType:       "create",
			SequenceNumber: 1,
		},
	},
}

var appendFile = EditBlockTestCase{
	name: "Valid edit to append to an existing File",
	testInput: `This is an existing file:

` + "```" + `go
edit_block:1
existing.go
<<<<<<< APPEND_TO_FILE
` + divider + `
new
>>>>>>> NEW_LINES
` + "```" + `
`,
	expectedResult: []*EditBlock{
		{
			FilePath:       "existing.go",
			OldLines:       nil,
			NewLines:       []string{"new"},
			EditType:       "append",
			SequenceNumber: 1,
		},
	},
}

var missingDividerAppendFile = EditBlockTestCase{
	name: "Missing divider when appending to an existing File",
	testInput: `This is an existing file:

` + "```" + `go
edit_block:1
existing.go
<<<<<<< APPEND_TO_FILE
new
>>>>>>> NEW_LINES
` + "```" + `
`,
	expectedResult: []*EditBlock{
		{
			FilePath:       "existing.go",
			OldLines:       nil,
			NewLines:       []string{"new"},
			EditType:       "append",
			SequenceNumber: 1,
		},
	},
}

var missingDividerCreateFile = EditBlockTestCase{
	name: "Missing divider when creating a new file",
	testInput: `New file:

` + "```" + `go
edit_block:1
new.go
<<<<<<< CREATE_FILE
new
>>>>>>> NEW_LINES
` + "```" + `
`,
	expectedResult: []*EditBlock{
		{
			FilePath:       "new.go",
			OldLines:       nil,
			NewLines:       []string{"new"},
			EditType:       "create",
			SequenceNumber: 1,
		},
	},
}

var multipleEditsInSameFile = EditBlockTestCase{
	name: "Multiple edits in the same file",
	testInput: `This has multiple edits in the same file:

` + "```" + `go
edit_block:1
file1.go
` + search + `
a
` + divider + `
b
` + replace + `

edit_block:2
file1.go
` + search + `
c
` + divider + `
d
` + replace + `
` + "```" + `
`,
	expectedResult: []*EditBlock{
		{
			FilePath:       "file1.go",
			OldLines:       []string{"a"},
			NewLines:       []string{"b"},
			EditType:       "update",
			SequenceNumber: 1,
		},
		{
			FilePath:       "file1.go",
			OldLines:       []string{"c"},
			NewLines:       []string{"d"},
			EditType:       "update",
			SequenceNumber: 2,
		},
	},
}

var multipleEditsInSameFile2 = EditBlockTestCase{
	name: "Multiple edits in the same file, but second edit is missing file name so we assume it's the first one",
	testInput: `This has multiple edits in the same file:

` + "```" + `go
edit_block:1
file2.go
` + search + `
a
` + divider + `
b
` + replace + `

someOtherCode()

edit_block:2
` + search + `
c
` + divider + `
d
` + replace + `
` + "```" + `
`,
	expectedResult: []*EditBlock{
		{
			FilePath:       "file2.go",
			OldLines:       []string{"a"},
			NewLines:       []string{"b"},
			EditType:       "update",
			SequenceNumber: 1,
		},
		{
			FilePath:       "file2.go",
			OldLines:       []string{"c"},
			NewLines:       []string{"d"},
			EditType:       "update",
			SequenceNumber: 2,
		},
	},
}

func TestExtractEditBlocks(t *testing.T) {
	testCases := []EditBlockTestCase{
		basicCase,
		extraContent,
		missingFileNameAndSequenceNumber,
		missingFileName,
		missingSequenceNumber,
		// TODO: add missingDivider test case
		newFile,
		appendFile,
		missingDividerAppendFile,
		missingDividerCreateFile,
		multipleEditsInSameFile,
		multipleEditsInSameFile2,
	}

	combinedTestInput := ""
	combinedExpectedResult := []*EditBlock{}
	for _, testCase := range testCases {
		combinedTestInput += testCase.testInput + "\n"
		combinedExpectedResult = append(combinedExpectedResult, testCase.expectedResult...)

		t.Run(testCase.name, func(t *testing.T) {
			result, err := ExtractEditBlocks(testCase.testInput)
			if err != nil {
				t.Errorf("Error extracting edit blocks: %v", err)
			}

			for i := range testCase.expectedResult {
				if len(result) != len(testCase.expectedResult) {
					t.Errorf("Expected:\n%v\nGot:\n%v", len(testCase.expectedResult), len(result))
				}

				if !reflect.DeepEqual(*result[i], *testCase.expectedResult[i]) {
					t.Errorf("Expected:\n%v\nGot:\n%v", *testCase.expectedResult[i], *result[i])
				}
			}
		})
	}

	// The following test case is a combination of all the test cases above, and
	// makes sure that we don't have any issues dealing with large inputs with
	// many edit blocks within.
	result, err := ExtractEditBlocks(combinedTestInput)
	if err != nil {
		t.Fatalf("Error extracting edit blocks: %v", err)
	}
	for i := range combinedExpectedResult {
		if len(result) <= i {
			t.Errorf("Expected:\n%v\nGot:\n%v", *combinedExpectedResult[i], nil)
			continue
		}
		if !reflect.DeepEqual(*result[i], *combinedExpectedResult[i]) {
			t.Errorf("Expected:\n%v\nGot:\n%v", *combinedExpectedResult[i], *result[i])
		}
	}
}

func TestExtractEditBlocksWithVisibility(t *testing.T) {
	makeSyntheticCodeBlock := func(filePath, content string) tree_sitter.CodeBlock {
		return tree_sitter.CodeBlock{
			FilePath:      filePath,
			Code:          content,
			BlockContent:  "```\n" + content + "\n```",
			FullContent:   "```\n" + content + "\n```",
			StartLine:     -1,
			EndLine:       -1,
			HeaderContent: "",
			Symbol:        "",
		}
	}

	tests := []struct {
		name                              string
		chatHistory                       []llm.ChatMessage
		text                              string
		expectedVisibleCodeBlocksPerBlock [][]tree_sitter.CodeBlock
		expectedVisibleFileRangesPerBlock [][]FileRange
		expectError                       bool
		enableNewVisibilityLogic          bool // Assumed based on prior errors indicating a new boolean flag
		expectedEditBlocks                []EditBlock
		wantErr                           bool
	}{
		{
			name: "Progressive visibility for same file",
			chatHistory: []common.ChatMessage{
				{Content: "```\nedit_block:100\nfileA.go\n<<<<<<< SEARCH_EXACT\nchat_old_A\n=======\nchat_new_A\n>>>>>>> REPLACE_EXACT\n```"},
			},
			text: "```\nedit_block:1\nfileA.go\n<<<<<<< SEARCH_EXACT\neb1_old_A\n=======\neb1_new_A\n>>>>>>> REPLACE_EXACT\n```\n\n" + // EB1 for fileA.go
				"```\nedit_block:2\nfileA.go\n<<<<<<< SEARCH_EXACT\neb2_old_A\n=======\neb2_new_A\n>>>>>>> REPLACE_EXACT\n```\n\n" + // EB2 for fileA.go
				"```\nedit_block:3\nfileB.go\n<<<<<<< SEARCH_EXACT\neb3_old_B\n=======\neb3_new_B\n>>>>>>> REPLACE_EXACT\n```", // EB3 for fileB.go
			enableNewVisibilityLogic: true,
			expectedEditBlocks: []EditBlock{
				{ // EB1 for fileA.go
					FilePath:       "fileA.go",
					OldLines:       []string{"eb1_old_A"},
					NewLines:       []string{"eb1_new_A"},
					EditType:       "update",
					SequenceNumber: 1,
					VisibleCodeBlocks: []tree_sitter.CodeBlock{
						makeSyntheticCodeBlock("fileA.go", "chat_old_A"),
						makeSyntheticCodeBlock("fileA.go", "chat_new_A"),
					},
					VisibleFileRanges: []FileRange{}, // Assuming synthetic chat history blocks (-1 lines) don't create ranges
				},
				{ // EB2 for fileA.go
					FilePath:       "fileA.go",
					OldLines:       []string{"eb2_old_A"},
					NewLines:       []string{"eb2_new_A"},
					EditType:       "update",
					SequenceNumber: 2,
					VisibleCodeBlocks: []tree_sitter.CodeBlock{
						makeSyntheticCodeBlock("fileA.go", "chat_old_A"),
						makeSyntheticCodeBlock("fileA.go", "chat_new_A"),
						makeSyntheticCodeBlock("fileA.go", "eb1_old_A"),
						makeSyntheticCodeBlock("fileA.go", "eb1_new_A"),
					},
					VisibleFileRanges: []FileRange{}, // Still based only on chat history for fileA.go
				},
				{ // EB3 for fileB.go
					FilePath:          "fileB.go",
					OldLines:          []string{"eb3_old_B"},
					NewLines:          []string{"eb3_new_B"},
					EditType:          "update",
					SequenceNumber:    3,
					VisibleCodeBlocks: nil,
					VisibleFileRanges: []FileRange{}, // No chat history for fileB.go
				},
			},
			wantErr: false,
		},

		{
			name: "Created files have full file visibility range",
			chatHistory: []common.ChatMessage{},
			text: "```\nedit_block:1\nfileA.go\n<<<<<<< CREATE_FILE\n=======\nline1\n>>>>>>> REPLACE_EXACT\n```\n\n" + 
				"```\nedit_block:2\nfileA.go\n<<<<<<< SEARCH_EXACT\nline1\n=======\nline1b\n>>>>>>> REPLACE_EXACT\n```",
			enableNewVisibilityLogic: true,
			expectedEditBlocks: []EditBlock{
				{ // EB1 for fileA.go
					FilePath:       "fileA.go",
					NewLines:       []string{"line1"},
					EditType:       "create",
					SequenceNumber: 1,
					VisibleCodeBlocks: nil,
					VisibleFileRanges: []FileRange{},
				},
				{ // EB2 for fileA.go
					FilePath:       "fileA.go",
					OldLines:       []string{"line1"},
					NewLines:       []string{"line1b"},
					EditType:       "update",
					SequenceNumber: 2,
					VisibleCodeBlocks: []tree_sitter.CodeBlock{
						{
							FilePath:      "fileA.go",
							Code:          "line1",
							BlockContent:  "```\nline1\n```",
							FullContent:   "```\nline1\n```",
							StartLine:     1,
							EndLine:       1,
						},
					},
					VisibleFileRanges: []FileRange{
						{FilePath: "fileA.go", StartLine: 1, EndLine: 1},
					},
				},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Assuming ExtractEditBlocksWithVisibility now takes a third boolean argument
			// based on previous compilation errors and user guidance.
			gotEditBlocks, err := ExtractEditBlocksWithVisibility(tt.text, tt.chatHistory, tt.enableNewVisibilityLogic)
			if (err != nil) != tt.wantErr {
				t.Errorf("ExtractEditBlocksWithVisibility() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if len(gotEditBlocks) != len(tt.expectedEditBlocks) {
				t.Logf("Got %d edit blocks, want %d", len(gotEditBlocks), len(tt.expectedEditBlocks))
				for i, eb := range gotEditBlocks {
					t.Logf("Got EB %d: %+v", i, eb)
					t.Logf("Got EB %d VCBs: %+v", i, eb.VisibleCodeBlocks)
				}
				for i, eb := range tt.expectedEditBlocks {
					t.Logf("Want EB %d: %+v", i, eb)
					t.Logf("Want EB %d VCBs: %+v", i, eb.VisibleCodeBlocks)
				}
				t.Fatalf("ExtractEditBlocksWithVisibility() length mismatch")
			}

			for i := range tt.expectedEditBlocks {
				expected := tt.expectedEditBlocks[i]
				actual := gotEditBlocks[i]

				// Compare basic fields that ExtractEditBlocks would populate
				if actual.FilePath != expected.FilePath ||
					!reflect.DeepEqual(actual.OldLines, expected.OldLines) ||
					!reflect.DeepEqual(actual.NewLines, expected.NewLines) ||
					actual.EditType != expected.EditType ||
					actual.SequenceNumber != expected.SequenceNumber {
					t.Errorf("EditBlock %d basic fields mismatch.\nExpected: %+v\nActual:   %+v", i, expected, actual)
				}

				// Compare VisibleCodeBlocks
				if !reflect.DeepEqual(actual.VisibleCodeBlocks, expected.VisibleCodeBlocks) {
					// Provide more detailed diff for VCBs
					t.Errorf("EditBlock %d VisibleCodeBlocks mismatch.", i)
					if len(actual.VisibleCodeBlocks) != len(expected.VisibleCodeBlocks) {
						t.Errorf("  Length mismatch: got %d, want %d", len(actual.VisibleCodeBlocks), len(expected.VisibleCodeBlocks))
					}
					for j := 0; j < len(actual.VisibleCodeBlocks); j++ {
						t.Errorf("Expected:\n%v\nGot:\n%v", expected.VisibleCodeBlocks[j], actual.VisibleCodeBlocks[j])
					}
				}

				// Compare VisibleFileRanges
				if !reflect.DeepEqual(actual.VisibleFileRanges, expected.VisibleFileRanges) {
					t.Errorf("EditBlock %d VisibleFileRanges mismatch.\nExpected: %+v\nActual:   %+v", i, expected.VisibleFileRanges, actual.VisibleFileRanges)
				}
			}
		})
	}
}
