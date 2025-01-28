package lsp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sidekick/env"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func getLineContentFromLocation(t *testing.T, loc Location) string {
	filePath := strings.Replace(loc.URI, "file://", "", 1)
	file, err := os.ReadFile(filePath)
	assert.NoError(t, err)
	lines := strings.Split(string(file), "\n")
	rangeLines := lines[loc.Range.Start.Line : loc.Range.End.Line+1]
	return strings.Join(rangeLines, "\n")
}

func TestE2eFindReferencesActivitySingleFile(t *testing.T) {
	t.Parallel()

	// Create a temporary directory for test files
	tempDir := t.TempDir()

	// Define test file content with various symbol types
	testFile := `package testpkg

import "fmt"

type StringEnum string

const (
	ConstA StringEnum = "A"
	ConstB StringEnum = "B"
)

var GlobalVar = "global"

type TestStruct struct {
	Field string
}

type WrapperType TestStruct

func (ts TestStruct) Method() {
	fmt.Println(ts.Field)
}

func RegularFunction(param string) string {
	return param + GlobalVar
}

type TestInterface interface {
	InterfaceMethod() string
}

func (wt WrapperType) InterfaceMethod() string {
	return wt.Field
}

func (f Foo) FooBar(){}
// Foo is a struct and this comment is a distractor
type Foo struct {}

func main() {
	var localVar = "local"
	ts := TestStruct{Field: localVar}
	ts.Method()
	result := RegularFunction(ConstA)
	fmt.Println(result)
	var x TestInterface = WrapperType(ts)
	x.InterfaceMethod()
}
`

	// Write test file to the temporary directory
	testFilePath := filepath.Join(tempDir, "test.go")
	err := os.WriteFile(testFilePath, []byte(testFile), 0644)
	require.NoError(t, err)

	// Create LSPActivities with real Jsonrpc2LSPClient
	lspa := &LSPActivities{
		LSPClientProvider: func(language string) LSPClient {
			return &Jsonrpc2LSPClient{
				LanguageName: "golang",
			}
		},
		InitializedClients: map[string]LSPClient{},
	}

	// Test cases
	testCases := []struct {
		name                   string
		symbolText             string
		expectedReferenceLines []string
		fileRange              *Range
	}{
		{
			name:       "Struct",
			symbolText: "TestStruct",
			expectedReferenceLines: []string{
				"type WrapperType TestStruct",
				"func (ts TestStruct) Method() {",
				"ts := TestStruct{Field: localVar}",
			},
		},
		{
			name:       "Wrapper Type",
			symbolText: "WrapperType",
			expectedReferenceLines: []string{
				"func (wt WrapperType) InterfaceMethod() string {",
				"var x TestInterface = WrapperType(ts)",
			},
		},
		{
			name:                   "Method",
			symbolText:             "Method",
			expectedReferenceLines: []string{"ts.Method()"},
		},
		{
			name:                   "Function",
			symbolText:             "RegularFunction",
			expectedReferenceLines: []string{"result := RegularFunction(ConstA)"},
		},
		{
			name:       "Field",
			symbolText: "Field",
			expectedReferenceLines: []string{
				"fmt.Println(ts.Field)",
				"return wt.Field",
				"ts := TestStruct{Field: localVar}",
			},
		},
		{
			name:                   "Constant",
			symbolText:             "ConstA",
			expectedReferenceLines: []string{"result := RegularFunction(ConstA)"},
		},
		{
			name:                   "Constant that isn't used",
			symbolText:             "ConstB",
			expectedReferenceLines: []string{},
		},
		{
			name:       "String Enum",
			symbolText: "StringEnum",
			expectedReferenceLines: []string{
				"ConstA StringEnum = \"A\"",
				"ConstB StringEnum = \"B\"",
			},
		},
		{
			name:                   "Global Variable",
			symbolText:             "GlobalVar",
			expectedReferenceLines: []string{"return param + GlobalVar"},
		},
		{
			name:                   "Local Variable",
			symbolText:             "localVar",
			expectedReferenceLines: []string{"ts := TestStruct{Field: localVar}"},
		},
		{
			name:                   "Interface",
			symbolText:             "TestInterface",
			expectedReferenceLines: []string{"var x TestInterface = WrapperType(ts)"},
		},
		{
			name:                   "Interface Method",
			symbolText:             "InterfaceMethod",
			expectedReferenceLines: []string{"x.InterfaceMethod()"},
		},
		{
			name:       "Imported Package",
			symbolText: "\"fmt\"",
			expectedReferenceLines: []string{
				"fmt.Println(ts.Field)",
				"fmt.Println(result)",
			},
		},
		{
			name:                   "Name overlap",
			symbolText:             "Foo",
			fileRange:              getRangeMatching(t, testFile, "type Foo struct {"),
			expectedReferenceLines: []string{"func (f Foo) FooBar(){}"},
		},
	}

	for _, tc := range testCases {
		tc := tc // capture range variable for parallel execution
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			input := FindReferencesActivityInput{
				EnvContainer: env.EnvContainer{
					Env: &env.LocalEnv{WorkingDirectory: tempDir},
				},
				RelativeFilePath: filepath.Base(testFilePath),
				SymbolText:       tc.symbolText,
			}
			if tc.fileRange != nil {
				input.Range = tc.fileRange
			}

			result, err := lspa.FindReferencesActivity(context.Background(), input)

			assert.NoError(t, err)
			assert.Equal(t, len(tc.expectedReferenceLines), len(result), "Expected %d references, but got %d for symbol %s", len(tc.expectedReferenceLines), len(result), tc.symbolText)

			// Check if all expected lines were found
			for _, expectedLine := range tc.expectedReferenceLines {
				found := false
				for _, ref := range result {
					lineContent := getLineContentFromLocation(t, ref)
					if strings.Contains(lineContent, expectedLine) {
						found = true
						break
					}
				}
				assert.True(t, found, "Expected reference not found for symbol %s: %s", tc.symbolText, expectedLine)
			}
		})
	}
}

