package coding

import (
	"fmt"
	"os"
	"path/filepath"
	"sidekick/coding/lsp"
	"sidekick/coding/tree_sitter"
	"sidekick/env"
	"sidekick/utils"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBulkGetSymbolDefinitions(t *testing.T) {
	t.Parallel()

	type testCase struct {
		name           string
		input          []FileSymDefRequest
		expectedOutput SymDefResults
		code           string
		otherCode      string
		fileName       string
		fileExtension  string
	}

	testCases := []testCase{
		{
			name: "Function definition",
			code: `package cools

func TestFunc() {
	println("Hello, world!")
}`,
			input: []FileSymDefRequest{
				{
					SymbolNames: []string{"TestFunc"},
				},
			},
			expectedOutput: SymDefResults{
				SymbolDefinitions: `File: placeholder_tempfile
Symbol: TestFunc
Lines: 1-5
` + "```go" + `
package cools

func TestFunc() {
	println("Hello, world!")
}
` + "```\n\n",
			},
		},
		{
			name: "Receiver Function definition with dot in symbol name",
			code: `package cools

func (*x SomeStruct) TestFunc() {
	println("Hello, world!")
}`,
			input: []FileSymDefRequest{
				{
					SymbolNames: []string{"SomeStruct.TestFunc"},
				},
			},
			expectedOutput: SymDefResults{
				SymbolDefinitions: `File: placeholder_tempfile
Symbol: SomeStruct.TestFunc
Lines: 1-5
` + "```go" + `
package cools

func (*x SomeStruct) TestFunc() {
	println("Hello, world!")
}
` + "```\n\n",
			},
		},
		{
			name: "Pointer Receiver Function definition with star and dot in symbol name",
			code: `package cools

var x = 1

func (*x SomeStruct) TestFunc() {
	println("Hello, world!")
}`,
			input: []FileSymDefRequest{
				{
					SymbolNames: []string{"*SomeStruct.TestFunc"},
				},
			},
			expectedOutput: SymDefResults{
				SymbolDefinitions: `File: placeholder_tempfile
Lines: 1-1
` + "```go" + `
package cools
` + "```" + `

File: placeholder_tempfile
Symbol: *SomeStruct.TestFunc
Lines: 5-7
` + "```go" + `
func (*x SomeStruct) TestFunc() {
	println("Hello, world!")
}
` + "```\n\n",
			},
		},
		{
			name: "Snippet method declaration resolves via normalization",
			code: `package cools

type SomeThing struct{}

func (x SomeThing) SomeMethod() string {
	return "ok"
}`,
			input: []FileSymDefRequest{
				{
					SymbolNames: []string{"func (x SomeThing) SomeMethod() string"},
				},
			},
			expectedOutput: SymDefResults{
				SymbolDefinitions: `File: placeholder_tempfile
Lines: 1-1
` + "```go" + `
package cools
` + "```" + `

File: placeholder_tempfile
Symbol: func (x SomeThing) SomeMethod() string
Lines: 5-7
` + "```go" + `
func (x SomeThing) SomeMethod() string {
	return "ok"
}
` + "```\n\n",
			},
		},
		{
			name: "Dup function definition: adjacent",
			code: `package cools

func TestFunc() {
	println("Hello, world!")
}

func TestFunc() {
	println("Second one")
}`,
			input: []FileSymDefRequest{
				{
					SymbolNames: []string{"TestFunc"},
				},
			},
			expectedOutput: SymDefResults{
				SymbolDefinitions: `File: placeholder_tempfile
Symbol: TestFunc
Lines: 1-9
` + "```go" + `
package cools

func TestFunc() {
	println("Hello, world!")
}

func TestFunc() {
	println("Second one")
}
` + "```" + `

NOTE: Multiple definitions were found for symbol TestFunc`,
			},
		},
		{
			name: "Wildcard * symbol name",
			code: `package cools

const x = 5

func TestFunc() {
	println("Second one")
}`,
			input: []FileSymDefRequest{
				{
					SymbolNames: []string{"*"},
				},
			},
			expectedOutput: SymDefResults{
				SymbolDefinitions: `File: placeholder_tempfile
Lines: 1-7 (full file)
` + "```go" + `
placeholder_full_code
` + "```\n\n",
			},
		},
		{
			name: "Wildcard empty symbol name",
			code: `package cools

const x = 5

func TestFunc() {
	println("Second one")
}`,
			input: []FileSymDefRequest{
				{
					SymbolNames: []string{""},
				},
			},
			expectedOutput: SymDefResults{
				SymbolDefinitions: `File: placeholder_tempfile
Lines: 1-7 (full file)
` + "```go" + `
placeholder_full_code
` + "```\n\n",
			},
		},
		{
			name: "Empty symbol names",
			code: `package cools

const x = 5

func TestFunc() {
	println("Second one")
}`,
			input: []FileSymDefRequest{
				{
					SymbolNames: []string{},
				},
			},
			expectedOutput: SymDefResults{
				SymbolDefinitions: `File: placeholder_tempfile
Lines: 1-7 (full file)
` + "```go" + `
placeholder_full_code
` + "```\n\n",
			},
		},
		{
			name: "Dup function definition: non-adjacent",
			code: `package cools

func TestFunc() {
	println("Hello, world!")
}

const x = 5

func TestFunc() {
	println("Second one")
}`,
			input: []FileSymDefRequest{
				{
					SymbolNames: []string{"TestFunc"},
				},
			},
			expectedOutput: SymDefResults{
				SymbolDefinitions: `File: placeholder_tempfile
Symbol: TestFunc
Lines: 1-5
` + "```go" + `
package cools

func TestFunc() {
	println("Hello, world!")
}
` + "```" + `

File: placeholder_tempfile
Symbol: TestFunc
Lines: 9-11
` + "```go" + `
func TestFunc() {
	println("Second one")
}
` + "```" + `

NOTE: Multiple definitions were found for symbol TestFunc`,
			},
		},
		{
			name: "Symbol non-existent",
			code: `package cools

func TestFunc() {
	println("Hello, world!")
}`,
			input: []FileSymDefRequest{
				{
					SymbolNames: []string{"NonExistentFunc"},
				},
			},
			expectedOutput: SymDefResults{
				SymbolDefinitions: `The file at 'placeholder_tempfile' does not contain the symbol 'NonExistentFunc'. However, it does contain the following symbols: TestFunc
The symbol 'NonExistentFunc' is not defined in any repo files.`,
				Failures: `The file at 'placeholder_tempfile' does not contain the symbol 'NonExistentFunc'. However, it does contain the following symbols: TestFunc
The symbol 'NonExistentFunc' is not defined in any repo files.`,
			},
		},
		{
			name: "Non-existent symbol that is the same as the file name in go",
			code: `package cools

func TestFunc() {
	println("Hello, world!")
}`,
			input: []FileSymDefRequest{
				{
					SymbolNames: []string{"placeholder_without_extension_tempfile"},
				},
			},
			fileExtension: "go",
			expectedOutput: SymDefResults{
				SymbolDefinitions: `The file at 'placeholder_tempfile' does not contain the symbol 'placeholder_without_extension_tempfile'. However, it does contain the following symbols: TestFunc
The symbol 'placeholder_without_extension_tempfile' is not defined in any repo files.`,
				Failures: `The file at 'placeholder_tempfile' does not contain the symbol 'placeholder_without_extension_tempfile'. However, it does contain the following symbols: TestFunc
The symbol 'placeholder_without_extension_tempfile' is not defined in any repo files.`,
			},
		},
		{
			name: "Non-existent symbol that is the same as the file name in vue",
			code: `<template><div>Hello, Vue 3!</div></template>

<script>
export default {
  name: 'MyComponent'
}
</script>`,
			input: []FileSymDefRequest{
				{
					SymbolNames: []string{"placeholder_without_extension_tempfile"},
				},
			},
			fileExtension: "vue",
			expectedOutput: SymDefResults{
				SymbolDefinitions: `File: placeholder_tempfile
Lines: 1-7 (full file)
` + "```vue" + `
placeholder_full_code
` + "```",
			},
		},
		{
			name: "Symbol in different file",
			code: `package cools

func WontExistHere() {
	println("Hello, world!")
}`,
			otherCode: `package cools

func ExistsElsewhere() {
	println("Hello, world!")
}`,
			input: []FileSymDefRequest{
				{
					SymbolNames: []string{"ExistsElsewhere"},
				},
			},
			expectedOutput: SymDefResults{
				SymbolDefinitions: `The file at 'placeholder_tempfile' does not contain the symbol 'ExistsElsewhere'. However, it does contain the following symbols: WontExistHere
The symbol 'ExistsElsewhere' is defined in the following files:
  - placeholder_other_tempfile`,
				Failures: `The file at 'placeholder_tempfile' does not contain the symbol 'ExistsElsewhere'. However, it does contain the following symbols: WontExistHere
The symbol 'ExistsElsewhere' is defined in the following files:
  - placeholder_other_tempfile`,
			},
		},
		{
			name: "Non-existent file (code not specified)",
			input: []FileSymDefRequest{
				{
					SymbolNames: []string{"TestFunc"},
					FilePath:    "nonexistent.go",
				},
			},
			expectedOutput: SymDefResults{
				SymbolDefinitions: `No file at 'nonexistent.go' exists in the repository. Please check the file path and try again.
The symbol 'TestFunc' is not defined in any repo files.`,
				Failures: `No file at 'nonexistent.go' exists in the repository. Please check the file path and try again.
The symbol 'TestFunc' is not defined in any repo files.`,
			},
		},
		{
			name:          "Unknown file extension, file exists (code specified)",
			code:          `not really go code, not important what it is, just need to make the file exist`,
			fileExtension: "unknown",
			input: []FileSymDefRequest{
				{
					SymbolNames: []string{"NonExistentFunc"},
				},
			},
			expectedOutput: SymDefResults{
				SymbolDefinitions: `failed to infer language: placeholder_tempfile
The symbol 'NonExistentFunc' is not defined in any repo files.`,
				Failures: `failed to infer language: placeholder_tempfile
The symbol 'NonExistentFunc' is not defined in any repo files.`,
			},
		},
		{
			name:          "Unknown file extension, file is not defined in any repo filescified)",
			fileExtension: "unknown",
			input: []FileSymDefRequest{
				{
					SymbolNames: []string{"TestFunc"},
					FilePath:    "nonexistent.ext",
				},
			},
			expectedOutput: SymDefResults{
				SymbolDefinitions: `No file at 'nonexistent.ext' exists in the repository. Please check the file path and try again.
The symbol 'TestFunc' is not defined in any repo files.`,
				Failures: `No file at 'nonexistent.ext' exists in the repository. Please check the file path and try again.
The symbol 'TestFunc' is not defined in any repo files.`,
			},
		},
		{
			name: "multiple import statements",
			code: `package cools

import "fmt"

var x = 1

func TestFunc() {
	println("Hello, world!")
}

var y = 1

import "os"`,
			input: []FileSymDefRequest{
				{
					SymbolNames: []string{"TestFunc"},
				},
			},
			expectedOutput: SymDefResults{
				SymbolDefinitions: `File: placeholder_tempfile
Lines: 1-3
` + "```go" + `
package cools

import "fmt"
` + "```" + `

File: placeholder_tempfile
Lines: 13-13
` + "```go" + `
import "os"
` + "```" + `

File: placeholder_tempfile
Symbol: TestFunc
Lines: 7-9
` + "```go" + `
func TestFunc() {
	println("Hello, world!")
}
` + "```\n\n",
			},
		},
		{
			name: "merge whitespace-separated functions",
			code: `package cools

func FirstFunc() {
	println("First")
}

			
  
func SecondFunc() {
	println("Second")
}`,
			input: []FileSymDefRequest{
				{
					SymbolNames: []string{"FirstFunc", "SecondFunc"},
				},
			},
			expectedOutput: SymDefResults{
				SymbolDefinitions: `File: placeholder_tempfile
Symbols: FirstFunc, SecondFunc
Lines: 1-11
` + "```go" + `
package cools

func FirstFunc() {
	println("First")
}

			
  
func SecondFunc() {
	println("Second")
}
` + "```\n\n",
			},
		},
		{
			name: "no merge for adjacent functions with non-whitespace between",
			code: `package cools

func FirstFunc() {
	println("First")
}

var foo = 123

func SecondFunc() {
	println("Second")
}`,
			input: []FileSymDefRequest{
				{
					SymbolNames: []string{"FirstFunc", "SecondFunc"},
				},
			},
			expectedOutput: SymDefResults{
				SymbolDefinitions: `File: placeholder_tempfile
Symbol: FirstFunc
Lines: 1-5
` + "```go" + `
package cools

func FirstFunc() {
	println("First")
}
` + "```" + `

File: placeholder_tempfile
Symbol: SecondFunc
Lines: 9-11
` + "```go" + `
func SecondFunc() {
	println("Second")
}
` + "```\n\n",
			},
		},
		{
			name: "reorder based on file order",
			code: `package cools

var y = 1

func FirstFunc() {
	println("First")
}

var foo = 123

func SecondFunc() {
	println("Second")
}`,
			input: []FileSymDefRequest{
				{
					SymbolNames: []string{"SecondFunc", "FirstFunc"},
				},
			},
			expectedOutput: SymDefResults{
				SymbolDefinitions: `File: placeholder_tempfile
Lines: 1-1
` + "```go" + `
package cools
` + "```" + `

File: placeholder_tempfile
Symbol: FirstFunc
Lines: 5-7
` + "```go" + `
func FirstFunc() {
	println("First")
}
` + "```" + `

File: placeholder_tempfile
Symbol: SecondFunc
Lines: 11-13
` + "```go" + `
func SecondFunc() {
	println("Second")
}
` + "```\n\n",
			},
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// Create temporary files for the test
			testDir := t.TempDir()
			fileExtension := "go"
			if tc.fileExtension != "" {
				fileExtension = tc.fileExtension
			}
			for i := range tc.input {
				if tc.code != "" {
					fileName := fmt.Sprintf("file%d.%s", i, fileExtension)
					if tc.fileName != "" {
						fileName = tc.fileName
					}
					filePath, err := utils.WriteTestFile(t, testDir, fileName, tc.code)
					if err != nil {
						t.Fatalf("Failed to write temp file: %v", err)
					}

					// Update the file path in the request
					relativePath := filepath.Base(filePath)
					ext := filepath.Ext(relativePath)
					relativeWithoutExt := relativePath[:len(relativePath)-len(ext)]
					tc.input[i].FilePath = relativePath
					tc.input[i].SymbolNames = utils.Map(tc.input[i].SymbolNames, func(s string) string {
						return strings.ReplaceAll(s, "placeholder_without_extension_tempfile", relativeWithoutExt)
					})
					tc.expectedOutput.SymbolDefinitions = strings.ReplaceAll(tc.expectedOutput.SymbolDefinitions, "placeholder_tempfile", relativePath)
					tc.expectedOutput.Failures = strings.ReplaceAll(tc.expectedOutput.Failures, "placeholder_tempfile", relativePath)
					tc.expectedOutput.SymbolDefinitions = strings.ReplaceAll(tc.expectedOutput.SymbolDefinitions, "placeholder_abs_tempfile", filePath)
					tc.expectedOutput.Failures = strings.ReplaceAll(tc.expectedOutput.Failures, "placeholder_abs_tempfile", filePath)
					tc.expectedOutput.SymbolDefinitions = strings.ReplaceAll(tc.expectedOutput.SymbolDefinitions, "placeholder_without_extension_tempfile", relativeWithoutExt)
					tc.expectedOutput.Failures = strings.ReplaceAll(tc.expectedOutput.Failures, "placeholder_without_extension_tempfile", relativeWithoutExt)
					tc.expectedOutput.SymbolDefinitions = strings.ReplaceAll(tc.expectedOutput.SymbolDefinitions, "placeholder_full_code", tc.code)
				}
			}

			if tc.otherCode != "" {
				otherFilePath, err := utils.WriteTestFile(t, testDir, fmt.Sprintf("other_file.%s", fileExtension), tc.otherCode)
				if err != nil {
					t.Fatalf("Failed to write temp file: %v", err)
				}
				tc.expectedOutput.SymbolDefinitions = strings.ReplaceAll(tc.expectedOutput.SymbolDefinitions, "placeholder_other_tempfile", filepath.Base(otherFilePath))
				tc.expectedOutput.Failures = strings.ReplaceAll(tc.expectedOutput.Failures, "placeholder_other_tempfile", filepath.Base(otherFilePath))
			}

			ca := &CodingActivities{}

			// Call the method under test
			numLines := 0
			dirSymDefRequest := DirectorySymDefRequest{
				EnvContainer: env.EnvContainer{
					Env: &env.LocalEnv{WorkingDirectory: testDir},
				},
				Requests:        tc.input,
				NumContextLines: &numLines,
			}
			output, err := ca.BulkGetSymbolDefinitions(t.Context(), dirSymDefRequest)
			assert.Nil(t, err)

			// Compare the output with the expected output
			if strings.TrimSpace(output.SymbolDefinitions) != strings.TrimSpace(tc.expectedOutput.SymbolDefinitions) {
				//t.Errorf("Expected symdef:\n%s\nGot got symdef:\n%s", utils.PanicJSON(tc.expectedOutput.SymbolDefinitions), utils.PanicJSON(output.SymbolDefinitions))
				t.Errorf("Expected symdef str:\n%s\nGot symdef str:\n%s", strings.TrimSpace(tc.expectedOutput.SymbolDefinitions), strings.TrimSpace(output.SymbolDefinitions))
			} else if strings.TrimSpace(output.Failures) != strings.TrimSpace(tc.expectedOutput.Failures) {
				t.Errorf("Expected failures %s, got %s", utils.PanicJSON(tc.expectedOutput.Failures), utils.PanicJSON(output.Failures))
			}
		})
	}
}

