package tree_sitter

import (
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
)

// writeKotlinSignatureCapture captures Kotlin signature information from source code.
// This is a stub implementation.
func writeKotlinSignatureCapture(out *strings.Builder, sourceCode *[]byte, c sitter.QueryCapture, name string) {
	out.WriteString("// kotlin signature capture not implemented yet")
}

// writeKotlinSymbolCapture captures Kotlin symbol information from source code.
// This is a stub implementation.
func writeKotlinSymbolCapture(out *strings.Builder, sourceCode *[]byte, c sitter.QueryCapture, name string) {
	out.WriteString("// kotlin symbol capture not implemented yet")
}
