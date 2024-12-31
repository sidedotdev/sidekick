package tree_sitter

import (
	"os"
	"sidekick/utils"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetFileSignaturesStringGolang(t *testing.T) {
	testCases := []struct {
		name     string
		code     string
		expected string
	}{
		{
			name:     "empty",
			code:     "",
			expected: "",
		},
		{
			name:     "single function",
			code:     "func TestFunc() {}",
			expected: "func TestFunc()\n---\n",
		},
		{
			name:     "single type",
			code:     "type TestType struct {}",
			expected: "type TestType struct {}\n---\n",
		},
		{
			name:     "function with arguments and return values",
			code:     "func TestFunc(arg1 int, arg2 string) (bool, error) { return true, nil }",
			expected: "func TestFunc(arg1 int, arg2 string) (bool, error)\n---\n",
		},
		{
			name:     "function with receiver",
			code:     "func (t *TestType) TestFunc() {}",
			expected: "func (t *TestType) TestFunc()\n---\n",
		},
		{
			name:     "function with comment",
			code:     "// This is a test function\nfunc TestFunc() {}",
			expected: "// This is a test function\nfunc TestFunc()\n---\n",
		},
		{
			name:     "struct with comment",
			code:     "// This is a test type\ntype TestType struct {}",
			expected: "// This is a test type\ntype TestType struct {}\n---\n",
		},
		{
			name:     "variable declaration",
			code:     "var TestVar int",
			expected: "var TestVar int\n---\n",
		},
		{
			name:     "constant declaration",
			code:     "const TestConst = 42",
			expected: "const TestConst = 42\n---\n",
		},
		{
			name:     "struct with fields",
			code:     "type TestStruct struct { field1 int; field2 string }",
			expected: "type TestStruct struct { field1 int; field2 string }\n---\n",
		},
		{
			name:     "interface",
			code:     "type TestInterface interface { Method1(arg1 int) error; Method2() }",
			expected: "type TestInterface interface { Method1(arg1 int) error; Method2() }\n---\n",
		},
		{
			name:     "type alias",
			code:     "type TestAlias = int",
			expected: "type TestAlias = int\n---\n",
		},
		{
			name:     "enum (iota)",
			code:     "type TestEnum int\nconst ( Enum1 TestEnum = iota; Enum2 )",
			expected: "type TestEnum int\n---\nconst ( Enum1 TestEnum = iota; Enum2 )\n---\n",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create a temporary file with the test case code
			tmpfile, err := os.CreateTemp("", "test*.go")
			if err != nil {
				t.Fatalf("Failed to create temp file: %v", err)
			}
			defer os.Remove(tmpfile.Name())

			if _, err := tmpfile.Write([]byte(tc.code)); err != nil {
				t.Fatalf("Failed to write to temp file: %v", err)
			}
			if err := tmpfile.Close(); err != nil {
				t.Fatalf("Failed to close temp file: %v", err)
			}

			// Call GetFileSignatures with the path to the temp file
			result, err := GetFileSignaturesString(tmpfile.Name())
			if err != nil {
				t.Fatalf("GetFileSignatures returned an error: %v", err)
			}

			// Check the result
			if result != tc.expected {
				t.Errorf("GetFileSignatures returned incorrect result. Expected:\n%s\nGot:\n%s", tc.expected, result)
			}
		})
	}
}