func TestGetHintForNonExistentFile(t *testing.T) {
	t.Parallel()
	// Define test cases
	tests := []struct {
		name                    string
		nonExistentRelativePath string
		setupFiles              []string
		expectedHint            string
	}{
		{
			name:                    "No other files",
			nonExistentRelativePath: "nonexistent.txt",
			setupFiles:              []string{},
			expectedHint:            "No file at 'nonexistent.txt' exists in the repository. Please check the file path and try again.",
		},
		{
			name:                    "No other files + nested directory",
			nonExistentRelativePath: filepath.Join("nested", "nonexistent.txt"),
			setupFiles:              []string{},
			expectedHint:            "No file at 'nested/nonexistent.txt' exists in the repository. Please check the file path and try again.",
		},
		{
			name:                    "Too many similar files",
			nonExistentRelativePath: filepath.Join("similar", "nonexistent.txt"),
			setupFiles: []string{
				"similar/file1.txt",
				"similar/file2.txt",
				"similar/file3.txt",
				"similar/file4.txt",
			},
			expectedHint: "No file at 'similar/nonexistent.txt' exists in the repository. Did you mean one of the following?:\n" +
				"similar/file1.txt\nsimilar/file2.txt\nsimilar/file3.txt",
		},
		{
			name:                    "Some similar files, some dissimilar",
			nonExistentRelativePath: filepath.Join("similar", "nonexistent.txt"),
			setupFiles: []string{
				"similar/file1.txt",
				"similar/file2.txt",
				"dissimilar/file3.txt",
				"dissimilar/file4.txt",
			},
			expectedHint: "No file at 'similar/nonexistent.txt' exists in the repository. Did you mean one of the following?:\n" +
				"similar/file1.txt\nsimilar/file2.txt",
		},
		{
			name:                    "wrong directory for file",
			nonExistentRelativePath: filepath.Join("wrong", "file1.txt"),
			setupFiles: []string{
				"right/file1.txt",
				"right/file2.txt",
			},
			expectedHint: "No file at 'wrong/file1.txt' exists in the repository. Did you mean one of the following?:\n" + "right/file1.txt",
		},
		{
			name:                    "missing directory for nested file",
			nonExistentRelativePath: filepath.Join("nested", "file1.txt"),
			setupFiles: []string{
				"nested/again/file1.txt",
				"nested/again/file2.txt",
			},
			expectedHint: "No file at 'nested/file1.txt' exists in the repository. Did you mean one of the following?:\n" + "nested/again/file1.txt",
		},
		{
			name:                    "multiple with same segment-based ratio sorts by overall string similarity",
			nonExistentRelativePath: filepath.Join("nested", "file1.txt"),
			setupFiles: []string{
				"nested/file0a.txt",
				"nested/file1a.txt",
				"nested/file2a.txt",
				"nested2/file1.txt",
			},
			expectedHint: `No file at 'nested/file1.txt' exists in the repository. Did you mean one of the following?:
nested/file1a.txt
nested2/file1.txt
nested/file0a.txt`,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			// Create a temporary directory for the test
			tmpDir := t.TempDir()

			// Set up files in the temporary directory
			for _, file := range tt.setupFiles {
				filePath := filepath.Join(tmpDir, file)
				if err := os.MkdirAll(filepath.Dir(filePath), 0755); err != nil {
					t.Fatalf("Failed to create directory for %s: %v", filePath, err)
				}
				if _, err := os.Create(filePath); err != nil {
					t.Fatalf("Failed to create file %s: %v", filePath, err)
				}
			}

			// Call the function and check the result
			absolutePath := filepath.Join(tmpDir, tt.nonExistentRelativePath)
			hint := getHintForNonExistentFile(tmpDir, absolutePath)
			if hint != tt.expectedHint {
				t.Errorf("Expected hint %q, but got %q", tt.expectedHint, hint)
			}
		})
	}
}

