package tree_sitter

import (
	"os"
	"testing"

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
