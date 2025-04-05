package tree_sitter

import (
	"reflect"
	"sidekick/utils"
	"testing"
)

func TestExtractSearchCodeBlocks(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []CodeBlock
	}{
		{
			name:     "Empty input",
			input:    "",
			expected: nil,
		},
		{
			name: "Single file, multiple lines",
			input: `components/Something.vue
20=import Another from './Another.vue'
21:import Woot from './Woot.vue'
37=const calcSomething = computed(() => {`,
			expected: []CodeBlock{
				{
					FilePath:  "components/Something.vue",
					StartLine: 20,
					EndLine:   21,
					Code:      "import Another from './Another.vue'\nimport Woot from './Woot.vue'",
				},
				{
					FilePath:  "components/Something.vue",
					StartLine: 37,
					EndLine:   37,
					Code:      "const calcSomething = computed(() => {",
				},
			},
		},
		{
			name: "Multiple files, single lines",
			input: `omg/Wow.vue
25=import type { Cool } from '../lib/models';
27:interface XParams {
34:interface XResult {
components/Calc.vue
37=const calcSomething = computed(() => {`,
			expected: []CodeBlock{
				{
					FilePath:  "omg/Wow.vue",
					StartLine: 25,
					EndLine:   25,
					Code:      "import type { Cool } from '../lib/models';",
				},
				{
					FilePath:  "omg/Wow.vue",
					StartLine: 27,
					EndLine:   27,
					Code:      "interface XParams {",
				},
				{
					FilePath:  "omg/Wow.vue",
					StartLine: 34,
					EndLine:   34,
					Code:      "interface XResult {",
				},
				{
					FilePath:  "components/Calc.vue",
					StartLine: 37,
					EndLine:   37,
					Code:      "const calcSomething = computed(() => {",
				},
			},
		},
		{
			name: "Multiple files, multiple lines with comments",
			input: `some/file.go
286=func x(y string) (string, error) {
--
290-            // a comment
291:            z, err := foo(y)
292-            if err != nil {
--
another/secondary_file.go
83=func DoSomething(ctx workflow.Context) error {
--
92-
93:            res, err := RunStuff(ctx)
94-            if err != nil {
--`,
			expected: []CodeBlock{
				{
					FilePath:  "some/file.go",
					StartLine: 286,
					EndLine:   286,
					Code:      "func x(y string) (string, error) {",
				},
				{
					FilePath:  "some/file.go",
					StartLine: 290,
					EndLine:   292,
					Code: `            // a comment
            z, err := foo(y)
            if err != nil {`,
				},
				{
					FilePath:  "another/secondary_file.go",
					StartLine: 83,
					EndLine:   83,
					Code:      "func DoSomething(ctx workflow.Context) error {",
				},
				{
					FilePath:  "another/secondary_file.go",
					StartLine: 92,
					EndLine:   94,
					Code: `
            res, err := RunStuff(ctx)
            if err != nil {`,
				},
			},
		},
		{
			name:  "Malformed input - no file path",
			input: "25=import type { Cool } from '../lib/models';",
			expected: []CodeBlock{
				{
					StartLine: 25,
					EndLine:   25,
					Code:      "import type { Cool } from '../lib/models';",
				},
			},
		},
		{
			name: "Malformed input - invalid line number",
			input: `some/file.go
abc=invalid line number`,
			expected: nil,
		},
		{
			name:     "Non-search output - regular code block",
			input:    "```go\nfunc main() {\n\tfmt.Println(\"hello\")\n}\n```",
			expected: nil,
		},

		// TODO /gen add test cases for truncated search output (with and without files listed after)
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractSearchCodeBlocks(tt.input)
			if !reflect.DeepEqual(got, tt.expected) {
				//t.Errorf("expected:\n%v\ngot:\n%v", tt.expected, got)
				t.Errorf("expected:\n%s\ngot:\n%s", utils.PanicJSON(tt.expected), utils.PanicJSON(got))
			}
		})
	}
}

