package tree_sitter

import (
	"os"
	"strings"
	"testing"

	"sidekick/utils"

	"github.com/stretchr/testify/assert"
)

func TestGetFileHeadersStringJavascript(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name      string
		code      string
		extension string
		expected  string
	}{
		{
			name:      "empty",
			code:      "",
			extension: ".js",
			expected:  "",
		},
		{
			name:      "no imports",
			code:      "const foo = 'bar';",
			extension: ".js",
			expected:  "",
		},
		{
			name:      "single import",
			code:      "import { foo } from 'bar';",
			extension: ".js",
			expected:  "import { foo } from 'bar';\n",
		},
		{
			name:      "single import with whitespace",
			code:      " import { foo } from 'bar';",
			extension: ".js",
			expected:  " import { foo } from 'bar';\n",
		},
		{
			name:      "multiple imports",
			code:      "import { foo, foo2 } from 'bar';\nimport { baz } from 'qux';",
			extension: ".js",
			expected:  "import { foo, foo2 } from 'bar';\nimport { baz } from 'qux';\n",
		},
		{
			name:      "import with alias",
			code:      "import { foo as f } from 'bar';",
			extension: ".js",
			expected:  "import { foo as f } from 'bar';\n",
		},
		{
			name:      "import with default",
			code:      "import foo from 'bar';",
			extension: ".js",
			expected:  "import foo from 'bar';\n",
		},
		{
			name:      "import with namespace",
			code:      "import * as foo from 'bar';",
			extension: ".js",
			expected:  "import * as foo from 'bar';\n",
		},
		{
			name:      "import with side effects",
			code:      "import 'bar';",
			extension: ".js",
			expected:  "import 'bar';\n",
		},
		{
			name:      "nested imports not captured",
			code:      "function x() {\n    import('bar');\n}",
			extension: ".js",
			expected:  "",
		},
		{
			name:      "jsx file with imports",
			code:      "import React from 'react';\nimport { useState } from 'react';",
			extension: ".jsx",
			expected:  "import React from 'react';\nimport { useState } from 'react';\n",
		},
		{
			name:      "jsx file with jsx content",
			code:      "import React from 'react';\n\nfunction App() {\n  return <div>Hello</div>;\n}",
			extension: ".jsx",
			expected:  "import React from 'react';\n",
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tmpfile, err := os.CreateTemp("", "test*"+tc.extension)
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
			if result != tc.expected {
				t.Errorf("GetFileHeadersString returned incorrect result. Expected:\n%s\nGot:\n%s", utils.PanicJSON(tc.expected), utils.PanicJSON(result))
			}
		})
	}
}

