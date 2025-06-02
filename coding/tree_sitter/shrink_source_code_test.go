package tree_sitter

import (
	"regexp"
	"sidekick/utils"
	"strings"
	"testing"
)

var newlinesRegex = regexp.MustCompile(`(\s*\n)+`)

func normalizeWhitespace(content string) string {
	return newlinesRegex.ReplaceAllString(strings.TrimSpace(content), "\n")
}

func createMarkdownCodeBlock(language, code string) string {
	return "```" + language + "\n" + code + "\n```"
}

var longFunction = `func long() {
	fmt.Println("hello testing 123 and this is a long line of code very long")
	fmt.Println("hello testing 123 and this is a long line of code very long")
	fmt.Println("hello testing 123 and this is a long line of code very long")
	fmt.Println("hello testing 123 and this is a long line of code very long")
	fmt.Println("hello testing 123 and this is a long line of code very long")
	fmt.Println("hello testing 123 and this is a long line of code very long")
	fmt.Println("hello testing 123 and this is a long line of code very long")
	fmt.Println("hello testing 123 and this is a long line of code very long")
	fmt.Println("hello testing 123 and this is a long line of code very long")
	fmt.Println("hello testing 123 and this is a long line of code very long")
}`

var lessLongFunction = `func lessLong() {
	fmt.Println("less long 123 and this is kinda long")
	fmt.Println("less long 123 and this is kinda long")
	fmt.Println("less long 123 and this is kinda long")
	fmt.Println("less long 123 and this is kinda long")
}`

