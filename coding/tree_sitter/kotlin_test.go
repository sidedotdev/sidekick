package tree_sitter

import (
	"os"
	"strings"
	"testing"

	"sidekick/utils"

	"github.com/stretchr/testify/assert"
)

func TestGetFileSignaturesStringKotlin(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name: "simple classes",
			input: `
class Empty
class Empty2 {}
`,
			expected: `class Empty
---
class Empty2
---
`,
		},
		{
			name: "class with methods",
			input: `
class Hello {
  fun a() {}
  fun b() {}
}
`,
			expected: `class Hello
	fun a()
	fun b()
---
`,
		},
		{
			name: "generic class",
			input: `
class Container<T> {}
`,
			expected: `class Container<T>
---
`,
		},
		{
			name: "class with properties",
			input: `
class Something {
  val x: Int = 4
  var y: Int?
}
`,
			expected: `class Something
	val x: Int = 4
	var y: Int?
---
`,
		},
		{
			name: "enum class",
			input: `
enum class Color(val rgb: Int) {
  RED(0xFF0000),
  GREEN(0x00FF00)
}
`,
			expected: `enum class Color(val rgb: Int)
	RED
	GREEN
---
`,
		},
		{
			name: "value class",
			input: `
@JvmInline
value class Password(private val s: String)
`,
			expected: `@JvmInline
value class Password(private val s: String)
---
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a temporary file with the test case code
			tmpfile, err := os.CreateTemp("", "test*.kt")
			if err != nil {
				t.Fatalf("Failed to create temp file: %v", err)
			}
			defer os.Remove(tmpfile.Name())

			if _, err := tmpfile.Write([]byte(tt.input)); err != nil {
				t.Fatalf("Failed to write to temp file: %v", err)
			}
			if err := tmpfile.Close(); err != nil {
				t.Fatalf("Failed to close temp file: %v", err)
			}

			// Call GetFileSignatures with the path to the temp file
			result, err := GetFileSignaturesString(tmpfile.Name())
			assert.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGetFileSymbolsStringKotlin(t *testing.T) {
	tests := []struct {
		name     string
		code     string
		expected string
	}{
		{
			name: "simple classes",
			code: `
class Empty
class Empty2 {}
`,
			expected: "Empty, Empty2",
		},
		{
			name: "class with methods",
			code: `
class Hello {
  fun a() {}
  fun b() {}
}
`,
			expected: "Hello, a, b",
		},
		{
			name: "generic class",
			code: `
class Container<T> {}
`,
			expected: "Container",
		},
		{
			name: "class with properties",
			code: `
class Something {
  val x: Int = 4
  var y: Int?
}
`,
			expected: "Something",
		},
		{
			name: "enum class",
			code: `
enum class Color(val rgb: Int) {
  RED(0xFF0000),
  GREEN(0x00FF00)
}
`,
			expected: "Color, RED, GREEN",
		},
		{
			name: "value class",
			code: `
@JvmInline
value class Password(private val s: String)
`,
			expected: "Password",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			tmpfile, err := os.CreateTemp("", "*.kt")
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

			assert.Equal(t, test.expected, symbolsString)
		})
	}
}

func TestGetSymbolDefinitionKotlin(t *testing.T) {
	testCases := []struct {
		name               string
		symbolName         string
		code               string
		expectedDefinition string
		expectedError      string
	}{
		{
			name:          "empty code",
			symbolName:    "TestClass",
			code:          "",
			expectedError: `symbol not found: TestClass`,
		},
		{
			name:       "basic class definition",
			symbolName: "TestClass",
			code: `class TestClass {
    private val name: String = ""
}`,
			expectedDefinition: `class TestClass {
    private val name: String = ""
}`,
		},
		{
			name:       "class with method",
			symbolName: "TestClass",
			code: `class TestClass {
    fun testMethod() {
        println("Hello")
    }
}`,
			expectedDefinition: `class TestClass {
    fun testMethod() {
        println("Hello")
    }
}`,
		},
		{
			name:       "method definition",
			symbolName: "testMethod",
			code: `class TestClass {
    fun testMethod() {
        println("Hello")
    }
}`,
			expectedDefinition: `    fun testMethod() {
        println("Hello")
    }`,
		},
		{
			name:          "symbol not found",
			symbolName:    "NonExistentSymbol",
			code:          "class SomeClass {}",
			expectedError: `symbol not found: NonExistentSymbol`,
		},
		{
			name:       "enum class definition",
			symbolName: "TestEnum",
			code: `enum class TestEnum {
    ONE,
    TWO(2),
    THREE
}`,
			expectedDefinition: `enum class TestEnum {
    ONE,
    TWO(2),
    THREE
}`,
		},
		{
			name:       "enum entry definition",
			symbolName: "TWO",
			code: `enum class TestEnum {
    ONE,
    TWO(2),
    THREE
}`,
			expectedDefinition: `    TWO(2),`,
		},
		{
			name:       "class with single line doc comments",
			symbolName: "DocClass",
			code: `// This is a documented class
// with multiple line comments
class DocClass {
    private val name: String = ""
}`,
			expectedDefinition: `// This is a documented class
// with multiple line comments
class DocClass {
    private val name: String = ""
}`,
		},
		{
			name:       "class with multi-line doc comments",
			symbolName: "DocClass2",
			code: `/* This is a documented class
 * with multiple lines
 * in a block comment
 */
class DocClass2 {
    private val name: String = ""
}`,
			expectedDefinition: `/* This is a documented class
 * with multiple lines
 * in a block comment
 */
class DocClass2 {
    private val name: String = ""
}`,
		},
		{
			name:       "method with single line doc comments",
			symbolName: "docMethod",
			code: `class TestClass {
    // This method does something
    // really important
    fun docMethod() {
        println("Hello")
    }
}`,
			expectedDefinition: `    // This method does something
    // really important
    fun docMethod() {
        println("Hello")
    }`,
		},
		{
			name:       "method with multi-line doc comments",
			symbolName: "docMethod2",
			code: `class TestClass {
    /* This method does something
     * really important
     * in a very special way
     */
    fun docMethod2() {
        println("Hello")
    }
}`,
			expectedDefinition: `    /* This method does something
     * really important
     * in a very special way
     */
    fun docMethod2() {
        println("Hello")
    }`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			filePath, err := utils.WriteTestTempFile(t, "kt", tc.code)
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
