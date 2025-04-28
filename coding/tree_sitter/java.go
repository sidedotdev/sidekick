package tree_sitter

import (
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
)

func writeJavaSymbolCapture(out *strings.Builder, sourceCode *[]byte, c sitter.QueryCapture, name string) {
	content := c.Node.Content(*sourceCode)
	// note: only top-level names here (eg we must skip enum.method.name for instance)
	switch name {
	case "class.name", "interface.name", "method.name", "field.name", "annotation.name", "enum.name", "constant.name", "annotation_type_element.name":
		{
			out.WriteString(content)
		}
	}
}

func writeJavaSignatureCapture(out *strings.Builder, sourceCode *[]byte, c sitter.QueryCapture, name string) {
	content := c.Node.Content(*sourceCode)
	switch name {
	case "class.declaration", "annotation.declaration", "interface.declaration", "enum.declaration":
		{
			maybeModifiers := c.Node.Child(0)
			if maybeModifiers != nil && maybeModifiers.Type() == "modifiers" {
				// modifiers must write first, so they will also handle "class "
				return
			}

			writeJavaIndentLevel(c.Node, out)

			switch name {
			case "annotation.declaration":
				{
					out.WriteString("@interface ")
				}
			case "class.declaration":
				{
					out.WriteString("class ")
				}
			case "interface.declaration":
				{
					out.WriteString("interface ")
				}
			case "enum.declaration":
				{
					out.WriteString("enum ")
				}
			}
		}
	case "annotation.modifiers", "class.modifiers", "interface.modifiers", "enum.modifiers":
		{
			writeJavaIndentLevel(c.Node.Parent(), out)
			out.WriteString(content)
			out.WriteString(" ")
			switch name {
			case "annotation.modifiers":
				{
					out.WriteString("@interface ")
				}
			case "class.modifiers":
				{
					out.WriteString("class ")
				}
			case "interface.modifiers":
				{
					out.WriteString("interface ")
				}
			case "enum.modifiers":
				{
					out.WriteString("enum ")
				}
			}
		}
	case "annotation.name", "interface.name", "class.name", "constructor.name", "method.name", "enum.name", "field.name", "constant.name":
		{
			out.WriteString(content)
		}
	case "class.type_parameters", "interface.type_parameters", "method.type_parameters":
		{
			out.WriteString(content)
			if name == "method.type_parameters" {
				out.WriteString(" ")
			}
		}
	case "constructor.modifiers", "method.modifiers", "field.modifiers", "constant.modifiers",
		"method.type", "field.type", "constant.type":
		{
			out.WriteString(content)
			out.WriteString(" ")
		}
	case "constructor.declaration", "method.declaration", "constant.declaration", "field.declaration":
		{
			writeJavaIndentLevel(c.Node, out)
		}
	case "annotation_type_element.declaration":
		{
			writeJavaIndentLevel(c.Node, out)
			out.WriteString(content)
		}
	case "method.parameters", "constructor.parameters":
		{
			out.WriteString(content)
			out.WriteString("\n")
		}
	}
}

// getJavaIndentLevel returns the number of declaration ancestors between the node and the program node
func getJavaIndentLevel(node *sitter.Node) int {
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

func writeJavaIndentLevel(node *sitter.Node, out *strings.Builder) {
	level := getJavaIndentLevel(node)
	for i := 0; i < level; i++ {
		out.WriteString("\t")
	}
}