func TestShrinkEmbeddedCodeContext(t *testing.T) {
	tests := []struct {
		name          string
		input         string
		maxLength     int
		longestFirst  bool
		wantContent   string
		wantDidShrink bool
	}{
		{
			name:          "empty content",
			input:         "",
			maxLength:     100,
			longestFirst:  true,
			wantContent:   "",
			wantDidShrink: false,
		},
		{
			name:          "content under max length",
			input:         "Some text without code blocks",
			maxLength:     100,
			longestFirst:  true,
			wantContent:   "Some text without code blocks",
			wantDidShrink: false,
		},
		{
			name:          "single code block under max length",
			input:         "Some text\n" + createMarkdownCodeBlock("go", "func test() {\n\treturn\n}") + "\nMore text",
			maxLength:     100,
			longestFirst:  true,
			wantContent:   "Some text\n" + createMarkdownCodeBlock("go", "func test() {\n\treturn\n}") + "\nMore text",
			wantDidShrink: false,
		},
		// TODO /gen/basic test case for avoiding "shrinking" a function that
		// has a very short body, one that is shorter than the hint itself,
		// which would actually enlarge it instead of shrink it (note: the
		// function doesn't handle this case properly yet)
		{
			name: "duplicate code blocks",
			input: createMarkdownCodeBlock("go", "func test() {\n\treturn\n}") + "\n" +
				"Some text\n" +
				createMarkdownCodeBlock("go", "func test() {\n\treturn\n}"),
			maxLength:     50, // Small enough to trigger deduplication, but not true shrinking (dedupe brings it below max length)
			longestFirst:  true,
			wantContent:   "[...]\nSome text\n" + createMarkdownCodeBlock("go", "func test() {\n\treturn\n}"),
			wantDidShrink: false, // deduplication is not shrinking
		},
		{
			name: "multiple different blocks under max length",
			input: createMarkdownCodeBlock("go", "func test1() {}") + "\n" +
				"Some text\n" +
				createMarkdownCodeBlock("python", "def test2():\n    pass"),
			maxLength:    1000,
			longestFirst: true,
			wantContent: createMarkdownCodeBlock("go", "func test1() {}") + "\n" +
				"Some text\n" +
				createMarkdownCodeBlock("python", "def test2():\n    pass"),
			wantDidShrink: false,
		},
		{
			name: "shrinks the blocks that are longest first when longestFirst=true",
			input: createMarkdownCodeBlock("go", longFunction) + "\n" +
				"Some text\n" +
				createMarkdownCodeBlock("go", lessLongFunction),
			maxLength:    500, // enough to shrink just the long function
			longestFirst: true,
			wantContent: "Shrank context - here are the extracted code signatures and docstrings only, in lieu of full code:\n" +
				createMarkdownCodeBlock("go-signatures", "func long()") + "\n" +
				"Some text\n" +
				createMarkdownCodeBlock("go", lessLongFunction),
			wantDidShrink: true,
		},
		{
			name: "shrinks in reverse order when longestFirst=false",
			input: createMarkdownCodeBlock("go", longFunction) + "\n" +
				"Some text\n" +
				createMarkdownCodeBlock("go", lessLongFunction),
			maxLength:    1000, // enough to shrink just the lessLong function
			longestFirst: false,
			wantContent: createMarkdownCodeBlock("go", longFunction) + "\n" +
				"Some text\n" +
				"Shrank context - here are the extracted code signatures and docstrings only, in lieu of full code:\n" +
				createMarkdownCodeBlock("go-signatures", "func lessLong()"),
			wantDidShrink: true,
		},
		{
			name:          "unsupported language remains unchanged",
			input:         createMarkdownCodeBlock("ruby", "def hello\n  puts 'world'\nend"),
			maxLength:     10,
			longestFirst:  true,
			wantContent:   createMarkdownCodeBlock("ruby", "def hello\n  puts 'world'\nend"),
			wantDidShrink: false,
		},
		{
			name: "mixed supported and unsupported languages",
			input: createMarkdownCodeBlock("ruby", "def hello\n  puts 'world'\nend") + "\n" +
				createMarkdownCodeBlock("go", "func main() {\n\tfmt.Println(\"hello\")\n}"),
			maxLength:    40,
			longestFirst: true,
			wantContent: createMarkdownCodeBlock("ruby", "def hello\n  puts 'world'\nend") + "\n" +
				"Shrank context - here are the extracted code signatures and docstrings only, in lieu of full code:\n" +
				createMarkdownCodeBlock("go-signatures", "func main()"),
			wantDidShrink: true,
		},
		{
			name: "python with docstrings and comments",
			input: createMarkdownCodeBlock("python", `"""
Module docstring
Multiple lines
"""

def function():
    """
    Function docstring
    Multiple lines
    """
    # Comment 1
    print("hello")  # Comment 2
    return True  # Comment 3`),
			maxLength:    200,
			longestFirst: true,
			wantContent: "Shrank context - here are the extracted code signatures and docstrings only, in lieu of full code:\n" +
				createMarkdownCodeBlock("python-signatures", `def function()
	"""
    Function docstring
    Multiple lines
    """`),
			wantDidShrink: true,
		},
		{
			name: "keeps comments when they fit",
			input: createMarkdownCodeBlock("go", `// Function comment
// Multiple lines
func main() {
	// Comment 1
	fmt.Println("hello") // Comment 2
	fmt.Println("abc 123 this is a comment to get over threshold length")
	return  // Comment 3
}`),
			maxLength:    190,
			longestFirst: true,
			wantContent: "Shrank context - here are the extracted code signatures and docstrings only, in lieu of full code:\n" +
				createMarkdownCodeBlock("go-signatures", "// Function comment\n// Multiple lines\nfunc main()"),
			wantDidShrink: true,
		},
		{
			name:         "remove all comments when still too long",
			input:        createMarkdownCodeBlock("go", "// Function comment\n// Multiple lines\nfunc main() {\n\t// Comment 1\n\tfmt.Println(\"hello\")  // Comment 2\n\treturn  // Comment 3\n}"),
			maxLength:    20,
			longestFirst: true,
			wantContent: "Shrank context - here are the extracted code signatures only, in lieu of full code:\n" +
				createMarkdownCodeBlock("go-signatures", "func main()"),
			wantDidShrink: true,
		},
		{
			name: "removes header lines and header comments",
			input: createMarkdownCodeBlock("go", `// Package comment
package main

// Func comment
func main() {
	// Comment 1
	fmt.Println("hello") // Comment 2
	fmt.Println("abc 123 this is a comment to get over threshold length")
	return  // Comment 3
}`),
			maxLength:    200,
			longestFirst: true,
			wantContent: "Shrank context - here are the extracted code signatures and docstrings only, in lieu of full code:\n" +
				createMarkdownCodeBlock("go-signatures", "// Func comment\nfunc main()"),
			wantDidShrink: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotContent, gotDidShrink := ShrinkEmbeddedCodeContext(tt.input, tt.longestFirst, tt.maxLength)
			if normalizeWhitespace(gotContent) != normalizeWhitespace(tt.wantContent) {
				t.Errorf("%s: mismatch\nwant:\n%s\ngot:\n%s", tt.name, tt.wantContent, gotContent)
				t.Errorf("%s: json-formatted mismatch\nwant:\n%s\ngot:\n%s", tt.name, utils.PanicJSON(tt.wantContent), utils.PanicJSON(gotContent))
			}
			if gotDidShrink != tt.wantDidShrink {
				t.Errorf("%s: didShrink mismatch: want %v, got %v", tt.name, tt.wantDidShrink, gotDidShrink)
			}

		})
	}
}

