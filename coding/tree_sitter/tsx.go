package tree_sitter

import (
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
)

func writeTsxSignatureCapture(out *strings.Builder, sourceCode *[]byte, c sitter.QueryCapture, name string) {
	writeTypescriptSignatureCapture(out, sourceCode, c, name)
}

// writeTsxSymbolCapture handles TSX symbol captures
func writeTsxSymbolCapture(out *strings.Builder, sourceCode *[]byte, c sitter.QueryCapture, name string) {
	writeTypescriptSymbolCapture(out, sourceCode, c, name)
}
