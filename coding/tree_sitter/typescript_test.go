package tree_sitter

import (
	"os"
	"sidekick/utils"
	"testing"
)

func TestGetFileSignaturesStringTypescript(t *testing.T) {
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
			expected: "class TestClass\n  method1(arg1: number): void\n---\n",
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
