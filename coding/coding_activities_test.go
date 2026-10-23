package coding

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sidekick/coding/lsp"
	"sidekick/coding/tree_sitter"
	"sidekick/env"
	"sidekick/utils"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
					Symbols: []RequestedSymbol{{Name: "TestFunc"}},
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
					Symbols: []RequestedSymbol{{Name: "SomeStruct.TestFunc"}},
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
					Symbols: []RequestedSymbol{{Name: "*SomeStruct.TestFunc"}},
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
					Symbols: []RequestedSymbol{{Name: "func (x SomeThing) SomeMethod() string"}},
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
					Symbols: []RequestedSymbol{{Name: "TestFunc"}},
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
					Symbols: []RequestedSymbol{{Name: "*"}},
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
					Symbols: []RequestedSymbol{{Name: ""}},
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
					Symbols: []RequestedSymbol{},
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
					Symbols: []RequestedSymbol{{Name: "TestFunc"}},
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
					Symbols: []RequestedSymbol{{Name: "NonExistentFunc"}},
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
					Symbols: []RequestedSymbol{{Name: "placeholder_without_extension_tempfile"}},
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
					Symbols: []RequestedSymbol{{Name: "placeholder_without_extension_tempfile"}},
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
			name: "Symbol referenced in requested file, defined in another file (resolved via LSP)",
			code: `package cools

func WontExistHere() {
	ExistsElsewhere()
}`,
			otherCode: `package cools

func ExistsElsewhere() {
	println("Hello, world!")
}`,
			input: []FileSymDefRequest{
				{
					Symbols: []RequestedSymbol{{Name: "ExistsElsewhere"}},
				},
			},
			expectedOutput: SymDefResults{
				SymbolDefinitions: `File: placeholder_other_tempfile
Symbol: ExistsElsewhere
Lines: 3-5
` + "```go" + `
func ExistsElsewhere() {
	println("Hello, world!")
}
` + "```\n\n",
			},
		},
		{
			name: "ReferenceLine disambiguates between same-named function and method",
			code: `package cools

func use() {
	Foo()
	B{}.Foo()
}`,
			otherCode: `package cools

func Foo() {}

type B struct{}

func (b B) Foo() {
	println("method")
}`,
			input: []FileSymDefRequest{
				{
					Symbols: []RequestedSymbol{{Name: "Foo", ReferenceLine: "B{}.Foo()"}},
				},
			},
			expectedOutput: SymDefResults{
				SymbolDefinitions: `File: placeholder_other_tempfile
Symbol: Foo
Lines: 7-9
` + "```go" + `
func (b B) Foo() {
	println("method")
}
` + "```\n\n",
			},
		},
		{
			name: "Without ReferenceLine every occurrence resolves to its own definition",
			code: `package cools

func use() {
	Foo()
	B{}.Foo()
}`,
			otherCode: `package cools

func Foo() {}

type B struct{}

func (b B) Foo() {
	println("method")
}`,
			input: []FileSymDefRequest{
				{
					Symbols: []RequestedSymbol{{Name: "Foo"}},
				},
			},
			expectedOutput: SymDefResults{
				SymbolDefinitions: `File: placeholder_other_tempfile
Symbol: Foo
Lines: 3-3
` + "```go" + `
func Foo() {}
` + "```" + `

File: placeholder_other_tempfile
Symbol: Foo
Lines: 7-9
` + "```go" + `
func (b B) Foo() {
	println("method")
}
` + "```\n\n",
			},
		},
		{
			name: "ReferenceLine that does not match errors with occurrence lines",
			code: `package cools

func WontExistHere() {
	ExistsElsewhere()
}`,
			otherCode: `package cools

func ExistsElsewhere() {
	println("Hello, world!")
}`,
			input: []FileSymDefRequest{
				{
					Symbols: []RequestedSymbol{{Name: "ExistsElsewhere", ReferenceLine: "this line is not present anywhere"}},
				},
			},
			expectedOutput: SymDefResults{
				SymbolDefinitions: `reference_line "this line is not present anywhere" did not match any line in placeholder_tempfile; symbol "ExistsElsewhere" occurs on the following lines:
  - line 4: 	ExistsElsewhere()
The file at 'placeholder_tempfile' does not contain the symbol 'ExistsElsewhere'. However, it does contain the following symbols: WontExistHere
The symbol 'ExistsElsewhere' is defined in the following files:
  - placeholder_other_tempfile`,
				Failures: `reference_line "this line is not present anywhere" did not match any line in placeholder_tempfile; symbol "ExistsElsewhere" occurs on the following lines:
  - line 4: 	ExistsElsewhere()
The file at 'placeholder_tempfile' does not contain the symbol 'ExistsElsewhere'. However, it does contain the following symbols: WontExistHere
The symbol 'ExistsElsewhere' is defined in the following files:
  - placeholder_other_tempfile`,
			},
		},
		{
			name: "Symbol referenced but unresolvable via LSP falls back to name-search hint",
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
					Symbols: []RequestedSymbol{{Name: "ExistsElsewhere"}},
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
			name: "Symbol only present as a substring of other identifiers is not resolved via LSP",
			code: `package cools

import "runtime"

const runActivityTaskQueue = "run-activity-script"

func WontExistHere() {
	runtime.Gosched()
	println(runActivityTaskQueue)
}`,
			input: []FileSymDefRequest{
				{
					Symbols: []RequestedSymbol{{Name: "run"}},
				},
			},
			expectedOutput: SymDefResults{
				SymbolDefinitions: `The file at 'placeholder_tempfile' does not contain the symbol 'run'. However, it does contain the following symbols: runActivityTaskQueue, WontExistHere
The symbol 'run' is not defined in any repo files.`,
				Failures: `The file at 'placeholder_tempfile' does not contain the symbol 'run'. However, it does contain the following symbols: runActivityTaskQueue, WontExistHere
The symbol 'run' is not defined in any repo files.`,
			},
		},
		{
			name:          "Symbol in different file - unsupported language degrades to name-search hint",
			fileExtension: "py",
			code: `def use():
    existsElsewhere()
`,
			otherCode: `def existsElsewhere():
    pass
`,
			input: []FileSymDefRequest{
				{
					Symbols: []RequestedSymbol{{Name: "existsElsewhere"}},
				},
			},
			expectedOutput: SymDefResults{
				SymbolDefinitions: `The file at 'placeholder_tempfile' does not contain the symbol 'existsElsewhere'. However, it does contain the following symbols: use
The symbol 'existsElsewhere' is defined in the following files:
  - placeholder_other_tempfile`,
				Failures: `The file at 'placeholder_tempfile' does not contain the symbol 'existsElsewhere'. However, it does contain the following symbols: use
The symbol 'existsElsewhere' is defined in the following files:
  - placeholder_other_tempfile`,
			},
		},
		{
			name: "Non-existent file (code not specified)",
			input: []FileSymDefRequest{
				{
					Symbols:  []RequestedSymbol{{Name: "TestFunc"}},
					FilePath: "nonexistent.go",
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
					Symbols: []RequestedSymbol{{Name: "NonExistentFunc"}},
				},
			},
			expectedOutput: SymDefResults{
				SymbolDefinitions: `failed to infer language: unsupported language: 
The symbol 'NonExistentFunc' is not defined in any repo files.`,
				Failures: `failed to infer language: unsupported language: 
The symbol 'NonExistentFunc' is not defined in any repo files.`,
			},
		},
		{
			name:          "Unknown file extension, file is not defined in any repo filescified)",
			fileExtension: "unknown",
			input: []FileSymDefRequest{
				{
					Symbols:  []RequestedSymbol{{Name: "TestFunc"}},
					FilePath: "nonexistent.ext",
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
					Symbols: []RequestedSymbol{{Name: "TestFunc"}},
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
					Symbols: []RequestedSymbol{{Name: "FirstFunc"}, {Name: "SecondFunc"}},
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
					Symbols: []RequestedSymbol{{Name: "FirstFunc"}, {Name: "SecondFunc"}},
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
					Symbols: []RequestedSymbol{{Name: "SecondFunc"}, {Name: "FirstFunc"}},
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
					for si := range tc.input[i].Symbols {
						tc.input[i].Symbols[si].Name = strings.ReplaceAll(tc.input[i].Symbols[si].Name, "placeholder_without_extension_tempfile", relativeWithoutExt)
					}
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

			ca := &CodingActivities{
				LSPActivities: lsp.NewLSPActivities(
					func(language string) lsp.LSPClient {
						return &lsp.Jsonrpc2LSPClient{
							LanguageName: language,
						}
					}),

				TreeSitterActivities: &tree_sitter.TreeSitterActivities{},
			}

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

// Reproduces the originally reported failure, where requesting symbols absent
// from scripts/run_activity/main.go inlined Go toolchain runtime sources.
func TestBulkGetSymbolDefinitionsRunActivityScriptRegression(t *testing.T) {
	t.Parallel()

	fixture, err := os.ReadFile(filepath.Join("test_files", "run_activity_main.go.txt"))
	require.NoError(t, err)

	testDir := t.TempDir()
	_, err = utils.WriteTestFile(t, testDir, "main.go", string(fixture))
	require.NoError(t, err)

	ca := &CodingActivities{
		LSPActivities: lsp.NewLSPActivities(
			func(language string) lsp.LSPClient {
				return &lsp.Jsonrpc2LSPClient{LanguageName: language}
			}),

		TreeSitterActivities: &tree_sitter.TreeSitterActivities{},
	}

	numLines := 0
	output, err := ca.BulkGetSymbolDefinitions(t.Context(), DirectorySymDefRequest{
		EnvContainer: env.EnvContainer{Env: &env.LocalEnv{WorkingDirectory: testDir}},
		Requests: []FileSymDefRequest{{
			FilePath: "main.go",
			Symbols:  []RequestedSymbol{{Name: "run"}, {Name: "findActivityEvent"}},
		}},
		NumContextLines: &numLines,
	})
	require.NoError(t, err)

	assert.NotContains(t, output.SymbolDefinitions, "```go", "no definition should have been inlined for symbols absent from the file")
	assert.Contains(t, output.Failures, "does not contain the symbol 'run'")
	assert.Contains(t, output.Failures, "does not contain the symbol 'findActivityEvent'")
}

func TestBulkGetSymbolDefinitionsIgnoresUnrelatedLSPDefinition(t *testing.T) {
	t.Parallel()

	testDir := t.TempDir()
	_, err := utils.WriteTestFile(t, testDir, "file0.go", `package cools

func WontExistHere() {
	run()
}`)
	require.NoError(t, err)
	otherFilePath, err := utils.WriteTestFile(t, testDir, "other_file.go", `package cools

func unrelated() {
	println("unrelated")
}`)
	require.NoError(t, err)

	ca := &CodingActivities{
		LSPActivities: lsp.NewLSPActivities(
			func(language string) lsp.LSPClient {
				return lsp.MockLSPClient{

					TextDocumentDefinitionFunc: func(ctx context.Context, uri string, line, character int) ([]lsp.Location, error) {
						return []lsp.Location{{
							URI: "file://" + otherFilePath,
							Range: lsp.Range{
								Start: lsp.Position{Line: 0, Character: 8},
								End:   lsp.Position{Line: 0, Character: 13},
							},
						}}, nil
					},
				}
			}),

		TreeSitterActivities: &tree_sitter.TreeSitterActivities{},
	}

	numLines := 0
	output, err := ca.BulkGetSymbolDefinitions(t.Context(), DirectorySymDefRequest{
		EnvContainer: env.EnvContainer{Env: &env.LocalEnv{WorkingDirectory: testDir}},
		Requests: []FileSymDefRequest{{
			FilePath: "file0.go",
			Symbols:  []RequestedSymbol{{Name: "run"}},
		}},
		NumContextLines: &numLines,
	})
	require.NoError(t, err)

	assert.NotContains(t, output.SymbolDefinitions, "package cools")
	assert.NotContains(t, output.SymbolDefinitions, "other_file.go")
	assert.Contains(t, output.Failures, "does not contain the symbol 'run'")
}

func TestBulkGetSymbolDefinitionsRejectsLSPDefinitionOfDifferentToken(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		otherCode     string
		definedRange  lsp.Range
		unwantedInOut string
	}{
		{
			name: "unrelated token sharing a line with the requested symbol's definition",
			otherCode: `package cools

func run() { runActivity() }

func runActivity() {}`,
			definedRange: lsp.Range{
				Start: lsp.Position{Line: 2, Character: 13},
				End:   lsp.Position{Line: 2, Character: 24},
			},
			unwantedInOut: "func run() {",
		},
		{
			name: "unrelated token nested inside the same-named definition",
			otherCode: `package cools

func run() {
	runCount := 1
	println(runCount)
}`,
			definedRange: lsp.Range{
				Start: lsp.Position{Line: 3, Character: 1},
				End:   lsp.Position{Line: 3, Character: 9},
			},
			unwantedInOut: "func run()",
		},
		{
			name: "range too broad to name the requested symbol",
			otherCode: `package cools

func run() {
	println("run")
}`,
			definedRange: lsp.Range{
				Start: lsp.Position{Line: 2, Character: 0},
				End:   lsp.Position{Line: 4, Character: 1},
			},
			unwantedInOut: "func run()",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			testDir := t.TempDir()
			_, err := utils.WriteTestFile(t, testDir, "file0.go", `package cools

func WontExistHere() {
	run()
}`)
			require.NoError(t, err)
			otherFilePath, err := utils.WriteTestFile(t, testDir, "other_file.go", tc.otherCode)
			require.NoError(t, err)

			ca := &CodingActivities{
				LSPActivities: lsp.NewLSPActivities(
					func(language string) lsp.LSPClient {
						return lsp.MockLSPClient{
							TextDocumentDefinitionFunc: func(ctx context.Context, uri string, line, character int) ([]lsp.Location, error) {
								return []lsp.Location{{
									URI:   "file://" + otherFilePath,
									Range: tc.definedRange,
								}}, nil
							},
						}
					}),

				TreeSitterActivities: &tree_sitter.TreeSitterActivities{},
			}

			numLines := 0
			output, err := ca.BulkGetSymbolDefinitions(t.Context(), DirectorySymDefRequest{
				EnvContainer: env.EnvContainer{Env: &env.LocalEnv{WorkingDirectory: testDir}},
				Requests: []FileSymDefRequest{{
					FilePath: "file0.go",
					Symbols:  []RequestedSymbol{{Name: "run"}},
				}},
				NumContextLines: &numLines,
			})
			require.NoError(t, err)

			assert.NotContains(t, output.SymbolDefinitions, tc.unwantedInOut)
			assert.Contains(t, output.Failures, "does not contain the symbol 'run'")
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
				{FilePath: "large_file.txt", Symbols: []RequestedSymbol{}},
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
				{FilePath: "file1.txt", Symbols: []RequestedSymbol{}},
				{FilePath: "file2.txt", Symbols: []RequestedSymbol{}},
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
				{FilePath: "huge_file.txt", Symbols: []RequestedSymbol{}},
				{FilePath: "large_file2.txt", Symbols: []RequestedSymbol{}},
				{FilePath: "small_file.txt", Symbols: []RequestedSymbol{}},
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
		LSPActivities: lsp.NewLSPActivities(
			func(language string) lsp.LSPClient {
				return &lsp.Jsonrpc2LSPClient{
					LanguageName: language,
				}
			}),

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
			{FilePath: "readme.md", Symbols: []RequestedSymbol{{Name: "Introduction"}}},
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
		LSPActivities: lsp.NewLSPActivities(
			func(language string) lsp.LSPClient {
				return &lsp.Jsonrpc2LSPClient{
					LanguageName: language,
				}
			}),

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

type readRecordingEnv struct {
	env.Env
	mu        sync.Mutex
	readPaths []string
}

func (e *readRecordingEnv) ReadFile(ctx context.Context, path string) ([]byte, error) {
	e.mu.Lock()
	e.readPaths = append(e.readPaths, path)
	e.mu.Unlock()
	return e.Env.ReadFile(ctx, path)
}

// A full-file request for a missing file yields a failure without a symbol
// name; that must not trigger building the repo-wide symbol index, which
// reads every file (very slow on remote envs).
func TestBulkGetSymbolDefinitionsMissingFullFileNoRepoScan(t *testing.T) {
	t.Parallel()

	testDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(testDir, "KanbanBoard.vue"), []byte("<template><div/></template>\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(testDir, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0644))

	recordingEnv := &readRecordingEnv{Env: &env.LocalEnv{WorkingDirectory: testDir}}
	ca := &CodingActivities{}
	result, err := ca.BulkGetSymbolDefinitions(context.Background(), DirectorySymDefRequest{
		EnvContainer: env.EnvContainer{Env: recordingEnv},
		Requests: []FileSymDefRequest{
			{
				FilePath: "Kanban.vue",
				Symbols:  []RequestedSymbol{{Name: "Kanban"}},
			},
		},
	})
	require.NoError(t, err)

	assert.Contains(t, result.Failures, "No file at 'Kanban.vue' exists in the repository")
	assert.Equal(t, 1, strings.Count(result.Failures, "No file at 'Kanban.vue'"), "hint should not be duplicated")
	assert.NotContains(t, result.Failures, "is not defined in any repo files")

	// Only the missing file itself may be read; other repo files must not be
	// scanned to build a symbol index that can't help a symbolless failure.
	recordingEnv.mu.Lock()
	defer recordingEnv.mu.Unlock()
	assert.Equal(t, []string{"Kanban.vue"}, recordingEnv.readPaths)
}

// Remote envs pay several network round trips per ReadFile, so all phases of
// a BulkGetSymbolDefinitions call (symbol lookup, header retrieval, LSP
// fallback, failure hints) must share a single read per file.
func TestBulkGetSymbolDefinitionsReadsEachFileOnce(t *testing.T) {
	t.Parallel()

	testDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(testDir, "main.go"), []byte("package main\n\nfunc Alpha() int { return 1 }\n"), 0644))

	t.Run("found symbol shares read with header retrieval", func(t *testing.T) {
		t.Parallel()
		recordingEnv := &readRecordingEnv{Env: &env.LocalEnv{WorkingDirectory: testDir}}
		ca := &CodingActivities{}
		result, err := ca.BulkGetSymbolDefinitions(context.Background(), DirectorySymDefRequest{
			EnvContainer: env.EnvContainer{Env: recordingEnv},
			Requests: []FileSymDefRequest{
				{FilePath: "main.go", Symbols: []RequestedSymbol{{Name: "Alpha"}}},
			},
		})
		require.NoError(t, err)
		assert.Contains(t, result.SymbolDefinitions, "func Alpha")

		recordingEnv.mu.Lock()
		defer recordingEnv.mu.Unlock()
		assert.Equal(t, []string{"main.go"}, recordingEnv.readPaths)
	})

	t.Run("concurrent duplicate requests for one file coalesce to a single read", func(t *testing.T) {
		t.Parallel()
		recordingEnv := &readRecordingEnv{Env: &env.LocalEnv{WorkingDirectory: testDir}}
		ca := &CodingActivities{}
		result, err := ca.BulkGetSymbolDefinitions(context.Background(), DirectorySymDefRequest{
			EnvContainer: env.EnvContainer{Env: recordingEnv},
			Requests: []FileSymDefRequest{
				{FilePath: "main.go", Symbols: []RequestedSymbol{{Name: "Alpha"}}},
				{FilePath: "main.go", Symbols: []RequestedSymbol{{Name: "Alpha"}}},
			},
		})
		require.NoError(t, err)
		assert.Contains(t, result.SymbolDefinitions, "func Alpha")

		recordingEnv.mu.Lock()
		defer recordingEnv.mu.Unlock()
		assert.Equal(t, []string{"main.go"}, recordingEnv.readPaths)
	})

	t.Run("related symbols and LSP fallback reuse the single read", func(t *testing.T) {
		t.Parallel()
		recordingEnv := &readRecordingEnv{Env: &env.LocalEnv{WorkingDirectory: testDir}}
		mockClient := lsp.MockLSPClient{
			InitializeFunc: func(ctx context.Context, params lsp.InitializeParams) (lsp.InitializeResponse, error) {
				return lsp.InitializeResponse{}, nil
			},
			// A reference pointing at Alpha's own declaration, so RelatedSymbols
			// resolves symbols/signatures for the subject file itself.
			TextDocumentReferencesFunc: func(ctx context.Context, uri string, line, character int) ([]lsp.Location, error) {
				return []lsp.Location{{
					URI: "file://" + filepath.Join(testDir, "main.go"),
					Range: lsp.Range{
						Start: lsp.Position{Line: 2, Character: 5},
						End:   lsp.Position{Line: 2, Character: 10},
					},
				}}, nil
			},
			TextDocumentDefinitionFunc: func(ctx context.Context, uri string, line, character int) ([]lsp.Location, error) {
				return nil, nil
			},
		}
		ca := &CodingActivities{
			LSPActivities: lsp.NewLSPActivities(func(language string) lsp.LSPClient { return mockClient }),
		}
		result, err := ca.BulkGetSymbolDefinitions(context.Background(), DirectorySymDefRequest{
			EnvContainer:          env.EnvContainer{Env: recordingEnv},
			IncludeRelatedSymbols: true,
			Requests: []FileSymDefRequest{
				{FilePath: "main.go", Symbols: []RequestedSymbol{{Name: "Alpha"}, {Name: "Missing"}}},
			},
		})
		require.NoError(t, err)
		assert.Contains(t, result.SymbolDefinitions, "func Alpha")
		assert.Contains(t, result.Failures, "Missing")

		recordingEnv.mu.Lock()
		defer recordingEnv.mu.Unlock()
		assert.Equal(t, []string{"main.go"}, recordingEnv.readPaths)
	})

	t.Run("missing symbol shares read with failure hint probe", func(t *testing.T) {
		t.Parallel()
		recordingEnv := &readRecordingEnv{Env: &env.LocalEnv{WorkingDirectory: testDir}}
		ca := &CodingActivities{}
		result, err := ca.BulkGetSymbolDefinitions(context.Background(), DirectorySymDefRequest{
			EnvContainer: env.EnvContainer{Env: recordingEnv},
			Requests: []FileSymDefRequest{
				{FilePath: "main.go", Symbols: []RequestedSymbol{{Name: "Missing"}}},
			},
		})
		require.NoError(t, err)
		assert.Contains(t, result.Failures, "Missing")

		recordingEnv.mu.Lock()
		defer recordingEnv.mu.Unlock()
		assert.Equal(t, []string{"main.go"}, recordingEnv.readPaths)
	})
}

// Related-symbol lookups for different symbols often share referencing files;
// those reads must coalesce through the bulk invocation's shared read cache
// instead of being repeated per symbol.
func TestBulkGetSymbolDefinitionsRelatedSymbolsShareReads(t *testing.T) {
	t.Parallel()

	testDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(testDir, "main.go"),
		[]byte("package main\n\nfunc Alpha() int { return 1 }\n\nfunc Beta() int { return 2 }\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(testDir, "other.go"),
		[]byte("package main\n\nfunc UseBoth() int { return Alpha() + Beta() }\n"), 0644))

	recordingEnv := &readRecordingEnv{Env: &env.LocalEnv{WorkingDirectory: testDir}}
	mockClient := lsp.MockLSPClient{
		// Both symbols are referenced from the same file, so its read must be
		// shared between the two related-symbol lookups.
		TextDocumentReferencesFunc: func(ctx context.Context, uri string, line, character int) ([]lsp.Location, error) {
			return []lsp.Location{{
				URI: "file://" + filepath.Join(testDir, "other.go"),
				Range: lsp.Range{
					Start: lsp.Position{Line: 2, Character: 5},
					End:   lsp.Position{Line: 2, Character: 12},
				},
			}}, nil
		},
	}
	ca := &CodingActivities{
		LSPActivities: lsp.NewLSPActivities(func(language string) lsp.LSPClient { return mockClient }),
	}
	result, err := ca.BulkGetSymbolDefinitions(context.Background(), DirectorySymDefRequest{
		EnvContainer:          env.EnvContainer{Env: recordingEnv},
		IncludeRelatedSymbols: true,
		Requests: []FileSymDefRequest{
			{FilePath: "main.go", Symbols: []RequestedSymbol{{Name: "Alpha"}, {Name: "Beta"}}},
		},
	})
	require.NoError(t, err)
	assert.Contains(t, result.SymbolDefinitions, "UseBoth")

	recordingEnv.mu.Lock()
	defer recordingEnv.mu.Unlock()
	assert.ElementsMatch(t, []string{"main.go", filepath.Join(testDir, "other.go")}, recordingEnv.readPaths)
}

type cancellationRecordingEnv struct {
	env.Env
	started     chan struct{}
	startedOnce sync.Once
}

func (e *cancellationRecordingEnv) ReadFile(ctx context.Context, path string) ([]byte, error) {
	e.startedOnce.Do(func() {
		close(e.started)
	})
	<-ctx.Done()
	return nil, ctx.Err()
}

func (e *cancellationRecordingEnv) GetType() env.EnvType {
	return env.EnvTypeModal
}

func (e *cancellationRecordingEnv) GetWorkingDirectory() string {
	return "/workspace"
}

func (e *cancellationRecordingEnv) Walk(ctx context.Context, ignoreFileNames []string, handleEntry func(path string, isDir bool) error) error {
	<-ctx.Done()
	return ctx.Err()
}

func TestBulkGetSymbolDefinitionsCancellationUnblocksFileRead(t *testing.T) {
	t.Parallel()

	recordingEnv := &cancellationRecordingEnv{started: make(chan struct{})}
	ctx, cancel := context.WithCancel(t.Context())
	completed := make(chan error, 1)

	go func() {
		_, err := (&CodingActivities{}).BulkGetSymbolDefinitions(ctx, DirectorySymDefRequest{
			EnvContainer: env.EnvContainer{Env: recordingEnv},
			Requests: []FileSymDefRequest{
				{
					FilePath: "example.go",
					Symbols:  []RequestedSymbol{{Name: "Target"}},
				},
			},
		})
		completed <- err
	}()

	select {
	case <-recordingEnv.started:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for the file read to start")
	}

	cancel()

	select {
	case err := <-completed:
		assert.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("BulkGetSymbolDefinitions did not return after cancellation")
	}
}