func TestE2EBulkGetSymbolDefinitionsWithRelatedSymbols(t *testing.T) {
	t.Parallel()

	ca := &CodingActivities{
		LSPActivities: &lsp.LSPActivities{
			LSPClientProvider: func(language string) lsp.LSPClient {
				return &lsp.Jsonrpc2LSPClient{
					LanguageName: language,
				}
			},
			InitializedClients: map[string]lsp.LSPClient{},
		},
		TreeSitterActivities: &tree_sitter.TreeSitterActivities{},
	}

	// Reduced thresholds for the tests
	maxSameFileRelatedSymbols = 3
	maxOtherFilesRelatedSymbols = 2
	maxOtherFiles = 1
	maxSameFileSignatureLines = 2
	maxOtherFileSignatureLines = 1

	testDir := t.TempDir()
	file1, err := utils.WriteTestFile(t, testDir, "file1.go", `package main

// G3 referenced thrice, G2 twice, G1 once, G0 zero times
func G4() {}
func G3() {
	G4()
}
func G2() {
	G4()
}
func G1() {
	G2()
	G3()
	G4()
}
func G0(s string, n int) {
	G2()
	G1()
	G4()
	G4() // call twice
}
var x = G3()
const Y = G3()

// Referenced by file2.go
func H1() {}
func H2() {}
// Referenced by file2.go and file3.go
func H3() {}

// X2 is feferenced here and by file2.go
func X2() {}
func X0() {
	X2()
}

// S2 referenced twice, S1 once, S0 zero times
type S0 struct {
	abc S1
}
type S2 struct {}
type S1 int
func (s S2) M_a() {}
func (s S2) M_b() {}

func FooBar(f Foo){}
func (f Foo) FooBaz(){}
// Foo is a struct and this comment is a distractor
type Foo struct {}
`)
	assert.Nil(t, err)

	_, err = utils.WriteTestFile(t, testDir, "file2.go", `package main

func File2H0() {
	H1()
	H2()
}
func File2H0_b() {
	H2()
	H3()
}

func File2X0() {
	X2()
}
`)
	assert.Nil(t, err)

	_, err = utils.WriteTestFile(t, testDir, "file3.go", `package main

func File3H0() {
	H3()
}
`)
	assert.Nil(t, err)

	testCases := []struct {
		name           string
		filename       string
		symbol         string
		referenceLines []string
		expectedOutput string
	}{
		{
			name:           "Few same-file calls: show signatures",
			filename:       file1,
			symbol:         "G2",
			referenceLines: []string{"\tG2()", "= G2()"},
			expectedOutput: `
G2 is referenced in the same file by:
	func G1()
	func G0(s string, n int)`,
		},
		{
			name:           "More same-file calls: show symbols",
			filename:       file1,
			symbol:         "G3",
			referenceLines: []string{"\tG3()", "= G3()"},
			expectedOutput: `
G3 is referenced in the same file by: G1, x, Y`,
		},
		{
			name:           "Even more same-file calls: show counts",
			filename:       file1,
			symbol:         "G4",
			referenceLines: []string{"\tG4()", "= G4()"},
			expectedOutput: `
G4 is referenced in the same file by 4 other symbols 5 times`,
		},
		{
			name:           "Struct: show method signature",
			filename:       file1,
			symbol:         "S2",
			referenceLines: []string{"\tS2", "func (s S2)"},
			expectedOutput: `
S2 is referenced in the same file by:
	func (s S2) M_a()
	func (s S2) M_b()`,
		},
		{
			name:           "Few calls in other files: show signature",
			filename:       file1,
			symbol:         "H1",
			referenceLines: []string{"\tH1"},
			expectedOutput: `
H1 is referenced in other files:
	file2.go:
		func File2H0()`,
		},
		{
			name:           "More calls in other files: show symbols",
			filename:       file1,
			symbol:         "H2",
			referenceLines: []string{"\tH2"},
			expectedOutput: `
H2 is referenced in other files:
	file2.go: File2H0, File2H0_b`,
		},
		{
			name:           "Too many other files: show stats",
			filename:       file1,
			symbol:         "H3",
			referenceLines: []string{"\tH3"},
			expectedOutput: `
H3 is referenced in 2 other files. Total referencing symbols: 2. Total references: 2`,
		},
		{
			name:           "Both few lines: show signatures for both",
			filename:       file1,
			symbol:         "X2",
			referenceLines: []string{"\tX2"},
			expectedOutput: `
X2 is referenced in the same file by:
	func X0()
X2 is referenced in other files:
	file2.go:
		func File2X0()`,
		},
		{
			name:     "Name overlap",
			filename: file1,
			symbol:   "Foo",
			referenceLines: []string{
				"func FooBar(f Foo){}",
				"func (f Foo) FooBaz(){}",
			},
			expectedOutput: `
Foo is referenced in the same file by:
	func FooBar(f Foo)
	func (f Foo) FooBaz()`,
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			input := []FileSymDefRequest{
				{FilePath: filepath.Base(tc.filename), SymbolNames: []string{tc.symbol}},
			}

			numContextLines := 0
			result, err := ca.BulkGetSymbolDefinitions(t.Context(), DirectorySymDefRequest{
				EnvContainer: env.EnvContainer{
					Env: &env.LocalEnv{WorkingDirectory: filepath.Dir(tc.filename)},
				},
				Requests:              input,
				NumContextLines:       &numContextLines,
				IncludeRelatedSymbols: true,
			})
			assert.Nil(t, err)
			if !strings.Contains(result.SymbolDefinitions, tc.expectedOutput) {
				t.Errorf("Expected to contain:\n%s\nInstead, got:\n%s", tc.expectedOutput, result.SymbolDefinitions)
			}
		})
	}
}

