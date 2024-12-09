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
