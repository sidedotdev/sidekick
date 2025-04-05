package coding

import (
	"context"
	"os"
	"path/filepath"
	"sidekick/env"
	"strings"
	"testing"

	"sidekick/coding/lsp"
	"sidekick/coding/tree_sitter"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestE2eRelatedSymbolsActivity(t *testing.T) {
	t.Parallel()

	// Create a temporary directory for test files
	tempDir := t.TempDir()

	// Create test files
	createTestFiles(t, tempDir)

	// Create RagActivities with NON-mock LSP and TreeSitter activities (we want to
	// test the real thing)
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

	type symAndFile struct {
		symbol      string
		filePath    string
		inSignature bool
	}
	testCases := []struct {
		name       string
		symbolText string
		filePath   string
		fileRange  *lsp.Range
		expected   []symAndFile
	}{
		{
			name:       "Function using variable",
			symbolText: "varInFile1",
			filePath:   filepath.Join(tempDir, "file1.go"),
			expected: []symAndFile{
				{"funcInFile1", "file1.go", false},
			},
		},
		{
			name:       "Struct used in function",
			symbolText: "StructInFile1",
			filePath:   filepath.Join(tempDir, "file1.go"),
			expected: []symAndFile{
				{"funcInFile1", "file1.go", false},
				{"func2InFile2", "file2.go", true},
			},
		},
		{
			name:       "Constant used in function",
			symbolText: "ConstInFile2",
			filePath:   filepath.Join(tempDir, "file2.go"),
			expected: []symAndFile{
				{"funcInFile2", "file2.go", false},
			},
		},
		{
			name:       "Name overlap",
			symbolText: "Foo",
			filePath:   filepath.Join(tempDir, "file1.go"),
			fileRange:  getRangeMatching(t, file1Content, "type Foo struct {"),
			expected: []symAndFile{
				{"FooBar", "file1.go", true},
				{"Foo.FooBaz", "file1.go", true},
			},
		},
		{
			name:       "Spaces in file path",
			symbolText: "Foo",
			filePath:   filepath.Join(tempDir, "has spaces here", "file space.go"),
			fileRange:  getRangeMatching(t, file1Content, "type Foo struct {"),
			expected: []symAndFile{
				{"FooBar", "file space.go", true},
				{"Foo.FooBaz", "file space.go", true},
			},
		},
	}

	for _, tc := range testCases {
		tc := tc // capture range variable
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			input := RelatedSymbolsActivityInput{
				EnvContainer: env.EnvContainer{
					Env: &env.LocalEnv{WorkingDirectory: filepath.Dir(tc.filePath)},
				},
				RelativeFilePath: filepath.Base(tc.filePath),
				SymbolText:       tc.symbolText,
				SymbolRange:      tc.fileRange,
			}

			result, err := ca.RelatedSymbolsActivity(context.Background(), input)

			require.NoError(t, err)
			assert.Equal(t, len(tc.expected), len(result), "Expected %d related symbols, but got %d", len(tc.expected), len(result))

			for _, expectedSymbol := range tc.expected {
				found := false
				for _, symbol := range result {
					if symbol.Symbol.Content == expectedSymbol.symbol {
						found = true
						assert.Equal(t, expectedSymbol.filePath, symbol.RelativeFilePath, "RelativeFilePath mismatch for %s", expectedSymbol.symbol)
						assert.Equal(t, expectedSymbol.inSignature, symbol.InSignature, "InSignature mismatch for %s", expectedSymbol.symbol)
						break
					}
				}
				assert.True(t, found, "Expected symbol %s not found in results", expectedSymbol.symbol)

				if !found {
					t.Logf("Input: %+v", input)
					t.Logf("Result: %+v", result)
				}
			}
		})
	}
}

var file1Content = `package testpkg

import "fmt"

type StructInFile1 struct {
	Field string
}

var varInFile1 = "test"

func funcInFile1() {
	fmt.Println(varInFile1)
	s := StructInFile1{Field: "test"}
	fmt.Println(s)
}

func main() {
	funcInFile1()
}

func FooBar(f Foo){}
func (f Foo) FooBaz(){}
// Foo is a struct and this comment is a distractor
type Foo struct {}`

var file2Content = `package testpkg

import "fmt"

type InterfaceInFile2 interface {
	Method() string
}

const ConstInFile2 = "constant"

func funcInFile2() {
	fmt.Println(ConstInFile2)
}
	
func func2InFile2(x StructInFile1) {
	fmt.Println(x)
}
`

func createTestFiles(t *testing.T, tempDir string) {
	writeFile(t, filepath.Join(tempDir, "file1.go"), file1Content)
	writeFile(t, filepath.Join(tempDir, "file2.go"), file2Content)
	writeFile(t, filepath.Join(tempDir, "has spaces here", "file space.go"), file1Content)
}

func writeFile(t *testing.T, path, content string) {
	os.MkdirAll(filepath.Dir(path), 0755)
	err := os.WriteFile(path, []byte(content), 0644)
	require.NoError(t, err)
}

func getRangeMatching(t *testing.T, testFile, s string) *lsp.Range {
	lines := strings.Split(testFile, "\n")
	for i, line := range lines {
		if strings.Contains(line, s) {
			return &lsp.Range{
				Start: lsp.Position{Line: i, Character: 0},
				End:   lsp.Position{Line: i, Character: len(line)},
			}
		}
	}

	// If the string is not found, fail the test
	t.Fatalf("String not found in test file: %s", s)
	return nil
}