func TestBulkGetSymbolDefinitionsTruncation(t *testing.T) {
	t.Parallel()

	t.Run("single large file exceeding 1MB is truncated with NOTE", func(t *testing.T) {
		t.Parallel()

		testDir := t.TempDir()

		// Create a file larger than 1MB (1024 * 1024 bytes)
		// Using a simple repeating pattern to create large content
		largeContent := strings.Repeat("x", 1024*1024+10000) // ~1MB + 10KB

		filePath := filepath.Join(testDir, "large_file.txt")
		err := os.WriteFile(filePath, []byte(largeContent), 0644)
		assert.Nil(t, err)

		ca := &CodingActivities{}
		numLines := 0

		// Request the file with empty symbol names (wildcard/full file read)
		result, err := ca.BulkGetSymbolDefinitions(t.Context(), DirectorySymDefRequest{
			EnvContainer: env.EnvContainer{
				Env: &env.LocalEnv{WorkingDirectory: testDir},
			},
			Requests: []FileSymDefRequest{
				{FilePath: "large_file.txt", SymbolNames: []string{}},
			},
			NumContextLines: &numLines,
		})
		assert.Nil(t, err)

		// The output should contain a truncation NOTE
		assert.Contains(t, result.SymbolDefinitions, "NOTE:")
		assert.Contains(t, result.SymbolDefinitions, "bytes were truncated from this file's output")

		// The output should be under 1MB
		assert.LessOrEqual(t, len(result.SymbolDefinitions), 1024*1024)
	})

	t.Run("multiple files exceeding 1MB total - largest excluded first", func(t *testing.T) {
		t.Parallel()

		testDir := t.TempDir()

		// Create multiple files that together exceed 1MB
		// File 1: 600KB
		content1 := strings.Repeat("a", 600*1024)
		err := os.WriteFile(filepath.Join(testDir, "file1.txt"), []byte(content1), 0644)
		assert.Nil(t, err)

		// File 2: 600KB (total now 1.2MB, exceeds limit)
		content2 := strings.Repeat("b", 600*1024)
		err = os.WriteFile(filepath.Join(testDir, "file2.txt"), []byte(content2), 0644)
		assert.Nil(t, err)

		ca := &CodingActivities{}
		numLines := 0

		result, err := ca.BulkGetSymbolDefinitions(t.Context(), DirectorySymDefRequest{
			EnvContainer: env.EnvContainer{
				Env: &env.LocalEnv{WorkingDirectory: testDir},
			},
			Requests: []FileSymDefRequest{
				{FilePath: "file1.txt", SymbolNames: []string{}},
				{FilePath: "file2.txt", SymbolNames: []string{}},
			},
			NumContextLines: &numLines,
		})
		assert.Nil(t, err)

		// The output should be under 1MB
		assert.LessOrEqual(t, len(result.SymbolDefinitions), 1024*1024)

		// At least one file should be truncated or have a truncation note
		hasTruncation := strings.Contains(result.SymbolDefinitions, "NOTE:") ||
			strings.Contains(result.SymbolDefinitions, "exceeded 1MB limit")
		assert.True(t, hasTruncation, "Expected truncation note or exclusion message in output")
	})

	t.Run("file completely excluded when too large shows exclusion message", func(t *testing.T) {
		t.Parallel()

		testDir := t.TempDir()

		// Create multiple large files that together far exceed 1MB
		// When one file alone exceeds the limit and there are other files,
		// the largest file gets excluded entirely
		veryLargeContent := strings.Repeat("x", 900*1024) // 900KB
		err := os.WriteFile(filepath.Join(testDir, "huge_file.txt"), []byte(veryLargeContent), 0644)
		assert.Nil(t, err)

		// Create another large file (total now ~1.8MB, well over limit)
		largeContent2 := strings.Repeat("y", 900*1024) // 900KB
		err = os.WriteFile(filepath.Join(testDir, "large_file2.txt"), []byte(largeContent2), 0644)
		assert.Nil(t, err)

		// Create a small file
		smallContent := "small content"
		err = os.WriteFile(filepath.Join(testDir, "small_file.txt"), []byte(smallContent), 0644)
		assert.Nil(t, err)

		ca := &CodingActivities{}
		numLines := 0

		result, err := ca.BulkGetSymbolDefinitions(t.Context(), DirectorySymDefRequest{
			EnvContainer: env.EnvContainer{
				Env: &env.LocalEnv{WorkingDirectory: testDir},
			},
			Requests: []FileSymDefRequest{
				{FilePath: "huge_file.txt", SymbolNames: []string{}},
				{FilePath: "large_file2.txt", SymbolNames: []string{}},
				{FilePath: "small_file.txt", SymbolNames: []string{}},
			},
			NumContextLines: &numLines,
		})
		assert.Nil(t, err)

		// At least one file should be excluded or truncated
		hasExclusionOrTruncation := strings.Contains(result.SymbolDefinitions, "exceeded 1MB limit for a single bulk request") ||
			strings.Contains(result.SymbolDefinitions, "bytes were truncated")
		assert.True(t, hasExclusionOrTruncation, "Expected exclusion or truncation message in output")

		// The small file content should still be present
		assert.Contains(t, result.SymbolDefinitions, "small content")

		// The output should be under 1MB
		assert.LessOrEqual(t, len(result.SymbolDefinitions), 1024*1024)
	})
}

