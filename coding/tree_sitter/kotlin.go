package tree_sitter

import (
	"strings"

	tree_sitter "github.com/tree-sitter/go-tree-sitter"
)

// writeKotlinSignatureCapture captures Kotlin signature information from source code.
func writeKotlinSignatureCapture(out *strings.Builder, sourceCode *[]byte, c tree_sitter.QueryCapture, name string) {
	content := c.Node.Utf8Text(*sourceCode)
	switch name {
	case "function.declaration":
		writeKotlinIndentLevel(&c.Node, out)
		maybeModifiers := c.Node.Child(0)
		if maybeModifiers != nil && maybeModifiers.Kind() == "modifiers" {
			out.WriteString(maybeModifiers.Utf8Text(*sourceCode))
			out.WriteString(" ")
		}
		out.WriteString("fun ")
	case "property.declaration":
		writeKotlinIndentLevel(&c.Node, out)
		out.WriteString(content)
	case "function.name":
		out.WriteString(content)
	case "function.type_parameters":
		out.WriteString(content)
		out.WriteString(" ")
	case "function.parameters", "function.type_constraints":
		out.WriteString(content)
	case "function.return_type":
		out.WriteString(": ")
		out.WriteString(content)
	case "class.declaration", "object.declaration":
		writeKotlinIndentLevel(&c.Node, out)
		maybeEnum := c.Node.Child(0)
		maybeModifiers := c.Node.Child(0)
		if maybeModifiers != nil && maybeModifiers.Kind() == "modifiers" {
			out.WriteString(maybeModifiers.Utf8Text(*sourceCode))
			out.WriteString(" ")
			maybeEnum = c.Node.Child(1)
		}
		if maybeEnum != nil && maybeEnum.Kind() == "enum" {
			out.WriteString("enum ")
		}
		switch name {
		case "class.declaration":
			{
				out.WriteString("class ")
			}
		case "object.declaration":
			{
				out.WriteString("object ")
			}
		}
	case "class.name":
		out.WriteString(content)
	case "class.primary_constructor":
		out.WriteString(content)
	case "class.type_parameters":
		out.WriteString(content)
	case "class.enum_entry.name":
		out.WriteString("\n")
		writeKotlinIndentLevel(&c.Node, out)
		out.WriteString(content)
	case "class.body":
		// Do nothing here as we want to handle each inner declaration separately
	}
}

// getKotlinIndentLevel returns the number of declaration ancestors between the node and the program node
func getKotlinIndentLevel(node *tree_sitter.Node) int {
	level := 0
	current := node.Parent()
	for current != nil {
		if strings.HasSuffix(current.Kind(), "_declaration") {
			level++
		}
		current = current.Parent()
	}
	return level
}

// writeKotlinIndentLevel writes the appropriate indentation level for a node
func writeKotlinIndentLevel(node *tree_sitter.Node, out *strings.Builder) {
	level := getKotlinIndentLevel(node)
	for i := 0; i < level; i++ {
		out.WriteString("\t")
	}
}

// writeKotlinSymbolCapture captures Kotlin symbol information from source code.
func writeKotlinSymbolCapture(out *strings.Builder, sourceCode *[]byte, c tree_sitter.QueryCapture, name string) {
	content := c.Node.Utf8Text(*sourceCode)
	switch name {
	case "class.name", "function.name", "enum_entry.name", "property.name":
		out.WriteString(content)
	}
}