func TestExtractSymbolDefinitionBlocks(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []CodeBlock
	}{
		{
			name:     "Empty input",
			input:    "",
			expected: nil,
		},
		{
			name:     "Input without triple backticks",
			input:    "This is some text without any code blocks.",
			expected: nil,
		},
		{
			name: "Input with no header content",
			input: `
` + "```" + `go
package main
` + "```",
			expected: []CodeBlock{
				{
					HeaderContent: "",
					BlockContent:  "```go\npackage main\n```",
					Code:          "package main",
					FullContent:   "```go\npackage main\n```",
					FilePath:      "",
					StartLine:     0,
					EndLine:       0,
				},
			},
		},
		{
			name: "Input with partial code block",
			input: `File: some/file.go
Lines: 1-3
` + "```" + `go
package main
` + "```",
			expected: []CodeBlock{
				{
					HeaderContent: "File: some/file.go\nLines: 1-3",
					BlockContent:  "```go\npackage main\n```",
					Code:          "package main",
					FullContent:   "File: some/file.go\nLines: 1-3\n```go\npackage main\n```",
					FilePath:      "some/file.go",
					StartLine:     1,
					EndLine:       3,
				},
			},
		},
		{
			name: "Input with complete code block",
			input: `File: some/file.go
Symbol: main
Lines: 1-5
` + "```" + `go
	package main

func main() {
	println("Hello, World!")
}
` + "```",
			expected: []CodeBlock{
				{
					HeaderContent: "File: some/file.go\nSymbol: main\nLines: 1-5",
					BlockContent:  "```go\n\tpackage main\n\nfunc main() {\n\tprintln(\"Hello, World!\")\n}\n```",
					Code:          "\tpackage main\n\nfunc main() {\n\tprintln(\"Hello, World!\")\n}",
					FullContent:   "File: some/file.go\nSymbol: main\nLines: 1-5\n```go\n\tpackage main\n\nfunc main() {\n\tprintln(\"Hello, World!\")\n}\n```",
					FilePath:      "some/file.go",
					Symbol:        "main",
					StartLine:     1,
					EndLine:       5,
				},
			},
		},
		{
			name: "Input with backticks in the middle of the code block but not at the start of a line",
			input: `File: some/file.go
Symbol: main
Lines: 1-5
` + "```" + `go
package main

func main() {
	println("` + "```" + `")
}
` + "```",
			expected: []CodeBlock{
				{
					HeaderContent: "File: some/file.go\nSymbol: main\nLines: 1-5",
					BlockContent:  "```go\npackage main\n\nfunc main() {\n\tprintln(\"```\")\n}\n```",
					Code:          "package main\n\nfunc main() {\n\tprintln(\"```\")\n}",
					FullContent:   "File: some/file.go\nSymbol: main\nLines: 1-5\n```go\npackage main\n\nfunc main() {\n\tprintln(\"```\")\n}\n```",
					FilePath:      "some/file.go",
					Symbol:        "main",
					StartLine:     1,
					EndLine:       5,
				},
			},
		},
		{
			name: "Input with multiple code blocks",
			input: `File: some/file.go
Lines: 1-5
` + "```" + `go
package main

import (
	"fmt"
)
` + "```" + `
File: some/file.go
Symbol: main
Lines: 7-11
` + "```" + `go
func main() {
	fmt.Println("Hello, World!")
}
` + "```",
			expected: []CodeBlock{
				{
					HeaderContent: "File: some/file.go\nLines: 1-5",
					BlockContent:  "```go\npackage main\n\nimport (\n\t\"fmt\"\n)\n```",
					Code:          "package main\n\nimport (\n\t\"fmt\"\n)",
					FullContent:   "File: some/file.go\nLines: 1-5\n```go\npackage main\n\nimport (\n\t\"fmt\"\n)\n```",
					FilePath:      "some/file.go",
					StartLine:     1,
					EndLine:       5,
				},
				{
					HeaderContent: "File: some/file.go\nSymbol: main\nLines: 7-11",
					BlockContent:  "```go\nfunc main() {\n\tfmt.Println(\"Hello, World!\")\n}\n```",
					Code:          "func main() {\n\tfmt.Println(\"Hello, World!\")\n}",
					FullContent:   "File: some/file.go\nSymbol: main\nLines: 7-11\n```go\nfunc main() {\n\tfmt.Println(\"Hello, World!\")\n}\n```",
					FilePath:      "some/file.go",
					Symbol:        "main",
					StartLine:     7,
					EndLine:       11,
				},
			},
		},
		{
			name: "Codeblock for a markdown file without language",
			input: `
			File: README.md
Lines: 1-3 (full file)
` + "```" + `
# Sidekick

Blah blah
` + "```",
			expected: []CodeBlock{
				{
					HeaderContent: "File: README.md\nLines: 1-3 (full file)",
					BlockContent:  "```\n# Sidekick\n\nBlah blah\n```",
					Code:          "# Sidekick\n\nBlah blah",
					FullContent:   "File: README.md\nLines: 1-3 (full file)\n```\n# Sidekick\n\nBlah blah\n```",
					FilePath:      "README.md",
					StartLine:     1,
					EndLine:       3,
				},
			},
		},
		{
			name: "Codeblock for a markdown file with language",
			input: `
			File: README.md
Lines: 1-3 (full file)
` + "```md" + `
# Sidekick

Blah blah
` + "```",
			expected: []CodeBlock{
				{
					HeaderContent: "File: README.md\nLines: 1-3 (full file)",
					BlockContent:  "```md\n# Sidekick\n\nBlah blah\n```",
					Code:          "# Sidekick\n\nBlah blah",
					FullContent:   "File: README.md\nLines: 1-3 (full file)\n```md\n# Sidekick\n\nBlah blah\n```",
					FilePath:      "README.md",
					StartLine:     1,
					EndLine:       3,
				},
			},
		},
		{
			name: "Some funky input",
			input: `File: gen_todo.md
Lines: 1-8
` + "```" + `
README.md
3:TODO: Explain Sidekick
agent/agent.go
12=type AgentAction struct {
15:     Data    interface{} // TODO blah1
api/api.go
3=import (
32:// TODO blah2
` + "```",
			expected: []CodeBlock{
				{
					HeaderContent: "File: gen_todo.md\nLines: 1-8",
					BlockContent:  "```\nREADME.md\n3:TODO: Explain Sidekick\nagent/agent.go\n12=type AgentAction struct {\n15:     Data    interface{} // TODO blah1\napi/api.go\n3=import (\n32:// TODO blah2\n```",
					Code:          "README.md\n3:TODO: Explain Sidekick\nagent/agent.go\n12=type AgentAction struct {\n15:     Data    interface{} // TODO blah1\napi/api.go\n3=import (\n32:// TODO blah2",
					FullContent:   "File: gen_todo.md\nLines: 1-8\n```\nREADME.md\n3:TODO: Explain Sidekick\nagent/agent.go\n12=type AgentAction struct {\n15:     Data    interface{} // TODO blah1\napi/api.go\n3=import (\n32:// TODO blah2\n```",
					FilePath:      "gen_todo.md",
					StartLine:     1,
					EndLine:       8,
				},
			},
		},
		{
			name: "Input with shrunk code blocks ignores shrunk content",
			input: `File: some/other_file.go
Lines: 1-5
Shrank the following code context and removed backticks
func x()
func y()

File: some/file.go
Symbol: main
Lines: 7-11
` + "```" + `go
func main() {
	fmt.Println("Hello, World!")
}
` + "```",
			expected: []CodeBlock{
				{
					HeaderContent: "File: some/file.go\nSymbol: main\nLines: 7-11",
					BlockContent:  "```go\nfunc main() {\n\tfmt.Println(\"Hello, World!\")\n}\n```",
					Code:          "func main() {\n\tfmt.Println(\"Hello, World!\")\n}",
					FullContent:   "File: some/file.go\nSymbol: main\nLines: 7-11\n```go\nfunc main() {\n\tfmt.Println(\"Hello, World!\")\n}\n```",
					FilePath:      "some/file.go",
					Symbol:        "main",
					StartLine:     7,
					EndLine:       11,
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractSymbolDefinitionBlocks(tt.input)
			if !reflect.DeepEqual(got, tt.expected) {
				//t.Errorf("expected:\n%v\ngot:\n%v", tt.expected, got)
				t.Errorf("expected:\n%s\ngot:\n%s", utils.PrettyJSON(tt.expected), utils.PrettyJSON(got))
			}
		})
	}
}