func TestGetFileSignaturesStringJavascript(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name      string
		code      string
		extension string
		expected  string
	}{
		{
			name:      "empty",
			code:      "",
			extension: ".js",
			expected:  "",
		},
		{
			name:      "function declaration",
			code:      "function TestFunc(arg1, arg2) { return true; }",
			extension: ".js",
			expected:  "function TestFunc(arg1, arg2)\n---\n",
		},
		{
			name:      "async function declaration",
			code:      "async function fetchData(url) { return await fetch(url); }",
			extension: ".js",
			expected:  "async function fetchData(url)\n---\n",
		},
		{
			name:      "function expression",
			code:      "let TestFunc = function(arg1, arg2) { return true; }",
			extension: ".js",
			expected:  "let TestFunc = function(arg1, arg2) { return true; }\n---\n",
		},
		{
			name:      "lexical declaration",
			code:      "let TestVar = 42;",
			extension: ".js",
			expected:  "let TestVar = 42;\n---\n",
		},
		{
			name:      "const declaration",
			code:      "const TestConst = 'hello';",
			extension: ".js",
			expected:  "const TestConst = 'hello';\n---\n",
		},
		{
			name:      "var declaration",
			code:      "var myVar = 123;",
			extension: ".js",
			expected:  "var myVar = 123;\n---\n",
		},
		{
			name:      "export function",
			code:      "export function greet(name) { return 'Hello ' + name; }",
			extension: ".js",
			expected:  "export function greet(name)\n---\n",
		},
		{
			name:      "export const",
			code:      "export const PI = 3.14159;",
			extension: ".js",
			expected:  "export const PI = 3.14159;\n---\n",
		},
		{
			name:      "export default function",
			code:      "export default function main() { }",
			extension: ".js",
			expected:  "export default function main()\n---\n",
		},
		{
			name:      "export async function",
			code:      "export async function fetchData(url) { return await fetch(url); }",
			extension: ".js",
			expected:  "export async function fetchData(url)\n---\n",
		},
		{
			name:      "async function declaration",
			code:      "async function fetchData(url) { return await fetch(url); }",
			extension: ".js",
			expected:  "async function fetchData(url)\n---\n",
		},
		{
			name: "export class",
			code: `export class MyClass {
  constructor(name) {
    this.name = name;
  }
  greet() {
    return 'Hello ' + this.name;
  }
}`,
			extension: ".js",
			expected: `export class MyClass
	constructor(name)
	greet()
---
`,
		},
		{
			name:      "empty class declaration",
			code:      "class TestClass {}",
			extension: ".js",
			expected:  "class TestClass\n---\n",
		},
		{
			name:      "class with one method",
			code:      "class TestClass { method1(arg1) {} }",
			extension: ".js",
			expected:  "class TestClass\n\tmethod1(arg1)\n---\n",
		},
		{
			name: "class declaration with constructor and method",
			code: `class Rectangle extends Polygon {
  constructor(width, height) {
    super();
    this.width = width;
    this.height = height;
  }

  getArea() {
    return this.width * this.height;
  }
}
`,
			extension: ".js",
			expected: `class Rectangle extends Polygon
	constructor(width, height)
	getArea()
---
`,
		},
		{
			name: "generator function declaration",
			code: `function* myGenerator() {
	yield 1;
	yield 2;
}`,
			extension: ".js",
			expected:  "function myGenerator()\n---\n",
		},
		{
			name:      "jsx function component",
			code:      "function App() { return <div>Hello</div>; }",
			extension: ".jsx",
			expected:  "function App()\n---\n",
		},
		{
			name: "jsx class component",
			code: `class App extends React.Component {
  render() {
    return <div>Hello</div>;
  }
}`,
			extension: ".jsx",
			expected: `class App extends React.Component
	render()
---
`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tmpfile, err := os.CreateTemp("", "test*"+tc.extension)
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

			result, err := GetFileSignaturesString(tmpfile.Name())
			if err != nil {
				t.Fatalf("GetFileSignaturesString returned an error: %v", err)
			}

			if result != tc.expected {
				t.Errorf("GetFileSignaturesString returned incorrect result. Expected:\n%s\nGot:\n%s", utils.PanicJSON(tc.expected), utils.PanicJSON(result))
			}
		})
	}
}

