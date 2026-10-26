package persisted_ai

import (
	"sidekick/coding/tree_sitter"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFilterVisibleCodeBlocksForFiles(t *testing.T) {
	t.Parallel()
	blocks := []tree_sitter.CodeBlock{
		{FilePath: "a.go", StartLine: 1, EndLine: 5, Symbol: "Foo", Code: "func Foo() {}", FullContent: "full", BlockContent: "block", HeaderContent: "header"},
		{FilePath: "b.go", StartLine: 1, EndLine: 2, Code: "package b"},
		{FilePath: "a.go", StartLine: 1, EndLine: 5, Symbol: "Foo", Code: "func Foo() {} // seen again in a later message"},
		{FilePath: "a.go", StartLine: 10, EndLine: 12, Code: "x"},
	}

	got := filterVisibleCodeBlocksForFiles(blocks, []string{"a.go", "c.go"})

	assert.Equal(t, []tree_sitter.CodeBlock{
		{FilePath: "a.go", StartLine: 1, EndLine: 5, Symbol: "Foo"},
		{FilePath: "a.go", StartLine: 10, EndLine: 12},
	}, got, "only requested files remain, code payloads are stripped and duplicates collapse")

	assert.Empty(t, filterVisibleCodeBlocksForFiles(blocks, nil))
}
