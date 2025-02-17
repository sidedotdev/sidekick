package tree_sitter

import (
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
)

// writeKotlinSignatureCapture captures Kotlin signature information from source code.
func writeKotlinSignatureCapture(out *strings.Builder, sourceCode *[]byte, c sitter.QueryCapture, name string) {
	content := c.Node.Content(*sourceCode)
	switch name {
	case "class.declaration":
		writeKotlinIndentLevel(c.Node, out)
		maybeEnum := c.Node.Child(0)
		maybeModifiers := c.Node.Child(0)
		if maybeModifiers != nil && maybeModifiers.Type() == "modifiers" {
			out.WriteString(maybeModifiers.Content(*sourceCode))
			out.WriteString(" ")
			maybeEnum = c.Node.Child(1)
		}
		if maybeEnum != nil && maybeEnum.Type() == "enum" {
			out.WriteString("enum ")
		}
		out.WriteString("class ")
	case "class.name", "class.method.name":
		out.WriteString(content)
	case "class.primary_constructor":
		out.WriteString(content)
	case "class.type_parameters", "class.method.parameters":
		out.WriteString(content)
	case "class.method.declaration":
		out.WriteString("\n")
		writeKotlinIndentLevel(c.Node, out)
		out.WriteString("fun ")
	case "class.property.declaration":
		out.WriteString("\n")
		writeKotlinIndentLevel(c.Node, out)
		out.WriteString(content)
	case "class.enum_entry.name":
		out.WriteString("\n")
		writeKotlinIndentLevel(c.Node, out)
		out.WriteString(content)
	case "class.body":
		// Do nothing here as we want to handle each inner declaration separately
		// out.WriteString("\n")
	}
}

// getKotlinIndentLevel returns the number of declaration ancestors between the node and the program node
func getKotlinIndentLevel(node *sitter.Node) int {
	level := 0
	current := node.Parent()
	for current != nil {
		if strings.HasSuffix(current.Type(), "_declaration") {
			level++
		}
		current = current.Parent()
	}
	return level
}

// writeKotlinIndentLevel writes the appropriate indentation level for a node
func writeKotlinIndentLevel(node *sitter.Node, out *strings.Builder) {
	level := getKotlinIndentLevel(node)
	for i := 0; i < level; i++ {
		out.WriteString("\t")
	}
}

// writeKotlinSymbolCapture captures Kotlin symbol information from source code.
// This is a stub implementation.
func writeKotlinSymbolCapture(out *strings.Builder, sourceCode *[]byte, c sitter.QueryCapture, name string) {
	out.WriteString("// kotlin symbol capture not implemented yet")
}
