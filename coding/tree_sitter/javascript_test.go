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
