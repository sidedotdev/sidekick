package tree_sitter

import (
	"os"
	"sidekick/utils"
	"strings"
	"testing"
)

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