// TODO move to golang_test.go
func TestRemoveCommentsGolang(t *testing.T) {
	testCases := []struct {
		name              string
		sourceCode        SourceCode
		expectedDidRemove bool
		expectedContent   string
	}{
		{
			name: "single line comment",
			sourceCode: SourceCode{
				LanguageName:         "go",
				OriginalLanguageName: "go",
				Content: `package main
// This is a comment
func main() {
	println("Hello, World!")
}
`,
			},
			expectedDidRemove: true,
			expectedContent: `package main
func main() {
	println("Hello, World!")
}
`,
		},
		{
			name: "multi-line comment",
			sourceCode: SourceCode{
				LanguageName:         "go",
				OriginalLanguageName: "go",
				Content: `package main
/* This is a
multi-line comment */
func main() {
	println("Hello, World!")
}
`,
			},
			expectedDidRemove: true,
			expectedContent: `package main
func main() {
	println("Hello, World!")
}
`,
		},
		{
			name: "mixed comments",
			sourceCode: SourceCode{
				LanguageName:         "go",
				OriginalLanguageName: "go",
				Content: `package main
// Single line comment
/* Multi-line
comment */
func main() {
	println("Hello, World!")
}
`,
			},
			expectedDidRemove: true,
			expectedContent: `package main
func main() {
	println("Hello, World!")
}
`,
		},
		{
			name: "empty source code",
			sourceCode: SourceCode{
				LanguageName:         "go",
				OriginalLanguageName: "go",
				Content:              ``,
			},
			expectedDidRemove: false,
			expectedContent:   ``,
		},
		{
			name: "non-empty source code without comments",
			sourceCode: SourceCode{
				LanguageName:         "go",
				OriginalLanguageName: "go",
				Content: `package main
func main() {
	println("Hello, World!")
}
`,
			},
			expectedDidRemove: false,
			expectedContent: `package main
func main() {
	println("Hello, World!")
}
`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			didRemove, result := removeComments(tc.sourceCode)
			if didRemove != tc.expectedDidRemove {
				t.Errorf("Expected didRemove: %t Got: %t", tc.expectedDidRemove, didRemove)
			}
			if normalizeWhitespace(result.Content) != normalizeWhitespace(tc.expectedContent) {
				t.Errorf("Expected: \n%s\nGot:\n%s", normalizeWhitespace(tc.expectedContent), normalizeWhitespace(result.Content))
				t.Errorf("\nExpected: %s\nGot_____: %s", utils.PanicJSON(normalizeWhitespace(tc.expectedContent)), utils.PanicJSON(normalizeWhitespace(result.Content)))
			}
		})
	}
}

// TODO move to python_test.go
func TestRemoveCommentsPython(t *testing.T) {
	testCases := []struct {
		name              string
		sourceCode        SourceCode
		expectedDidRemove bool
		expectedContent   string
	}{
		{
			name: "single line comment",
			sourceCode: SourceCode{
				LanguageName:         "python",
				OriginalLanguageName: "python",
				Content: `# This is a comment
def main():
    print("Hello, World!")
`,
			},
			expectedDidRemove: true,
			expectedContent: `def main():
    print("Hello, World!")
`,
		},
		{
			name: "multi-line comment at top-level",
			sourceCode: SourceCode{
				LanguageName:         "python",
				OriginalLanguageName: "python",
				Content: `"""
This is a
multi-line comment
"""
def main():
    print("Hello, World!")
`,
			},
			expectedDidRemove: true,
			expectedContent: `def main():
    print("Hello, World!")
`,
		},
		{
			name: "multi-line comment as function docstring",
			sourceCode: SourceCode{
				LanguageName:         "python",
				OriginalLanguageName: "python",
				Content: `
def main():
	"""
	This is a
	multi-line comment
	"""
    print("Hello, World!")
`,
			},
			expectedDidRemove: true,
			expectedContent: `
def main():
    print("Hello, World!")
`,
		},
		{
			name: "class docstring",
			sourceCode: SourceCode{
				LanguageName:         "python",
				OriginalLanguageName: "python",
				Content: `
class MyClass:
	"""
	This is a class docstring.
	"""
	def __init__(self): pass
		`,
			},
			expectedDidRemove: true,
			expectedContent: `
class MyClass:
	def __init__(self): pass
		`,
		},
		{
			name: "method docstring",
			sourceCode: SourceCode{
				LanguageName:         "python",
				OriginalLanguageName: "python",
				Content: `
class MyClass:
	def my_method(self):
		"""
		This is a method docstring.
		"""
		print("Hello, World!")
		`,
			},
			expectedDidRemove: true,
			expectedContent: `
class MyClass:
	def my_method(self):
		print("Hello, World!")
		`,
		},
		{
			name: "mixed comments",
			sourceCode: SourceCode{
				LanguageName:         "python",
				OriginalLanguageName: "python",
				Content: `
# Single line comment
"""
Multi-line
"""
def main():
	"""
	Docstring
	"""
    print("Hello, World!")
`,
			},
			expectedDidRemove: true,
			expectedContent: `
def main():
    print("Hello, World!")
`,
		},
		{
			name: "empty source code",
			sourceCode: SourceCode{
				LanguageName:         "python",
				OriginalLanguageName: "python",
				Content:              ``,
			},
			expectedDidRemove: false,
			expectedContent:   ``,
		},
		{
			name: "non-empty source code without comments",
			sourceCode: SourceCode{
				LanguageName:         "python",
				OriginalLanguageName: "python",
				Content: `
def main():
    print("Hello, World!")
`,
			},
			expectedDidRemove: false,
			expectedContent: `
def main():
    print("Hello, World!")
`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			didRemove, result := removeComments(tc.sourceCode)
			if didRemove != tc.expectedDidRemove {
				t.Errorf("Expected didRemove: %t Got: %t", tc.expectedDidRemove, didRemove)
			}
			if normalizeWhitespace(result.Content) != normalizeWhitespace(tc.expectedContent) {
				t.Errorf("Expected: \n%s\nGot:\n%s", normalizeWhitespace(tc.expectedContent), normalizeWhitespace(result.Content))
				t.Errorf("\nExpected: %s\nGot_____: %s", utils.PanicJSON(normalizeWhitespace(tc.expectedContent)), utils.PanicJSON(normalizeWhitespace(result.Content)))
			}
		})
	}
}

