package tree_sitter

import (
	"testing"
)

const search = "<<<<<<< SEARCH_EXACT"
const divider = "======="
const replace = ">>>>>>> REPLACE_EXACT"

func symbolToStringSlice(symbols []Symbol) []string {
	var strSlice []string
	for _, symbol := range symbols {
		strSlice = append(strSlice, symbol.Content)
	}
	return strSlice
}

// TODO use this for examples to use to test ExtractSourceCodes
func TestSymbolizeEmbeddedCode(t *testing.T) {
	t.Parallel()
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

` + "```" + `go
SomeStruct
` + "```" + `

Some postamble
`,
		},
		{
			name: "Triple Backticks inside a code block",
			input: `
Some preamble

` + "```" + `go
var aString = "` + "```" + `"
` + "```" + `

Some postamble
`,
			expected: `
Some preamble

` + "```" + `go
aString
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

` + "```" + `go
SomeFunc, SomeStruct
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

` + "```" + `typescript
someFunc, SomeInterface
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

` + "```" + `python
some_func, SomeClass
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

` + "```" + `go
SomeFunc, SomeStruct
` + "```" + `

` + "```" + `typescript
someFunc, SomeInterface
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
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			result := SymbolizeEmbeddedCode(tc.input)
			if result != tc.expected {
				t.Errorf("expected %q, got %q", tc.expected, result)
			}

			// re-symbolize the result to ensure it's idempotent
			result2 := SymbolizeEmbeddedCode(result)
			if result2 != tc.expected {
				t.Errorf("expected %q, got %q", tc.expected, result2)
			}
		})
	}
}
