package tree_sitter

import (
	"os"
	"sidekick/utils"
	"strings"
	"testing"
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

func TestGetFileSignaturesStringPython(t *testing.T) {
	testCases := []struct {
		name     string
		code     string
		expected string
	}{
		{
			name: "Simple Function",
			code: `def hello_world():
	print("Hello, world!")`,
			expected: "def hello_world()\n---\n",
		},
		{
			name: "Function with Single Parameter",
			code: `def greet(name):
	print("Hello, " + name)`,
			expected: "def greet(name)\n---\n",
		},
		{
			name: "Function with Multiple Parameters",
			code: `def add_numbers(a, b):
	return a + b`,
			expected: "def add_numbers(a, b)\n---\n",
		},
		{
			name: "Function with decorator",
			code: `@my_decorator
def my_function():
	pass`,
			expected: `@my_decorator
def my_function()
---`,
		},
		{
			name: "Function with decorator with arguments",
			code: `@my_decorator(arg1, arg2)
def my_function(x, y):
	pass`,
			expected: `@my_decorator(arg1, arg2)
def my_function(x, y)
---`,
		},
		{
			name: "Class with method",
			code: `class Greeter:
	def greet(self, name):
		print("Hello, " + name)`,
			expected: "class Greeter\n\tdef greet(self, name)\n---\n",
		},
		{
			name: "Class with method and decorators on both class and method",
			code: `
@class_decorator(arg1, arg2)
class Greeter:
	@method_decorator(arg3, arg4)
	def greet(self, name):
		print("Hello, " + name)`,
			expected: `
@class_decorator(arg1, arg2)
class Greeter
	@method_decorator(arg3, arg4)
	def greet(self, name)
---
`,
		},
		{
			name: "Class with multiple method, expressions, type definitions and imports between them",
			code: `
@class_decorator(arg1, arg2)
class Greeter:
	def what(self):
		pass

	x = 1

	@method_decorator(arg3, arg4)
	def greet(self, name):
		print("Hello, " + name)

	type Vector = list[float]

	def what2(self):
		pass

	import os

	def what3(self):
		pass`,
			// NOTE the expected is not idealy here as the type is not nested under the class, but it'll do for now
			expected: `
type Vector = list[float]
---
@class_decorator(arg1, arg2)
class Greeter
	def what(self)
	@method_decorator(arg3, arg4)
	def greet(self, name)
	def what2(self)
	def what3(self)
---
`,
		},
		{
			name: "Multiple classes with inheritance",
			code: `
class Animal:
	def __init__(self, name):
		self.name = name

	def speak(self):
		pass

class Dog(Animal):
	def speak(self):
		return "Woof!"

class Cat(Animal):
	def speak(self):
		return "Meow!"`,
			expected: "class Animal\n\tdef __init__(self, name)\n\tdef speak(self)\n---\nclass Dog(Animal)\n\tdef speak(self)\n---\nclass Cat(Animal)\n\tdef speak(self)\n---\n",
		},
		{
			name: "Function with Default Parameter",
			code: `def greet(name="World"):
			print("Hello, " + name)`,
			expected: "def greet(name=\"World\")\n---\n",
		},
		{
			name: "Function with Variable Number of Arguments",
			code: `def add_numbers(*args):
			return sum(args)`,
			expected: "def add_numbers(*args)\n---\n",
		},
		{
			name: "Function with Docstring",
			code: `
def square(n):
	"""
	This function returns the square of a number.
	"""
	return n ** 2`,
			expected: `def square(n)
	"""
	This function returns the square of a number.
	"""
---
`,
		},
		{
			name: "Class with Docstring",
			code: `
class MyClass:
	"""
	This is a test class.
	"""
	def my_method(self):
		pass`,
			expected: `class MyClass
	"""
	This is a test class.
	"""
	def my_method(self)
---
`,
		},
		{
			name:     "Type Alias",
			code:     "type Vector = list[float]",
			expected: "type Vector = list[float]\n---\n",
		},
		{
			name:     "Type Alias (alternative syntax for backcompat pre-3.12)",
			code:     "Vector = list[float]",
			expected: "Vector\n---\n",
		},
		{
			name:     "Typed expression",
			code:     "Something: AType = ok()",
			expected: "Something: AType\n---\n",
		},
		/* TODO: Uncomment this test case once we support including right when @assignment.type is TypeAlias
		{
			name:     "Type Alias (with annotation)",
			code:     "Vector: TypeAlias = list[float]",
			expected: "Vector: TypeAlias = list[float]\n---\n",
		},
		*/
		{
			name:     "NewType",
			code:     "UserId = NewType('UserId', int)",
			expected: "UserId = NewType('UserId', int)\n---\n",
		},
		{
			name:     "Typed function",
			code:     "def greet(name: str) -> None:\n\tprint(\"Hello, \" + name)",
			expected: "def greet(name: str) -> None\n---\n",
		},
		{
			name:     "Typed method",
			code:     "class Greeter:\n\tdef greet(self, name: str) -> None:\n\t\tprint(\"Hello, \" + name)",
			expected: "class Greeter\n\tdef greet(self, name: str) -> None\n---\n",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create a temporary file with the test case code
			tmpfile, err := os.CreateTemp("", "test*.py")
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
			if strings.TrimSpace(result) != strings.TrimSpace(tc.expected) {
				t.Errorf("GetFileSignatures returned incorrect result. Expected:\n%s\nGot:\n%s", utils.PanicJSON(tc.expected), utils.PanicJSON(result))
			}
		})
	}
}
func TestSignaturizeEmbeddedCode(t *testing.T) {
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

` + "```" + `go-signatures
type SomeStruct struct {}
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

` + "```" + `go-signatures
func SomeFunc(content string) (string, error)
type SomeStruct struct {}
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

` + "```" + `typescript-signatures
function someFunc(content: string): string
interface SomeInterface {}
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

` + "```" + `python-signatures
def some_func(content)
class SomeClass
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

` + "```" + `go-signatures
func SomeFunc(content string) (string, error)
type SomeStruct struct {}
` + "```" + `

` + "```" + `typescript-signatures
function someFunc(content: string): string
interface SomeInterface {}
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
			result := SignaturizeEmbeddedCode(tc.input)
			if result != tc.expected {
				t.Errorf("expected %q, got %q", tc.expected, result)
			}

			// re-signaturize the result to ensure it's idempotent
			result2 := SignaturizeEmbeddedCode(result)
			if result2 != tc.expected {
				t.Errorf("expected %q, got %q", tc.expected, result2)
			}
		})
	}
}