func TestBulkGetSymbolDefinitionsNilNameRange(t *testing.T) {
	t.Parallel()

	ca := &CodingActivities{
		LSPActivities: &lsp.LSPActivities{
			LSPClientProvider: func(language string) lsp.LSPClient {
				return &lsp.Jsonrpc2LSPClient{
					LanguageName: language,
				}
			},
			InitializedClients: map[string]lsp.LSPClient{},
		},
		TreeSitterActivities: &tree_sitter.TreeSitterActivities{},
	}

	testDir := t.TempDir()

	// Markdown headings have nil NameRange
	_, err := utils.WriteTestFile(t, testDir, "readme.md", `# Introduction

This is the intro section.

## Details

Some details here.
`)
	assert.Nil(t, err)

	numContextLines := 0
	result, err := ca.BulkGetSymbolDefinitions(t.Context(), DirectorySymDefRequest{
		EnvContainer: env.EnvContainer{
			Env: &env.LocalEnv{WorkingDirectory: testDir},
		},
		Requests: []FileSymDefRequest{
			{FilePath: "readme.md", SymbolNames: []string{"Introduction"}},
		},
		NumContextLines:       &numContextLines,
		IncludeRelatedSymbols: true,
	})
	assert.Nil(t, err)
	assert.Contains(t, result.SymbolDefinitions, "# Introduction")
}