func TestGetFileSymbolsStringGolang(t *testing.T) {
	tests := []struct {
		name     string
		code     string
		expected string
	}{
		{
			name: "simple functions",
			code: `package main

import "fmt"

func main() {
	fmt.Println("Hello, world!")
}

func add(a int, b int) int {
	return a + b
}`,
			expected: "main, add",
		},
		{
			name:     "empty",
			code:     "",
			expected: "",
		},
		{
			name:     "single function",
			code:     "func TestFunc() {}",
			expected: "TestFunc",
		},
		{
			name:     "single type",
			code:     "type TestType struct {}",
			expected: "TestType",
		},
		{
			name:     "function with arguments and return values",
			code:     "func TestFunc(arg1 int, arg2 string) (bool, error) { return true, nil }",
			expected: "TestFunc",
		},
		{
			name:     "function with receiver",
			code:     "func (t *TestType) TestFunc() {}",
			expected: "*TestType.TestFunc",
		},
		{
			name:     "function with comment",
			code:     "// This is a test function\nfunc TestFunc() {}",
			expected: "TestFunc",
		},
		{
			name:     "struct with comment",
			code:     "// This is a test type\ntype TestType struct {}",
			expected: "TestType",
		},
		{
			name:     "variable declaration",
			code:     "var TestVar int",
			expected: "TestVar",
		},
		{
			name:     "constant declaration",
			code:     "const TestConst = 42",
			expected: "TestConst",
		},
		{
			name:     "struct with fields",
			code:     "type TestStruct struct { field1 int; field2 string }",
			expected: "TestStruct",
		},
		{
			name:     "interface",
			code:     "type TestInterface interface { Method1(arg1 int) error; Method2() }",
			expected: "TestInterface",
		},
		{
			name:     "type alias",
			code:     "type TestAlias = int",
			expected: "TestAlias",
		},
		{
			name:     "enum (iota)",
			code:     "type TestEnum int\nconst ( Enum1 TestEnum = iota; Enum2 )",
			expected: "TestEnum, Enum1, Enum2",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tmpfile, err := os.CreateTemp("", "*.go")
			if err != nil {
				t.Fatalf("Failed to create temp file: %v", err)
			}
			defer os.Remove(tmpfile.Name())

			if _, err := tmpfile.Write([]byte(test.code)); err != nil {
				t.Fatalf("Failed to write to temp file: %v", err)
			}
			if err := tmpfile.Close(); err != nil {
				t.Fatalf("Failed to close temp file: %v", err)
			}

			symbolsString, err := GetFileSymbolsString(tmpfile.Name())
			if err != nil {
				t.Fatalf("Failed to get symbols: %v", err)
			}

			if symbolsString != test.expected {
				t.Errorf("Got %s, expected %s", symbolsString, test.expected)
			}
		})
	}
}

func TestGetAllAlternativeFileSymbolsGolang(t *testing.T) {
	// Define the test cases
	testCases := []struct {
		name           string
		input          string
		expectedOutput []string
	}{
		{
			name: "Method with pointer receiver",
			input: `
				package main

				func (x *T) Foo() {}
			`,
			expectedOutput: []string{"(x *T) Foo", "(x *T).Foo", "(x T) Foo", "(x T).Foo", "(T) Foo", "(T).Foo", "(*T) Foo", "(*T).Foo", "*T.Foo", "T.Foo", "*T Foo", "T Foo", "Foo"},
		},
		{
			name: "Method with value receiver",
			input: `
				package main

				func (x T) Foo() {}
			`,
			expectedOutput: []string{"(x T) Foo", "(x T).Foo", "(T) Foo", "(T).Foo", "(*T) Foo", "(*T).Foo", "*T.Foo", "T.Foo", "*T Foo", "T Foo", "Foo"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Write the input to a temp file with a '.go' extension
			filePath, err := utils.WriteTestTempFile(t, "go", tc.input)
			if err != nil {
				t.Fatalf("Failed to create temp file: %v", err)
			}
			defer os.Remove(filePath)

			// Call the function and check the output
			output, err := GetAllAlternativeFileSymbols(filePath)
			if err != nil {
				t.Fatalf("failed to get all alternative file symbols: %v", err)
			}
			outputStr := symbolToStringSlice(output)
			if !assert.ElementsMatch(t, outputStr, tc.expectedOutput) {
				t.Errorf("Expected %s, but got %s", utils.PanicJSON(tc.expectedOutput), utils.PanicJSON(outputStr))
			}
		})
	}
}
