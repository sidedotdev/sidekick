package tree_sitter

import (
	"os"
	"strings"
	"testing"

	"sidekick/utils"

	"github.com/stretchr/testify/assert"
)

func TestGetFileSignaturesStringTypescript(t *testing.T) {
	t.Parallel()
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
			name:     "function declaration",
			code:     "function TestFunc(arg1: number, arg2: string): boolean { return true; }",
			expected: "function TestFunc(arg1: number, arg2: string): boolean\n---\n",
		},
		{
			name:     "function",
			code:     "let TestFunc = function(arg1: number, arg2: string): boolean { return true; }",
			expected: "let TestFunc = function(arg1: number, arg2: string): boolean { return true; }\n---\n",
		},
		{
			name:     "interface declaration",
			code:     "interface TestInterface { method1(arg1: number): void; method2(): void; }",
			expected: "interface TestInterface { method1(arg1: number): void; method2(): void; }\n---\n",
		},
		{
			name:     "type alias declaration",
			code:     "type TestAlias = number;",
			expected: "type TestAlias = number;\n---\n",
		},
		{
			name:     "lexical declaration",
			code:     "let TestVar: number;",
			expected: "let TestVar: number;\n---\n",
		},
		{
			name:     "empty class declaration",
			code:     "class TestClass {}",
			expected: "class TestClass\n---\n",
		},
		{
			name:     "class with one method",
			code:     "class TestClass { method1(arg1: number): void {} }",
			expected: "class TestClass\n\tmethod1(arg1: number): void\n---\n",
		},
		{
			name: "class declaration with constructor, member and method",
			code: `class Rectangle extends Polygon {
  private abc: string;
  public constructor(protected readonly width: number, protected readonly height: number) {
    super();
  }

  public getArea(): number {
    return this.width * this.height;
  }
}
			`,
			expected: `class Rectangle extends Polygon
	private abc: string
	public constructor(protected readonly width: number, protected readonly height: number)
	public getArea(): number
---
`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// Create a temporary file with the test case code
			tmpfile, err := os.CreateTemp("", "test*.ts")
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
				t.Errorf("GetFileSignatures returned incorrect result. Expected:\n%s\nGot:\n%s", utils.PanicJSON(tc.expected), utils.PanicJSON(result))
			}
		})
	}
}

