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