func TestRemoveCommentsPythonSignatures(t *testing.T) {
	testCases := []struct {
		name              string
		sourceCode        SourceCode
		expectedDidRemove bool
		expectedContent   string
	}{
		{
			name: "single line comment",
			sourceCode: SourceCode{
				LanguageName:         "python",
				OriginalLanguageName: "python-signatures",
				Content: `
# This is a comment
def main()
`,
			},
			expectedDidRemove: true,
			expectedContent: `
def main()
`,
		},
		{
			name: "multi-line comment at top-level",
			sourceCode: SourceCode{
				LanguageName:         "python",
				OriginalLanguageName: "python-signatures",
				Content: `
"""
This is a
multi-line comment
"""
def main()
`,
			},
			expectedDidRemove: true,
			expectedContent: `
def main()
`,
		},
		{
			name: "multi-line comment as function docstring",
			sourceCode: SourceCode{
				LanguageName:         "python",
				OriginalLanguageName: "python-signatures",
				Content: `
def main()
	"""
	This is a
	multi-line comment
	"""
`,
			},
			expectedDidRemove: true,
			expectedContent: `
def main()
`,
		},

		{
			name: "class docstring",
			sourceCode: SourceCode{
				LanguageName:         "python",
				OriginalLanguageName: "python-signatures",
				Content: `
class MyClass
	"""
	This is a class docstring.
	"""
	def __init__(self)
		`,
			},
			expectedDidRemove: true,
			expectedContent: `
class MyClass
	def __init__(self)
		`,
		},
		{
			name: "method docstring",
			sourceCode: SourceCode{
				LanguageName:         "python",
				OriginalLanguageName: "python-signatures",
				Content: `
class MyClass
	def my_method(self)
		"""
		This is a method docstring.
		"""
	def my_method2(self)
		"""
		This is another method docstring.
		"""
		`,
			},
			expectedDidRemove: true,
			expectedContent: `
class MyClass
	def my_method(self)
	def my_method2(self)
		`,
		},
		{
			name: "mixed comments",
			sourceCode: SourceCode{
				LanguageName:         "python",
				OriginalLanguageName: "python-signatures",
				Content: `
# Single line comment
"""
Multi-line
"""
def main()
	"""
	Docstring
	"""
`,
			},
			expectedDidRemove: true,
			expectedContent: `
def main()
`,
		},
		{
			name: "empty source code",
			sourceCode: SourceCode{
				LanguageName:         "python",
				OriginalLanguageName: "python-signatures",
				Content:              ``,
			},
			expectedDidRemove: false,
			expectedContent:   ``,
		},
		{
			name: "non-empty source code without comments",
			sourceCode: SourceCode{
				LanguageName:         "python",
				OriginalLanguageName: "python-signatures",
				Content: `
def main()
`,
			},
			expectedDidRemove: false,
			expectedContent: `
def main()
`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			didRemove, result := removeComments(tc.sourceCode)
			if didRemove != tc.expectedDidRemove {
				t.Errorf("Expected didRemove: %t Got: %t", tc.expectedDidRemove, didRemove)
			}
			if normalizeWhitespace(result.Content) != normalizeWhitespace(tc.expectedContent) {
				t.Errorf("Expected: \n%s\nGot:\n%s", normalizeWhitespace(tc.expectedContent), normalizeWhitespace(result.Content))
				t.Errorf("\nExpected: %s\nGot_____: %s", utils.PanicJSON(normalizeWhitespace(tc.expectedContent)), utils.PanicJSON(normalizeWhitespace(result.Content)))
			}
		})
	}
}