func TestGetFileSymbolsStringTypescript(t *testing.T) {
	t.Parallel()
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
			expected: "TestClass, testMethod",
		},
		{
			name:     "single class with multiple methods",
			code:     "class TestClass { testMethod1() {} testMethod2() {} }",
			expected: "TestClass, testMethod1, testMethod2",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
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

func TestGetSymbolDefinitionTypescript(t *testing.T) {
	t.Parallel()
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
			code: `function TestFunc() {
	console.log("Hello, world!");
}`,
			expectedDefinition: `function TestFunc() {
	console.log("Hello, world!");
}`,
		},
		{
			name:       "class definition",
			symbolName: "TestClass",
			code: `class TestClass {
	constructor(name, age) {
		this.name = name;
		this.age = age;
	}
}`,
			expectedDefinition: `class TestClass {
	constructor(name, age) {
		this.name = name;
		this.age = age;
	}
}`,
		},
		// TODO bring back once this is fixed: https://github.com/tree-sitter/tree-sitter/issues/2799#issue-2016383906
		//		{
		//			name:       "commented function definition",
		//			symbolName: "TestFunc",
		//			code: `// TestFunc is a test function.
		//function TestFunc() {
		//	console.log("Hello, world!");
		//}`,
		//			expectedDefinition: `// TestFunc is a test function.
		//function TestFunc() {
		//	console.log("Hello, world!");
		//}`,
		//		},
		//		{
		//			name:       "commented class definition",
		//			symbolName: "TestClass",
		//			code: `// TestClass is a test class.
		//class TestClass {
		//	constructor(name, age) {
		//		this.name = name;
		//		this.age = age;
		//	}
		//}`,
		//			expectedDefinition: `// TestClass is a test class.
		//class TestClass {
		//	constructor(name, age) {
		//		this.name = name;
		//		this.age = age;
		//	}
		//}`,
		//		},
		//		{
		//			name:       "commented const definition",
		//			symbolName: "TestConst",
		//			code: `// TestConst is a test const.
		//const TestConst = "test";`,
		//			expectedDefinition: `// TestConst is a test const.
		//const TestConst = "test";`,
		//		},
		{
			name:               "const definition",
			symbolName:         "TestConst",
			code:               `const TestConst = "test";`,
			expectedDefinition: `const TestConst = "test";`,
		},
		{
			name:               "var definition",
			symbolName:         "TestVar",
			code:               `var TestVar = "test";`,
			expectedDefinition: `var TestVar = "test";`,
		},
		{
			name:          "symbol not found",
			symbolName:    "NonExistentSymbol",
			code:          `var TestVar = "test";`,
			expectedError: `symbol not found: NonExistentSymbol`,
		},
		// we include the entire interface definition as part of the function definition
		{
			name:       "interface method definition",
			symbolName: "TestMethod",
			code: `interface TestInterface {
	TestMethod();
	TestMethod2();
}`,
			expectedDefinition: `interface TestInterface {
	TestMethod();
	TestMethod2();
}`,
		},
		{
			name:       "call expression",
			symbolName: "someFunction",
			code: `somethingElse();

const x = someFunction({
	foo: 'bar'
});`,
			expectedDefinition: `const x = someFunction({
	foo: 'bar'
});`,
		},
		{
			name:       "exported call expression with extra comment",
			symbolName: "someFunction",
			code: `somethingElse();

export const x = someFunction({
	foo: 'bar'
}); // testing`,
			expectedDefinition: `export const x = someFunction({
	foo: 'bar'
}); // testing`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			filePath, err := utils.WriteTestTempFile(t, "ts", tc.code)
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

func TestGetFileHeadersStringTypescript(t *testing.T) {
	t.Parallel()
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
			name:     "no imports",
			code:     "const foo = 'bar';",
			expected: "",
		},
		{
			name:     "single import",
			code:     "import { foo } from 'bar';",
			expected: "import { foo } from 'bar';\n",
		},
		{
			name:     "single import with whitespace",
			code:     " import { foo } from 'bar';",
			expected: " import { foo } from 'bar';\n",
		},
		{
			name:     "multiple imports",
			code:     "import { foo, foo2 } from 'bar';\nimport { baz } from 'qux';",
			expected: "import { foo, foo2 } from 'bar';\nimport { baz } from 'qux';\n",
		},
		{
			name:     "import with alias",
			code:     "import { foo as f } from 'bar';",
			expected: "import { foo as f } from 'bar';\n",
		},
		{
			name:     "import with default",
			code:     "import foo from 'bar';",
			expected: "import foo from 'bar';\n",
		},
		{
			name:     "import with namespace",
			code:     "import * as foo from 'bar';",
			expected: "import * as foo from 'bar';\n",
		},
		{
			name:     "import with side effects",
			code:     "import 'bar';",
			expected: "import 'bar';\n",
		},
		{
			name:     "import with type only",
			code:     "import type { foo } from 'bar';",
			expected: "import type { foo } from 'bar';\n",
		},
		{
			name:     "import with type and side effects",
			code:     "import type 'bar';",
			expected: "import type 'bar';\n",
		},
		{
			name:     "import with type and default",
			code:     "import type foo from 'bar';",
			expected: "import type foo from 'bar';\n",
		},
		{
			name:     "import with type and namespace",
			code:     "import type * as foo from 'bar';",
			expected: "import type * as foo from 'bar';\n",
		},
		{
			name:     "nested imports",
			code:     "function x() {\n    import { foo } from 'bar';\n    import { baz } from 'qux';\n}",
			expected: "",
		},
		{
			name:     "import aliases",
			code:     "import { foo as f, bar as b } from 'bar';",
			expected: "import { foo as f, bar as b } from 'bar';\n",
		},
		{
			name:     "import equals",
			code:     "import foo = bar;",
			expected: "import foo = bar;\n",
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// Create a temporary file with the test case code
			tmpfile, err := os.CreateTemp("", "test*.ts")
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

// FIXME /gen move test cases into TestGetSymbolDefinitionTypescript and remove this separate test function
func TestGetSymbolDefinitionTypescriptEnum(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name               string
		symbolName         string
		code               string
		expectedDefinition string
		expectedError      string
	}{
		{
			name:               "simple enum with string literal member",
			symbolName:         "MySimpleEnum",
			code:               `enum MySimpleEnum { Member1 = "val1" }`,
			expectedDefinition: `enum MySimpleEnum { Member1 = "val1" }`,
			expectedError:      "",
		},
		{
			name:               "exported enum with string literal members",
			symbolName:         "MyTestStatus",
			code:               `export enum MyTestStatus { Active = "active", Inactive = "inactive" }`,
			expectedDefinition: `export enum MyTestStatus { Active = "active", Inactive = "inactive" }`,
			expectedError:      "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			filePath, err := utils.WriteTestTempFile(t, "ts", tc.code)
			if err != nil {
				t.Fatalf("Failed to write temp file: %v", err)
			}
			defer os.Remove(filePath)

			definition, err := GetSymbolDefinitionsString(filePath, tc.symbolName, 0)

			if tc.expectedError != "" {
				// We expect an error
				if err == nil {
					t.Fatalf("Expected error containing '%s', but got no error. Definition returned:\n%s", tc.expectedError, definition)
				}
				if !strings.Contains(err.Error(), tc.expectedError) {
					t.Fatalf("Expected error containing '%s', got: %v", tc.expectedError, err)
				}
				// If err is not nil AND contains the expectedError, this sub-test passes for this step's goal (to show current failure).
			} else {
				// We expect no error (this branch is for when the fix is implemented in a future step)
				if err != nil {
					t.Fatalf("GetSymbolDefinitionsString returned an unexpected error: %v", err)
				}
				if strings.TrimSuffix(definition, "\n") != strings.TrimSuffix(tc.expectedDefinition, "\n") {
					t.Errorf("Expected definition:\n%s\nGot:\n%s", utils.PanicJSON(tc.expectedDefinition), utils.PanicJSON(definition))
				}
			}
		})
	}
}

func TestNormalizeSymbolFromSnippet_Typescript(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		snippet  string
		expected string
	}{
		{
			name:     "Top-level function or method signature",
			snippet:  "function someFunc(content: string): string",
			expected: "someFunc",
		},
		{
			name:     "Class signature",
			snippet:  "class SomeClass",
			expected: "SomeClass",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := NormalizeSymbolFromSnippet("typescript", tc.snippet)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.expected {
				t.Fatalf("expected %q, got %q", tc.expected, got)
			}
		})
	}
}
