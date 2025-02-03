package tree_sitter

import (
	"os"
	"sidekick/utils"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetFileSignaturesStringPython(t *testing.T) {
	t.Parallel()
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
@class_decorator(arg1, arg2)
class Greeter
	def what(self)
	@method_decorator(arg3, arg4)
	def greet(self, name)
	def what2(self)
	def what3(self)
---
type Vector = list[float]
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
			t.Parallel()
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
	t.Parallel()
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
			expected: "TestClass, method1, method2",
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
			expected: "Greeter, greet",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
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

func TestGetSymbolDefinitionPython(t *testing.T) {
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
			code: `other = True
def TestFunc():
	print("Hello, world!")`,
			expectedDefinition: `def TestFunc():
	print("Hello, world!")`,
		},
		{
			name:       "decorated function definition",
			symbolName: "TestFunc",
			code: `other = True
@my_decorator
def TestFunc():
	print("Hello, world!")`,
			expectedDefinition: `@my_decorator
def TestFunc():
	print("Hello, world!")`,
		},
		{
			name:       "class definition",
			symbolName: "TestClass",
			code: `class TestClass:
	def __init__(self, name, age):
		self.name = name
		self.age = age`,
			expectedDefinition: `class TestClass:
	def __init__(self, name, age):
		self.name = name
		self.age = age`,
		},
		{
			name:       "nested class definition",
			symbolName: "NestedClass",
			code: `class TestClass:
	class NestedClass:
		def __init__(self, name, age):
			self.name = name
			self.age = age`,
			expectedDefinition: `	class NestedClass:
		def __init__(self, name, age):
			self.name = name
			self.age = age`,
		},
		{
			name:       "nested class definition with parent class specifier",
			symbolName: "TestClass.NestedClass",
			code: `class TestClass:
	class NestedClass:
		def __init__(self, name, age):
			self.name = name
			self.age = age`,
			expectedDefinition: `	class NestedClass:
		def __init__(self, name, age):
			self.name = name
			self.age = age`,
		},
		{
			name:       "class definition with decorator",
			symbolName: "TestClass",
			code: `other = True

@class_decorator(arg1, arg2)
class TestClass:
	def __init__(self, name, age):
		self.name = name
		self.age = age`,
			expectedDefinition: `@class_decorator(arg1, arg2)
class TestClass:
	def __init__(self, name, age):
		self.name = name
		self.age = age`,
		},
		{
			name:       "method definition",
			symbolName: "test_method",
			code: `otherCode = True
class TestClass:
	def test_method(self):
		print("This is a test method")`,
			expectedDefinition: `	def test_method(self):
		print("This is a test method")`,
		},
		{
			name:       "decorated method definition",
			symbolName: "test_method",
			code: `otherCode = True
class TestClass:
	@my_decorator
	def test_method(self):
		print("This is a test method")`,
			expectedDefinition: `	@my_decorator
	def test_method(self):
		print("This is a test method")`,
		},
		{
			name:       "method definition with class specifier + distractors",
			symbolName: "TestClass.test_method",
			code: `
class OtherClass:
    description = _("Other")
	def test_method(self):
		print("This is a other class test method")

class TestClass:
    description = _("Test")
	def test_method2(self):
		pass
	@my_decorator
	def test_method3(self):
		pass
	def test_method(self):
		print("This is a test method")`,
			expectedDefinition: `	def test_method(self):
		print("This is a test method")`,
		},
		{
			name:               "variable assignment",
			symbolName:         "TestVar",
			code:               "TestVar = 42",
			expectedDefinition: `TestVar = 42`,
		},
		{
			name:               "constant definition",
			symbolName:         "TestConst",
			code:               "const TestConst = 3.14",
			expectedDefinition: `const TestConst = 3.14`,
		},
		{
			name:          "symbol not found",
			symbolName:    "NonExistentSymbol",
			code:          "x = 10",
			expectedError: `symbol not found: NonExistentSymbol`,
		},
		{
			name:               "NewType definition",
			symbolName:         "UserId",
			code:               "UserId = NewType('UserId', int)",
			expectedDefinition: `UserId = NewType('UserId', int)`,
		},
		{
			name:               "Type alias",
			symbolName:         "Vector",
			code:               "type Vector = list[float]",
			expectedDefinition: `type Vector = list[float]`,
		},
		{
			name:               "Backcompat type alias",
			symbolName:         "Vector",
			code:               "Vector = list[float]",
			expectedDefinition: `Vector = list[float]`,
		},
		{
			name:               "Typed assignment",
			symbolName:         "Something",
			code:               "Something: AType = ok()",
			expectedDefinition: `Something: AType = ok()`,
		},
		{
			name:               "UserId definition",
			symbolName:         "UserId",
			code:               "UserId = NewType('UserId', int)",
			expectedDefinition: `UserId = NewType('UserId', int)`,
		},
		{
			name:               "typed function definition",
			symbolName:         "greet",
			code:               `def greet(name: str) -> None:\n\tprint("Hello, " + name)`,
			expectedDefinition: `def greet(name: str) -> None:\n\tprint("Hello, " + name)`,
		},
		{
			name:       "Typed method definition",
			symbolName: "greet",
			code: `
class Greeter:
	def greet(self, name: str) -> None:
		print("Hello, " + name)`,
			expectedDefinition: `	def greet(self, name: str) -> None:
		print("Hello, " + name)`,
		},
		// FIXME decorated functions, classes and methods should include the decorator in the definition
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			filePath, err := utils.WriteTestTempFile(t, "py", tc.code)
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
				t.Errorf("Expected definition:\n%s\nGot:\n%s", tc.expectedDefinition, definition)
			}
		})
	}
}

func TestGetFileHeadersStringPython(t *testing.T) {
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
			code:     "print('Hello, world!')",
			expected: "",
		},
		{
			name:     "import with comments",
			code:     "import math  # Import the math module",
			expected: "import math  # Import the math module\n",
		},
		{
			name:     "import with multiple lines",
			code:     "import math\nimport os\nimport sys",
			expected: "import math\nimport os\nimport sys\n",
		},
		{
			name:     "import with leading and trailing whitespace",
			code:     "    import math  \n  import os  \n  import sys  ",
			expected: "    import math  \n  import os  \n  import sys  \n",
		},
		{
			name:     "import with from and comments",
			code:     "from math import sqrt  # Import the sqrt function",
			expected: "from math import sqrt  # Import the sqrt function\n",
		},
		{
			name:     "import with from and alias",
			code:     "from math import sqrt as s",
			expected: "from math import sqrt as s\n",
		},
		{
			name:     "import with multiple from and alias",
			code:     "from math import sqrt as s, pow as p",
			expected: "from math import sqrt as s, pow as p\n",
		},
		{
			name:     "nested imports",
			code:     "def x():\n    import math\n    import os\n    import sys",
			expected: "",
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
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
			result, err := GetFileHeadersString(tmpfile.Name(), 0)
			assert.Nil(t, err)
			// Check the result
			if result != tc.expected {
				t.Errorf("GetFileHeadersString returned incorrect result. Expected:\n%s\nGot:\n%s", utils.PanicJSON(tc.expected), utils.PanicJSON(result))
			}
		})
	}
}
