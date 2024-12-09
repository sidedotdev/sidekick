package tree_sitter

import (
	"os"
	"sidekick/utils"
	"strings"
	"testing"
)

func TestGetSymbolDefinitionVue(t *testing.T) {
	testCases := []struct {
		name               string
		symbolName         string
		code               string
		expectedDefinition string
		expectedError      string
	}{
		{
			name:          "empty code",
			symbolName:    "<template>",
			code:          "",
			expectedError: `symbol not found: <template>`,
		},
		{
			name:       "template definition",
			symbolName: "<template>",
			code: `<template>
  <div id="app">
    <h1>{{ message }}</h1>
  </div>
</template>

<script>
export default {
  data() {
    return {
      message: 'Hello Vue!'
    }
  }
}
</script>

<style scoped>
h1 {
  color: red;
}
</style>`,
			expectedDefinition: `<template>
  <div id="app">
    <h1>{{ message }}</h1>
  </div>
</template>`,
		},
		{
			name:       "script definition",
			symbolName: "<script>",
			code: `<template>
  <div id="app">
    <h1>{{ message }}</h1>
  </div>
</template>

<script>
export default {
  data() {
    return {
      message: 'Hello Vue!'
    }
  }
}
</script>

<style scoped>
h1 {
  color: red;
}
</style>`,
			expectedDefinition: `<script>
export default {
  data() {
    return {
      message: 'Hello Vue!'
    }
  }
}
</script>`,
		},
		{
			name:       "style definition",
			symbolName: "<style>",
			code: `<template>
  <div id="app">
    <h1>{{ message }}</h1>
  </div>
</template>

<script>
export default {
  data() {
    return {
      message: 'Hello Vue!'
    }
  }
}
</script>

<style scoped>
h1 {
  color: red;
}
</style>`,
			expectedDefinition: `<style scoped>
h1 {
  color: red;
}
</style>`,
		},
		{
			name:       "TypeScript function definition",
			symbolName: "myFunction",
			code: `<template>
  <div id="app">
    <h1>{{ message }}</h1>
  </div>
</template>

<script lang="ts">
function myFunction() {
  return 'Hello TypeScript!';
}
</script>

<style scoped>
h1 {
  color: red;
}
</style>`,
			expectedDefinition: `function myFunction() {
  return 'Hello TypeScript!';
}`,
		},
		{
			name:       "TypeScript variable declaration",
			symbolName: "myVariable",
			code: `<template>
  <div id="app">
    <h1>{{ message }}</h1>
  </div>
</template>

<script lang="ts">
let myVariable = 'Hello TypeScript!';
</script>

<style scoped>
h1 {
  color: red;
}
</style>`,
			expectedDefinition: `let myVariable = 'Hello TypeScript!';`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			filePath, err := utils.WriteTestTempFile(t, "vue", tc.code)
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

func TestGetSymbolDefinitionTypescript(t *testing.T) {
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

func TestGetSymbolDefinitionPython(t *testing.T) {
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
