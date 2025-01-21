package tree_sitter

import (
	"os"
	"sidekick/utils"
	"strings"
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

func TestGetSymbolDefinitionGolang(t *testing.T) {
	testCases := []struct {
		name               string
		symbolName         string
		code               string
		expectedDefinition string
		expectedError      string
	}{
		{
			name:          "empty code",
			symbolName:    "TestVar",
			code:          "",
			expectedError: `symbol not found: TestVar`,
		},
		{
			name:       "function definition",
			symbolName: "TestFunc",
			code: `package main

func TestFunc() {
	println("Hello, world!")
}`,
			expectedDefinition: `func TestFunc() {
	println("Hello, world!")
}`,
		},
		{
			name:       "struct definition",
			symbolName: "TestStruct",
			code: `package main

type TestStruct struct {
	Name string
	Age  int
}`,
			expectedDefinition: `type TestStruct struct {
	Name string
	Age  int
}`,
		},
		// TODO /gen/plan/req make the following commented out test work
		/*
					{
						name:       "struct with methods definition",
						symbolName: "TestStruct",
						code: `package main

			type TestStruct struct {
				Name string
				Age  int
			}

			func (t *TestStruct) GetName() string {
				return t.Name
			}

			func (t *TestStruct) GetAge() int {
				return t.Age
			}`,
						expectedDefinition: `type TestStruct struct {
				Name string
				Age  int
			}
			func (t *TestStruct) GetName() string
			func (t *TestStruct) GetAge() int`,
					},
		*/
		{
			name:       "commented function definition",
			symbolName: "TestFunc",
			code: `package main

// TestFunc is a test function.
func TestFunc() {
	println("Hello, world!")
}`,
			expectedDefinition: `// TestFunc is a test function.
func TestFunc() {
	println("Hello, world!")
}`,
		},
		{
			name:       "commented struct definition",
			symbolName: "TestStruct",
			code: `package main

// TestStruct is a test struct.
type TestStruct struct {
	Name string
	Age  int
}`,
			expectedDefinition: `// TestStruct is a test struct.
type TestStruct struct {
	Name string
	Age  int
}`,
		},
		{
			name:       "const definition",
			symbolName: "TestConst",
			code: `package main

const TestConst = "test"`,
			expectedDefinition: `const TestConst = "test"`,
		},
		{
			name:       "var definition",
			symbolName: "TestVar",
			code: `package main

var TestVar = "test"`,
			expectedDefinition: `var TestVar = "test"`,
		},
		{
			name:       "commented const definition",
			symbolName: "TestConst",
			code: `package main

// TestConst is a test const.
const TestConst = "test"`,
			expectedDefinition: `// TestConst is a test const.
const TestConst = "test"`,
		},
		{
			name:       "symbol not found",
			symbolName: "NonExistentSymbol",
			code: `package main

var TestVar = "test"`,
			expectedError: `symbol not found: NonExistentSymbol`,
		},
		// we include the entire interface definition as part of the function definition
		{
			name:       "interface method definition",
			symbolName: "TestMethod",
			code: `package main

type TestInterface interface {
	TestMethod()
	TestMethod2()
}`,
			expectedDefinition: `type TestInterface interface {
	TestMethod()
	TestMethod2()
}`,
		},

		{
			name:       "type alias definition",
			symbolName: "Something",
			code: `package main

type Something = string
`,
			expectedDefinition: `type Something = string`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			filePath, err := utils.WriteTestTempFile(t, "go", tc.code)
			if err != nil {
				t.Fatalf("Failed to write temp file: %v", err)
			}
			defer os.Remove(filePath)

			definition, err := GetSymbolDefinitionsString(filePath, tc.symbolName, 0)
			if err != nil {
				if tc.expectedError == "" {
					t.Fatalf("Unexpected error: %v", err)
				} else if !strings.Contains(err.Error(), tc.expectedError) {
					t.Fatalf("Expected error: %s, got: %v", tc.expectedError, err)
				}
			}

			if strings.TrimSuffix(definition, "\n") != strings.TrimSuffix(tc.expectedDefinition, "\n") {
				t.Errorf("Expected definition:\n%s\nGot:\n%s", utils.PanicJSON(tc.expectedDefinition), utils.PanicJSON(definition))
			}
		})
	}
}

func TestGetFileHeadersStringGolang(t *testing.T) {
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
			name:     "single import",
			code:     "import \"fmt\"",
			expected: "import \"fmt\"\n",
		},
		{
			name:     "multiple imports",
			code:     "import (\n\t\"fmt\"\n\t\"os\"\n)",
			expected: "import (\n\t\"fmt\"\n\t\"os\"\n)\n",
		},
		{
			name:     "import with alias",
			code:     "import f \"fmt\"",
			expected: "import f \"fmt\"\n",
		},
		{
			name:     "import with dot",
			code:     "import . \"fmt\"",
			expected: "import . \"fmt\"\n",
		},
		{
			name:     "import with underscore",
			code:     "import _ \"fmt\"",
			expected: "import _ \"fmt\"\n",
		},
		{
			name:     "package declaration",
			code:     "package main",
			expected: "package main\n",
		},
		{
			name:     "package + import",
			code:     "package main\nimport \"fmt\"",
			expected: "package main\nimport \"fmt\"\n",
		},
		{
			name:     "package + empty line + import",
			code:     "package main\n\nimport \"fmt\"",
			expected: "package main\n\nimport \"fmt\"\n",
		},
		{
			name:     "package + multiple whitespace lines + import",
			code:     "package main\n\n\t\t\n  \n \t \t\nimport \"fmt\"",
			expected: "package main\n\n\t\t\n  \n \t \t\nimport \"fmt\"\n",
		},
		{
			name:     "package later in file",
			code:     "import \"fmt\"\npackage main",
			expected: "import \"fmt\"\npackage main\n",
		},
		{
			name:     "import later in file",
			code:     "package main\nfunc main() {}\nimport \"fmt\"",
			expected: "package main\n---\nimport \"fmt\"\n",
		},
		{
			name:     "package twice in file, top and later",
			code:     "package main\nfunc main() {}\npackage main",
			expected: "package main\n---\npackage main\n",
		},
		{
			name:     "import twice in file, top and later",
			code:     "import \"fmt\"\nfunc main() {}\nimport \"os\"",
			expected: "import \"fmt\"\n---\nimport \"os\"\n",
		},
		{
			name:     "package + import twice in file, top and later",
			code:     "package main\nimport \"fmt\"\nfunc main() {}\npackage main\nimport \"os\"",
			expected: "package main\nimport \"fmt\"\n---\npackage main\nimport \"os\"\n",
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

			result, err := GetFileHeadersString(tmpfile.Name(), 0)
			assert.Nil(t, err)

			// Check the result
			if result != tc.expected {
				t.Errorf("GetFileHeadersString returned incorrect result. Expected:\n%s\nGot:\n%s", utils.PanicJSON(tc.expected), utils.PanicJSON(result))
			}
		})
	}
}
