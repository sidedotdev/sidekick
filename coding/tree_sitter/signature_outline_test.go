package tree_sitter

import (
	"testing"
)

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
