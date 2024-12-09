package tree_sitter

import (
	"os"
	"path/filepath"
	"sidekick/utils"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

const search = "<<<<<<< SEARCH_EXACT"
const divider = "======="
const replace = ">>>>>>> REPLACE_EXACT"

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

func TestGetFileSymbolsStringTypescript(t *testing.T) {
	tests := []struct {
		name     string
		code     string
		expected string
	}{
		{
			name: "simple functions",
			code: `function helloWorld() {
	console.log("Hello, world!");
}

function add(a: number, b: number): number {
	return a + b;
}`,
			expected: "helloWorld, add",
		},
		{
			name:     "empty",
			code:     "",
			expected: "",
		},
		{
			name:     "single function",
			code:     "function testFunc() {}",
			expected: "testFunc",
		},
		{
			name:     "single type",
			code:     "type TestType = {}",
			expected: "TestType",
		},
		{
			name:     "function with arguments and return values",
			code:     "function testFunc(arg1: number, arg2: string): boolean { return true }",
			expected: "testFunc",
		},
		{
			name:     "function with comment",
			code:     "// This is a test function\nfunction testFunc() {}",
			expected: "testFunc",
		},
		{
			name:     "type with comment",
			code:     "// This is a test type\ntype TestType = {}",
			expected: "TestType",
		},
		{
			name:     "let declaration",
			code:     "let testLet: number",
			expected: "testLet",
		},
		{
			name:     "constant declaration",
			code:     "const testConst = 42",
			expected: "testConst",
		},
		{
			name:     "var declaration",
			code:     "var testVar: number",
			expected: "testVar",
		},
		{
			name:     "type with fields",
			code:     "type TestType = { field1: number; field2: string }",
			expected: "TestType",
		},
		{
			name:     "interface",
			code:     "interface TestInterface { method1(arg1: number): Error; method2(): void }",
			expected: "TestInterface",
		},
		{
			name:     "type alias",
			code:     "type TestAlias = number",
			expected: "TestAlias",
		},
		{
			name:     "enum",
			code:     "enum TestEnum { Enum1, Enum2 }",
			expected: "TestEnum, Enum1, Enum2",
		},
		{
			name:     "single class",
			code:     "class TestClass {}",
			expected: "TestClass",
		},
		{
			name:     "single class with single method",
			code:     "class TestClass { testMethod() {} }",
			expected: "testMethod, TestClass",
		},
		{
			name:     "single class with multiple methods",
			code:     "class TestClass { testMethod1() {} testMethod2() {} }",
			expected: "testMethod1, testMethod2, TestClass",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tmpfile, err := os.CreateTemp("", "*.ts")
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

func TestGetFileSymbolsStringPython(t *testing.T) {
	tests := []struct {
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
			code:     "def test_func(): pass",
			expected: "test_func",
		},
		{
			name: "multiple functions",
			code: `
def hello_world():
	print("Hello, world!")

def add(a, b):
	return a + b`,
			expected: "hello_world, add",
		},
		{
			name:     "single empty class",
			code:     "class TestClass: pass",
			expected: "TestClass",
		},

		{
			name: "multiple classes",
			code: `
class TestClass1:
	pass

class TestClass2:
	pass`,
			expected: "TestClass1, TestClass2",
		},
		{
			name:     "function with arguments and return values",
			code:     "def test_func(arg1, arg2): return True",
			expected: "test_func",
		},
		{
			name:     "function with comment",
			code:     "# This is a test function\ndef test_func(): pass",
			expected: "test_func",
		},
		{
			name:     "class with comment",
			code:     "# This is a test class\nclass TestClass: pass",
			expected: "TestClass",
		},
		{
			name: "class with methods",
			code: `
class TestClass:
	def method1(self):
		pass
	def method2(self):
		pass`,
			expected: "method1, method2, TestClass",
		},
		{
			name:     "variable declaration",
			code:     "test_var = 42",
			expected: "test_var",
		},
		{
			name:     "Type Alias",
			code:     "type Vector = list[float]",
			expected: "Vector",
		},
		{
			name:     "Type Alias (alternative syntax for backcompat pre-3.12)",
			code:     "Vector = list[float]",
			expected: "Vector",
		},
		{
			name:     "Typed expression",
			code:     "Something: AType = ok()",
			expected: "Something",
		},
		{
			name:     "Type Alias (with annotation)",
			code:     "Vector: TypeAlias = list[float]",
			expected: "Vector",
		},
		{
			name:     "NewType",
			code:     "UserId = NewType('UserId', int)",
			expected: "UserId",
		},
		{
			name:     "Typed function",
			code:     "def greet(name: str) -> None:\n\tprint(\"Hello, \" + name)",
			expected: "greet",
		},
		{
			name:     "Typed method",
			code:     "class Greeter:\n\tdef greet(self, name: str) -> None:\n\t\tprint(\"Hello, \" + name)",
			expected: "greet, Greeter",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tmpfile, err := os.CreateTemp("", "*.py")
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
				t.Errorf("Got:\n%s, expected:\n%s", symbolsString, test.expected)
			}
		})
	}
}

func TestGetFileSymbolsStringVue(t *testing.T) {
	tests := []struct {
		name     string
		code     string
		expected string
	}{
		{
			name: "filename is included",
			code: `
				<template>
					<button @click="sayHello">Click me</button>
				</template>

				<script>
				export default {
					methods: {
						sayHello() {
							console.log('Hello, world!');
						}
					}
				}
				</script>
			`,
			expected: "<template>, <script>, sayHello, placeholder_filename",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tmpfile, err := os.CreateTemp("", "*.vue")
			if err != nil {
				t.Fatalf("Failed to create temp file: %v", err)
			}
			defer os.Remove(tmpfile.Name())
			sfcName := strings.ReplaceAll(filepath.Base(tmpfile.Name()), filepath.Ext(tmpfile.Name()), "")
			test.expected = strings.ReplaceAll(test.expected, "placeholder_filename", sfcName)

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

func TestGetAllAlternativeFileSymbolsVue(t *testing.T) {
	tests := []struct {
		name                string
		code                string
		expectedSymbolNames []string
	}{
		{
			name: "filename is included",
			code: `
				<template>
					<button @click="sayHello">Click me</button>
				</template>

				<script>
				export default {
					methods: {
						sayHello() {
							console.log('Hello, world!');
						}
					}
				}
				</script>
			`,
			expectedSymbolNames: []string{"<template>", "<script>", "sayHello", "placeholder_filename"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Write the input to a temp file with a '.go' extension
			filePath, err := utils.WriteTestTempFile(t, "vue", tc.code)
			if err != nil {
				t.Fatalf("Failed to create temp file: %v", err)
			}
			defer os.Remove(filePath)

			for i, symbol := range tc.expectedSymbolNames {
				if symbol == "placeholder_filename" {
					tc.expectedSymbolNames[i] = strings.ReplaceAll(filepath.Base(filePath), filepath.Ext(filePath), "")
				}
			}

			// Call the function and check the output
			output, err := GetAllAlternativeFileSymbols(filePath)
			if err != nil {
				t.Fatalf("failed to get all alternative file symbols: %v", err)
			}
			outputStr := symbolToStringSlice(output)
			if !assert.ElementsMatch(t, outputStr, tc.expectedSymbolNames) {
				t.Errorf("Expected %s, but got %s", utils.PanicJSON(tc.expectedSymbolNames), utils.PanicJSON(outputStr))
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

func symbolToStringSlice(symbols []Symbol) []string {
	var strSlice []string
	for _, symbol := range symbols {
		strSlice = append(strSlice, symbol.Content)
	}
	return strSlice
}

// TODO use this for examples to use to test ExtractSourceCodes
func TestSymbolizeEmbeddedCode(t *testing.T) {
	testCases := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name: "Simplest Go code block",
			input: `
Some preamble

` + "```" + `go
type SomeStruct struct {}
` + "```" + `

Some postamble
`,
			expected: `
Some preamble

` + "```" + `go
SomeStruct
` + "```" + `

Some postamble
`,
		},
		{
			name: "Triple Backticks inside a code block",
			input: `
Some preamble

` + "```" + `go
var aString = "` + "```" + `"
` + "```" + `

Some postamble
`,
			expected: `
Some preamble

` + "```" + `go
aString
` + "```" + `

Some postamble
`,
		},
		{
			name: "Go code block",
			input: `
Some preamble

` + "```" + `go
func SomeFunc(content string) (string, error) {
	return content, nil
}
type SomeStruct struct {}
` + "```" + `

Some postamble
`,
			expected: `
Some preamble

` + "```" + `go
SomeFunc, SomeStruct
` + "```" + `

Some postamble
`,
		},
		{
			name: "Go edit block",
			input: `
Some preamble

` + "```" + `go
edit_block:1
path/to/file.go
` + search + `
func SomeFunc(content string) (string, error) {
` + divider + `
func SomeFunc(another string) (string, error) {
` + replace + `
` + "```" + `

Some postamble
`,
			expected: `
Some preamble

` + "```" + `go
edit_block:1
path/to/file.go
` + search + `
func SomeFunc(content string) (string, error) {
` + divider + `
func SomeFunc(another string) (string, error) {
` + replace + `
` + "```" + `

Some postamble
`,
		},
		{
			name: "TypeScript code block",
			input: `
Some preamble

` + "```" + `typescript
function someFunc(content: string): string {
	return content;
}
interface SomeInterface {}
` + "```" + `

Some postamble
`,
			expected: `
Some preamble

` + "```" + `typescript
someFunc, SomeInterface
` + "```" + `

Some postamble
`,
		},
		{
			name: "Python code block",
			input: `
Some preamble

` + "```" + `python
def some_func(content):
	return content
class SomeClass:
	pass
` + "```" + `

Some postamble`,
			expected: `
Some preamble

` + "```" + `python
some_func, SomeClass
` + "```" + `

Some postamble`,
		},
		{
			name: "Multiple code blocks",
			input: `
Some preamble

` + "```" + `go
func SomeFunc(content string) (string, error) {
	return content, nil
}
type SomeStruct struct {}
` + "```" + `

` + "```" + `typescript
function someFunc(content: string): string {
	return content;
}
interface SomeInterface {}
` + "```" + `

Some postamble
`,
			expected: `
Some preamble

` + "```" + `go
SomeFunc, SomeStruct
` + "```" + `

` + "```" + `typescript
someFunc, SomeInterface
` + "```" + `

Some postamble
`,
		},
		{
			name: "No code blocks",
			input: `
Some preamble

Some postamble
`,
			expected: `
Some preamble

Some postamble
`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := SymbolizeEmbeddedCode(tc.input)
			if result != tc.expected {
				t.Errorf("expected %q, got %q", tc.expected, result)
			}

			// re-symbolize the result to ensure it's idempotent
			result2 := SymbolizeEmbeddedCode(result)
			if result2 != tc.expected {
				t.Errorf("expected %q, got %q", tc.expected, result2)
			}
		})
	}
}
