package dev

import (
	"reflect"
	"testing"
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

var fourBacktickFenceWithTripleInside = EditBlockTestCase{
	name: "Four backtick fence with triple backticks inside content",
	testInput: `Edit block with nested code:

` + "````" + `markdown
edit_block:1
nested.md
` + search + `
Some text before.
` + divider + `
Some text before.

` + "```" + `javascript
var x = 1;
` + "```" + `

Some text after.
` + replace + `
` + "````" + `
`,
	expectedResult: []*EditBlock{
		{
			FilePath: "nested.md",
			OldLines: []string{
				"Some text before.",
			},
			NewLines: []string{
				"Some text before.",
				"",
				"```javascript",
				"var x = 1;",
				"```",
				"",
				"Some text after.",
			},
			EditType:       "update",
			SequenceNumber: 1,
		},
	},
}

var fiveBacktickFenceWithInfoString = EditBlockTestCase{
	name: "Five backtick fence with four backticks inside content",
	testInput: `Edit block with five backtick fence:

` + "`````" + `markdown
edit_block:1
doc.md
` + search + `
Header text.
` + divider + `
Header text.

` + "````" + `python
def hello():
    print("world")
` + "````" + `

Footer text.
` + replace + `
` + "`````" + `
`,
	expectedResult: []*EditBlock{
		{
			FilePath: "doc.md",
			OldLines: []string{"Header text."},
			NewLines: []string{
				"Header text.",
				"",
				"````python",
				"def hello():",
				"    print(\"world\")",
				"````",
				"",
				"Footer text.",
			},
			EditType:       "update",
			SequenceNumber: 1,
		},
	},
}

var mixedFenceLengths = EditBlockTestCase{
	name: "Mixed fence lengths in single input",
	testInput: `Multiple blocks with different fence lengths:

` + "```" + `go
edit_block:1
file1.go
` + search + `
a
` + divider + `
b
` + replace + `
` + "```" + `

` + "````" + `python
edit_block:2
file2.py
` + search + `
x
` + divider + `
y
` + replace + `
` + "````" + `

` + "`````" + `javascript
edit_block:3
file3.js
` + search + `
foo
` + divider + `
bar
` + replace + `
` + "`````" + `
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
			FilePath:       "file2.py",
			OldLines:       []string{"x"},
			NewLines:       []string{"y"},
			EditType:       "update",
			SequenceNumber: 2,
		},
		{
			FilePath:       "file3.js",
			OldLines:       []string{"foo"},
			NewLines:       []string{"bar"},
			EditType:       "update",
			SequenceNumber: 3,
		},
	},
}

var standardTripleBacktickRegression = EditBlockTestCase{
	name: "Standard triple backtick regression test",
	testInput: `Standard triple backtick fence:

` + "```" + `
edit_block:1
standard.go
` + search + `
original
` + divider + `
modified
` + replace + `
` + "```" + `
`,
	expectedResult: []*EditBlock{
		{
			FilePath:       "standard.go",
			OldLines:       []string{"original"},
			NewLines:       []string{"modified"},
			EditType:       "update",
			SequenceNumber: 1,
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
	t.Parallel()
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
		fourBacktickFenceWithTripleInside,
		fiveBacktickFenceWithInfoString,
		mixedFenceLengths,
		standardTripleBacktickRegression,
	}

	combinedTestInput := ""
	combinedExpectedResult := []*EditBlock{}
	for _, testCase := range testCases {
		combinedTestInput += testCase.testInput + "\n"
		combinedExpectedResult = append(combinedExpectedResult, testCase.expectedResult...)

		tc := testCase
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			result, err := ExtractEditBlocks(tc.testInput, false)
			if err != nil {
				t.Errorf("Error extracting edit blocks: %v", err)
			}

			for i := range tc.expectedResult {
				if len(result) != len(tc.expectedResult) {
					t.Errorf("Expected:\n%v\nGot:\n%v", len(tc.expectedResult), len(result))
				}

				if !reflect.DeepEqual(*result[i], *tc.expectedResult[i]) {
					t.Errorf("Expected:\n%v\nGot:\n%v", *tc.expectedResult[i], *result[i])
				}
			}
		})
	}

	// The following test case is a combination of all the test cases above, and
	// makes sure that we don't have any issues dealing with large inputs with
	// many edit blocks within.
	result, err := ExtractEditBlocks(combinedTestInput, false)
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

// Tilde-based test cases

var tildeBasicCase = EditBlockTestCase{
	name: "Tilde basic edit block",
	testInput: ` This is a basic edit block:

~~~~go
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
~~~~
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

var tildeExtraContent = EditBlockTestCase{
	name: "Tilde extra content in the code fence",
	testInput: `
~~~~go
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
~~~~
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

var tildeMissingFileNameAndSequenceNumber = EditBlockTestCase{
	name: "Tilde missing File Name and Sequence Number",
	testInput: `This is missing the filename and sequence number:

~~~~go
` + search + `
stuff
` + divider + `
more stuff
` + replace + `
~~~~
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

var tildeMissingFileName = EditBlockTestCase{
	name: "Tilde missing File Name",
	testInput: `This is missing the filename:

~~~~go
edit_block:1
` + search + `
stuff
` + divider + `
more stuff
` + replace + `
~~~~
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

var tildeMissingSequenceNumber = EditBlockTestCase{
	name: "Tilde missing Sequence Number",
	testInput: `This is missing the filename and sequence number:

~~~~go
omg.go
` + search + `
stuff
` + divider + `
more stuff
` + replace + `
~~~~
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

var tildeNewFile = EditBlockTestCase{
	name:      "Tilde valid edit to create a new file",
	testInput: "~~~~go\nedit_block:1\nnew.go\n<<<<<<< CREATE_FILE\n" + divider + "\nnew\n>>>>>>> NEW_LINES\n~~~~\n",
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

var tildeAppendFile = EditBlockTestCase{
	name:      "Tilde valid edit to append to an existing File",
	testInput: "~~~~go\nedit_block:1\nexisting.go\n<<<<<<< APPEND_TO_FILE\n" + divider + "\nnew\n>>>>>>> NEW_LINES\n~~~~\n",
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

var tildeMissingDividerAppendFile = EditBlockTestCase{
	name:      "Tilde missing divider when appending to an existing File",
	testInput: "~~~~go\nedit_block:1\nexisting.go\n<<<<<<< APPEND_TO_FILE\nnew\n>>>>>>> NEW_LINES\n~~~~\n",
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

var tildeMissingDividerCreateFile = EditBlockTestCase{
	name:      "Tilde missing divider when creating a new file",
	testInput: "~~~~go\nedit_block:1\nnew.go\n<<<<<<< CREATE_FILE\nnew\n>>>>>>> NEW_LINES\n~~~~\n",
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

var tildeMultipleEditsInSameFile = EditBlockTestCase{
	name:      "Tilde multiple edits in the same file",
	testInput: "~~~~go\nedit_block:1\nfile1.go\n" + search + "\na\n" + divider + "\nb\n" + replace + "\n\nedit_block:2\nfile1.go\n" + search + "\nc\n" + divider + "\nd\n" + replace + "\n~~~~\n",
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

var tildeMultipleEditsInSameFile2 = EditBlockTestCase{
	name:      "Tilde multiple edits in the same file, second edit missing file name",
	testInput: "~~~~go\nedit_block:1\nfile2.go\n" + search + "\na\n" + divider + "\nb\n" + replace + "\n\nsomeOtherCode()\n\nedit_block:2\n" + search + "\nc\n" + divider + "\nd\n" + replace + "\n~~~~\n",
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

var fiveTildeFenceWithFourInside = EditBlockTestCase{
	name:      "Five tilde fence with four tildes inside content",
	testInput: "~~~~~markdown\nedit_block:1\ndoc.md\n" + search + "\nHeader text.\n" + divider + "\nHeader text.\n\n~~~~python\ndef hello():\n    print(\"world\")\n~~~~\n\nFooter text.\n" + replace + "\n~~~~~\n",
	expectedResult: []*EditBlock{
		{
			FilePath: "doc.md",
			OldLines: []string{"Header text."},
			NewLines: []string{
				"Header text.",
				"",
				"~~~~python",
				"def hello():",
				"    print(\"world\")",
				"~~~~",
				"",
				"Footer text.",
			},
			EditType:       "update",
			SequenceNumber: 1,
		},
	},
}

var fourTildeFenceWithTripleInside = EditBlockTestCase{
	name:      "Four tilde fence with triple tildes inside content",
	testInput: "~~~~markdown\nedit_block:1\nnested.md\n" + search + "\nSome text before.\n" + divider + "\nSome text before.\n\n~~~javascript\nvar x = 1;\n~~~\n\nSome text after.\n" + replace + "\n~~~~\n",
	expectedResult: []*EditBlock{
		{
			FilePath: "nested.md",
			OldLines: []string{"Some text before."},
			NewLines: []string{
				"Some text before.",
				"",
				"~~~javascript",
				"var x = 1;",
				"~~~",
				"",
				"Some text after.",
			},
			EditType:       "update",
			SequenceNumber: 1,
		},
	},
}

var mixedTildeLengths = EditBlockTestCase{
	name:      "Mixed tilde fence lengths in single input",
	testInput: "~~~go\nedit_block:1\nfile1.go\n" + search + "\na\n" + divider + "\nb\n" + replace + "\n~~~\n\n~~~~python\nedit_block:2\nfile2.py\n" + search + "\nx\n" + divider + "\ny\n" + replace + "\n~~~~\n\n~~~~~javascript\nedit_block:3\nfile3.js\n" + search + "\nfoo\n" + divider + "\nbar\n" + replace + "\n~~~~~\n",
	expectedResult: []*EditBlock{
		{
			FilePath:       "file1.go",
			OldLines:       []string{"a"},
			NewLines:       []string{"b"},
			EditType:       "update",
			SequenceNumber: 1,
		},
		{
			FilePath:       "file2.py",
			OldLines:       []string{"x"},
			NewLines:       []string{"y"},
			EditType:       "update",
			SequenceNumber: 2,
		},
		{
			FilePath:       "file3.js",
			OldLines:       []string{"foo"},
			NewLines:       []string{"bar"},
			EditType:       "update",
			SequenceNumber: 3,
		},
	},
}

var tildeStandardTripleTildeRegression = EditBlockTestCase{
	name:      "Standard triple tilde regression test",
	testInput: "~~~\nedit_block:1\nstandard.go\n" + search + "\noriginal\n" + divider + "\nmodified\n" + replace + "\n~~~\n",
	expectedResult: []*EditBlock{
		{
			FilePath:       "standard.go",
			OldLines:       []string{"original"},
			NewLines:       []string{"modified"},
			EditType:       "update",
			SequenceNumber: 1,
		},
	},
}

func TestExtractEditBlocksTildeOnly(t *testing.T) {
	t.Parallel()
	tildeTestCases := []EditBlockTestCase{
		tildeBasicCase,
		tildeExtraContent,
		tildeMissingFileNameAndSequenceNumber,
		tildeMissingFileName,
		tildeMissingSequenceNumber,
		tildeNewFile,
		tildeAppendFile,
		tildeMissingDividerAppendFile,
		tildeMissingDividerCreateFile,
		tildeMultipleEditsInSameFile,
		tildeMultipleEditsInSameFile2,
		fourTildeFenceWithTripleInside,
		fiveTildeFenceWithFourInside,
		mixedTildeLengths,
		tildeStandardTripleTildeRegression,
	}

	combinedTestInput := ""
	combinedExpectedResult := []*EditBlock{}
	for _, testCase := range tildeTestCases {
		combinedTestInput += testCase.testInput + "\n"
		combinedExpectedResult = append(combinedExpectedResult, testCase.expectedResult...)

		tc := testCase
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			result, err := ExtractEditBlocks(tc.testInput, true)
			if err != nil {
				t.Errorf("Error extracting edit blocks: %v", err)
			}

			if len(result) != len(tc.expectedResult) {
				t.Errorf("Expected %d blocks, got %d", len(tc.expectedResult), len(result))
				return
			}

			for i := range tc.expectedResult {
				if !reflect.DeepEqual(*result[i], *tc.expectedResult[i]) {
					t.Errorf("Expected:\n%v\nGot:\n%v", *tc.expectedResult[i], *result[i])
				}
			}
		})
	}

	result, err := ExtractEditBlocks(combinedTestInput, true)
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

func TestExtractEditBlocksTildeOnlyRejectsBackticks(t *testing.T) {
	t.Parallel()
	backtickInput := "```go\nedit_block:1\nfile.go\n" + search + "\na\n" + divider + "\nb\n" + replace + "\n```\n"
	result, err := ExtractEditBlocks(backtickInput, true)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if len(result) != 0 {
		t.Errorf("Expected 0 blocks when tildeOnly=true with backtick input, got %d", len(result))
	}
}

func TestExtractEditBlocksBothFenceTypes(t *testing.T) {
	t.Parallel()
	mixedInput := "~~~~go\nedit_block:1\nfile1.go\n" + search + "\na\n" + divider + "\nb\n" + replace + "\n~~~~\n\n```go\nedit_block:2\nfile2.go\n" + search + "\nx\n" + divider + "\ny\n" + replace + "\n```\n"
	result, err := ExtractEditBlocks(mixedInput, false)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if len(result) != 2 {
		t.Errorf("Expected 2 blocks when tildeOnly=false with mixed input, got %d", len(result))
	}
	if len(result) >= 1 && result[0].FilePath != "file1.go" {
		t.Errorf("Expected first block file path 'file1.go', got '%s'", result[0].FilePath)
	}
	if len(result) >= 2 && result[1].FilePath != "file2.go" {
		t.Errorf("Expected second block file path 'file2.go', got '%s'", result[1].FilePath)
	}
}