func TestGetFileSymbolsStringJavascript(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		code      string
		extension string
		expected  string
	}{
		{
			name: "simple functions",
			code: `function helloWorld() {
	console.log("Hello, world!");
}

function add(a, b) {
	return a + b;
}`,
			extension: ".js",
			expected:  "helloWorld, add",
		},
		{
			name:      "empty",
			code:      "",
			extension: ".js",
			expected:  "",
		},
		{
			name:      "single function",
			code:      "function testFunc() {}",
			extension: ".js",
			expected:  "testFunc",
		},
		{
			name:      "function with arguments",
			code:      "function testFunc(arg1, arg2) { return true }",
			extension: ".js",
			expected:  "testFunc",
		},
		{
			name:      "function with comment",
			code:      "// This is a test function\nfunction testFunc() {}",
			extension: ".js",
			expected:  "testFunc",
		},
		{
			name:      "let declaration",
			code:      "let testLet = 42",
			extension: ".js",
			expected:  "testLet",
		},
		{
			name:      "constant declaration",
			code:      "const testConst = 42",
			extension: ".js",
			expected:  "testConst",
		},
		{
			name:      "var declaration",
			code:      "var testVar = 42",
			extension: ".js",
			expected:  "testVar",
		},
		{
			name:      "single class",
			code:      "class TestClass {}",
			extension: ".js",
			expected:  "TestClass",
		},
		{
			name:      "single class with single method",
			code:      "class TestClass { testMethod() {} }",
			extension: ".js",
			expected:  "TestClass, TestClass.testMethod",
		},
		{
			name:      "single class with multiple methods",
			code:      "class TestClass { testMethod1() {} testMethod2() {} }",
			extension: ".js",
			expected:  "TestClass, TestClass.testMethod1, TestClass.testMethod2",
		},
		{
			name:      "jsx function component",
			code:      "function App() { return <div>Hello</div>; }",
			extension: ".jsx",
			expected:  "App",
		},
		{
			name: "jsx class component",
			code: `class App extends React.Component {
  render() {
    return <div>Hello</div>;
  }
}`,
			extension: ".jsx",
			expected:  "App, App.render",
		},
		{
			name:      "export function",
			code:      "export function greet(name) { return 'Hello ' + name; }",
			extension: ".js",
			expected:  "greet",
		},
		{
			name:      "export const",
			code:      "export const PI = 3.14159;",
			extension: ".js",
			expected:  "PI",
		},
		{
			name:      "export default function",
			code:      "export default function main() { }",
			extension: ".js",
			expected:  "main",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			extension := test.extension
			if extension == "" {
				extension = ".js"
			}
			tmpfile, err := os.CreateTemp("", "*"+extension)
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

func TestGetSymbolDefinitionJavascript(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name               string
		symbolName         string
		code               string
		extension          string
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
		{
			name:       "generator function definition",
			symbolName: "myGenerator",
			code: `function* myGenerator() {
	yield 1;
	yield 2;
	yield 3;
}`,
			expectedDefinition: `function* myGenerator() {
	yield 1;
	yield 2;
	yield 3;
}`,
		},
		{
			name:               "const definition",
			symbolName:         "TestConst",
			code:               `const TestConst = "test";`,
			expectedDefinition: `const TestConst = "test";`,
		},
		{
			name:               "let definition",
			symbolName:         "TestLet",
			code:               `let TestLet = "test";`,
			expectedDefinition: `let TestLet = "test";`,
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
			name:       "exported call expression",
			symbolName: "someFunction",
			code: `somethingElse();

export const x = someFunction({
	foo: 'bar'
});`,
			expectedDefinition: `export const x = someFunction({
	foo: 'bar'
});`,
		},
		{
			name:       "async function definition",
			symbolName: "fetchData",
			code: `async function fetchData(url) {
	return await fetch(url);
}`,
			expectedDefinition: `async function fetchData(url) {
	return await fetch(url);
}`,
		},
		{
			name:       "arrow function const",
			symbolName: "myFunc",
			code: `const myFunc = (a, b) => {
	return a + b;
};`,
			expectedDefinition: `const myFunc = (a, b) => {
	return a + b;
};`,
		},
		{
			name:       "class method definition",
			symbolName: "TestClass.greet",
			code: `class TestClass {
	constructor(name) {
		this.name = name;
	}
	greet() {
		return 'Hello ' + this.name;
	}
}`,
			expectedDefinition: `	greet() {
		return 'Hello ' + this.name;
	}`,
		},
		{
			name:       "export function",
			symbolName: "greet",
			code: `export function greet(name) {
	return 'Hello ' + name;
}`,
			expectedDefinition: `export function greet(name) {
	return 'Hello ' + name;
}`,
		},
		{
			name:       "export default function",
			symbolName: "main",
			code: `export default function main() {
	console.log('main');
}`,
			expectedDefinition: `export default function main() {
	console.log('main');
}`,
		},
		{
			name:       "jsx function component",
			symbolName: "App",
			extension:  "jsx",
			code: `function App() {
	return <div>Hello</div>;
}`,
			expectedDefinition: `function App() {
	return <div>Hello</div>;
}`,
		},
		{
			name:       "jsx class component",
			symbolName: "App",
			extension:  "jsx",
			code: `class App extends React.Component {
	render() {
		return <div>Hello</div>;
	}
}`,
			expectedDefinition: `class App extends React.Component {
	render() {
		return <div>Hello</div>;
	}
}`,
		},
		{
			name:       "jsx class component method",
			symbolName: "App.render",
			extension:  "jsx",
			code: `class App extends React.Component {
	render() {
		return <div>Hello</div>;
	}
}`,
			expectedDefinition: `	render() {
		return <div>Hello</div>;
	}`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ext := tc.extension
			if ext == "" {
				ext = "js"
			}
			filePath, err := utils.WriteTestTempFile(t, ext, tc.code)
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

func TestNormalizeSymbolFromSnippet_Javascript(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "function declaration",
			input:    "function testFunc(arg1, arg2) {",
			expected: "testFunc",
		},
		{
			name:     "async function declaration",
			input:    "async function fetchData(url) {",
			expected: "fetchData",
		},
		{
			name:     "class declaration",
			input:    "class TestClass {",
			expected: "TestClass",
		},
		{
			name:     "class extends",
			input:    "class TestClass extends BaseClass {",
			expected: "TestClass",
		},
		{
			name:     "const declaration",
			input:    "const testConst = 42;",
			expected: "testConst",
		},
		{
			name:     "let declaration",
			input:    "let testLet = 'hello';",
			expected: "testLet",
		},
		{
			name:     "arrow function const",
			input:    "const myFunc = (a, b) => {",
			expected: "myFunc",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			result, err := NormalizeSymbolFromSnippet("javascript", test.input)
			assert.NoError(t, err)
			if !strings.Contains(result, test.expected) {
				t.Errorf("NormalizeSymbolFromSnippet(%q) = %q, expected to contain %q", test.input, result, test.expected)
			}
		})
	}
}

func TestShrinkJavascriptEmbeddedCodeContext(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name     string
		code     string
		expected string
	}{
		{
			name: "class with methods",
			code: `
class TestClass {
    constructor(name) {
        this.name = name;
    }
    
    greet() {
        // pad out length of this code
        console.log("Hello, " + this.name);
        return this.name;
    }
    
    static create(name) {
        return new TestClass(name);
    }
}`,
			expected: `Shrank context - here are the extracted code signatures and docstrings only, in lieu of full code:
` + "```" + `javascript-signatures
class TestClass
	constructor(name)
	greet()
	create(name)
` + "```",
		},
		{
			name: "functions and variables",
			code: `
function processData(data) {
    // pad out length of this code
    const result = data.map(x => x * 2);
    return result;
}

const CONFIG = {
    apiUrl: "https://api.example.com",
    timeout: 5000
};

async function fetchData(url) {
    // pad out length of this code
    const response = await fetch(url);
    return response.json();
}`,
			expected: `Shrank context - here are the extracted code signatures and docstrings only, in lieu of full code:
` + "```" + `javascript-signatures
function processData(data)
const CONFIG = {
    apiUrl: "https://api.example.com",
    timeout: 5000
    [...]
async function fetchData(url)
` + "```",
		},
		{
			name: "jsx component",
			code: `
function MyComponent({ title, children }) {
    // pad out length of this code
    const [count, setCount] = useState(0);
    
    return (
        <div className="container">
            <h1>{title}</h1>
            <button onClick={() => setCount(count + 1)}>
                Count: {count}
            </button>
            {children}
        </div>
    );
}

class ClassComponent extends React.Component {
    render() {
        // pad out length of this code
        return <div>{this.props.message}</div>;
    }
}`,
			expected: `Shrank context - here are the extracted code signatures and docstrings only, in lieu of full code:
` + "```" + `javascript-signatures
function MyComponent({ title, children })
class ClassComponent extends React.Component
	render()
` + "```",
		},
		{
			name: "exports",
			code: `
export function exportedFunc(arg) {
    // pad out length of this code
    console.log(arg);
    return arg;
}

export const EXPORTED_CONST = "value";

export default function defaultExport() {
    // pad out length of this code
    return "default";
}`,
			expected: `Shrank context - here are the extracted code signatures and docstrings only, in lieu of full code:
` + "```" + `javascript-signatures
export function exportedFunc(arg)
export const EXPORTED_CONST = "value";
export default function defaultExport()
` + "```",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			embeddedCode := createMarkdownCodeBlock("javascript", tc.code)
			result, didShrink := ShrinkEmbeddedCodeContext(embeddedCode, false, len(tc.code)-100)

			normalizedCode := strings.TrimSpace(strings.ReplaceAll(tc.code, "\r\n", "\n"))
			normalizedResult := strings.TrimSpace(strings.ReplaceAll(result, "\r\n", "\n"))
			normalizedExpected := strings.TrimSpace(strings.ReplaceAll(tc.expected, "\r\n", "\n"))

			if normalizedCode == normalizedResult {
				assert.False(t, didShrink)
			} else {
				assert.True(t, didShrink)
			}

			if normalizedResult != normalizedExpected {
				t.Errorf("ShrinkEmbeddedCodeContext returned incorrect result.\nExpected:\n%s\n\nGot:\n%s", normalizedExpected, normalizedResult)
			}
		})
	}
}