func getRangeMatching(t *testing.T, testFile, s string) *Range {
	lines := strings.Split(testFile, "\n")
	for i, line := range lines {
		if strings.Contains(line, s) {
			return &Range{
				Start: Position{Line: i, Character: 0},
				End:   Position{Line: i, Character: len(line)},
			}
		}
	}

	// If the string is not found, fail the test
	t.Fatalf("String not found in test file: %s", s)
	return nil
}

func TestE2eFindReferencesActivityCrossFile(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()
	// Create two temporary Go files
	file1Path := filepath.Join(tempDir, "file1.go")
	file2Path := filepath.Join(tempDir, "file2.go")

	file1Content := `package main

import "fmt"

type MyStruct struct {}

func (m MyStruct) Method() {
	fmt.Println(GlobalVar)
}

var GlobalVar = "global"
const ConstValue = 42

func main() {
	s := MyStruct{}
	s.Method()
	fmt.Println(ConstValue)
}`

	file2Content := `package main

import "fmt"

func useStuff() {
	s := MyStruct{}
	s.Method()
	fmt.Println(GlobalVar, ConstValue)
}`

	err := os.WriteFile(file1Path, []byte(file1Content), 0644)
	if err != nil {
		t.Fatalf("Failed to write file1: %v", err)
	}

	err = os.WriteFile(file2Path, []byte(file2Content), 0644)
	if err != nil {
		t.Fatalf("Failed to write file2: %v", err)
	}

	// Create LSPActivities with real Jsonrpc2LSPClient
	lspa := &LSPActivities{
		LSPClientProvider: func(language string) LSPClient {
			return &Jsonrpc2LSPClient{
				LanguageName: "golang",
			}
		},
		InitializedClients: map[string]LSPClient{},
	}

	ctx := context.Background()

	testCases := []struct {
		name            string
		file            string
		symbol          string
		numExpectedRefs int
	}{
		{"Struct", file1Path, "MyStruct", 3},
		{"Method", file1Path, "Method", 2},
		{"Global Variable", file1Path, "GlobalVar", 2},
		{"Constant", file1Path, "ConstValue", 2},
		{"Function", file2Path, "useStuff", 0},
		{"Imported Package", file1Path, "fmt", 2},
	}

	for _, tc := range testCases {
		tc := tc // capture range variable for parallel execution
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			input := FindReferencesActivityInput{
				EnvContainer: env.EnvContainer{
					Env: &env.LocalEnv{WorkingDirectory: tempDir},
				},
				RelativeFilePath: filepath.Base(tc.file),
				SymbolText:       tc.symbol,
			}
			references, err := lspa.FindReferencesActivity(ctx, input)
			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}

			if len(references) != tc.numExpectedRefs {
				t.Errorf("Expected %d references, but got %d for symbol %s", tc.numExpectedRefs, len(references), tc.symbol)
			}
		})
	}
}

func TestE2eFindReferencesActivitySpacesInPath(t *testing.T) {
	t.Parallel()
	tempDirWithSpaces := filepath.Join(t.TempDir(), "path with spaces")

	testFilePath := filepath.Join(tempDirWithSpaces, "test file with spaces.go")
	testFile := `package foo

func (f Foo) FooBar(){}
func (f Foo) Omg(){}

type Foo struct {}
`

	os.MkdirAll(tempDirWithSpaces, 0755)
	err := os.WriteFile(testFilePath, []byte(testFile), 0644)
	require.NoError(t, err)

	lspa := &LSPActivities{
		LSPClientProvider: func(language string) LSPClient {
			return &Jsonrpc2LSPClient{
				LanguageName: "golang",
			}
		},
		InitializedClients: map[string]LSPClient{},
	}

	input := FindReferencesActivityInput{
		EnvContainer: env.EnvContainer{
			Env: &env.LocalEnv{WorkingDirectory: tempDirWithSpaces},
		},
		RelativeFilePath: filepath.Base(testFilePath),
		SymbolText:       "Foo",
	}

	result, err := lspa.FindReferencesActivity(context.Background(), input)
	fmt.Printf("Result: %v\n", result)
	require.NoError(t, err)
	require.Len(t, result, 2)
}