func TestBulkGetSymbolDefinitionsNoRequests(t *testing.T) {
	t.Parallel()

	ca := &CodingActivities{
		LSPActivities: &lsp.LSPActivities{
			LSPClientProvider: func(language string) lsp.LSPClient {
				return &lsp.Jsonrpc2LSPClient{
					LanguageName: language,
				}
			},
			InitializedClients: map[string]lsp.LSPClient{},
		},
		TreeSitterActivities: &tree_sitter.TreeSitterActivities{},
	}

	result, err := ca.BulkGetSymbolDefinitions(t.Context(), DirectorySymDefRequest{
		EnvContainer: env.EnvContainer{
			Env: &env.LocalEnv{WorkingDirectory: t.TempDir()},
		},
		Requests: []FileSymDefRequest{},
	})
	assert.Nil(t, err)
	assert.Equal(t, "No symbol definition requests were provided.", result.SymbolDefinitions)
	assert.Empty(t, result.Failures)
}

func TestShouldRetrieveFullFile(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		symbols      []string
		absolutePath string
		expected     bool
	}{
		{
			name:         "wildcard star",
			symbols:      []string{"*"},
			absolutePath: "/project/src/main.go",
			expected:     true,
		},
		{
			name:         "wildcard empty string",
			symbols:      []string{""},
			absolutePath: "/project/src/main.go",
			expected:     true,
		},
		{
			name:         "wildcard empty list",
			symbols:      nil,
			absolutePath: "/project/src/main.go",
			expected:     true,
		},
		{
			name:         "vue file with matching component name",
			symbols:      []string{"MyComponent"},
			absolutePath: "/project/src/components/MyComponent.vue",
			expected:     true,
		},
		{
			name:         "vue file with case-insensitive match",
			symbols:      []string{"mycomponent"},
			absolutePath: "/project/src/components/MyComponent.vue",
			expected:     true,
		},
		{
			name:         "vue index file uses parent directory name",
			symbols:      []string{"MyComponent"},
			absolutePath: "/project/src/components/MyComponent/index.vue",
			expected:     true,
		},
		{
			name:         "vue index file case-insensitive match",
			symbols:      []string{"mycomponent"},
			absolutePath: "/project/src/components/MyComponent/index.vue",
			expected:     true,
		},
		{
			name:         "vue index file with hyphenated directory name",
			symbols:      []string{"MyComponent"},
			absolutePath: "/project/src/components/My-Component/index.vue",
			expected:     true,
		},
		{
			name:         "vue index file with underscored directory name",
			symbols:      []string{"MyComponent"},
			absolutePath: "/project/src/components/My_Component/index.vue",
			expected:     true,
		},
		{
			name:         "vue file with non-matching symbol",
			symbols:      []string{"OtherComponent"},
			absolutePath: "/project/src/components/MyComponent.vue",
			expected:     false,
		},
		{
			name:         "vue index file with non-matching symbol",
			symbols:      []string{"OtherComponent"},
			absolutePath: "/project/src/components/MyComponent/index.vue",
			expected:     false,
		},
		{
			name:         "svelte index file not recognized without language support",
			symbols:      []string{"MyWidget"},
			absolutePath: "/project/src/components/MyWidget/index.svelte",
			expected:     false,
		},
		{
			name:         "go file with non-matching symbol",
			symbols:      []string{"SomeFunc"},
			absolutePath: "/project/src/main.go",
			expected:     false,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			result := shouldRetrieveFullFile(tc.symbols, tc.absolutePath)
			assert.Equal(t, tc.expected, result)
		})
	}
}
