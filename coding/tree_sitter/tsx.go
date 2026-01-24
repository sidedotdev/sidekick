package tree_sitter

import (
	"strings"

	tree_sitter "github.com/tree-sitter/go-tree-sitter"
)

func writeTsxSignatureCapture(out *strings.Builder, sourceCode *[]byte, c tree_sitter.QueryCapture, name string) {
	writeTypescriptSignatureCapture(out, sourceCode, c, name)
}

// writeTsxSymbolCapture handles TSX symbol captures
func writeTsxSymbolCapture(out *strings.Builder, sourceCode *[]byte, c tree_sitter.QueryCapture, name string) {
	writeTypescriptSymbolCapture(out, sourceCode, c, name)
}
