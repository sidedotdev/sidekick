package tree_sitter

import (
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
)

func writeJavaSymbolCapture(out *strings.Builder, sourceCode *[]byte, c sitter.QueryCapture, name string) {
	content := c.Node.Content(*sourceCode)
	switch name {
	case "class.name", "interface.name", "method.name", "annotation.name":
		{
			out.WriteString(content)
		}
	}
}

func writeJavaSignatureCapture(out *strings.Builder, sourceCode *[]byte, c sitter.QueryCapture, name string) {
	content := c.Node.Content(*sourceCode)
	switch name {
	case "class.declaration", "annotation.declaration", "interface.declaration":
		{
			maybeModifiers := c.Node.Child(0)
			if maybeModifiers != nil && maybeModifiers.Type() == "modifiers" {
				// modifiers must write first, so they will also handle "class "
				return
			}

			level := getDeclarationIndentLevel(c.Node)
			for i := 0; i < level; i++ {
				out.WriteString("\t")
			}

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
			}
		}
	case "annotation.modifiers", "class.modifiers", "interface.modifiers":
		{
			level := getDeclarationIndentLevel(c.Node.Parent())
			for i := 0; i < level; i++ {
				out.WriteString("\t")
			}
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
			}
		}
	case "annotation.name", "interface.name", "class.name", "class.constructor.name", "class.method.name":
		{
			out.WriteString(content)
		}
	case "class.type_parameters", "interface.type_parameters", "class.method.type_parameters":
		{
			out.WriteString(content)
			if name == "class.method.type_parameters" {
				out.WriteString(" ")
			}
		}
	case "interface.body", "class.body", "annotation.body":
		{
			out.WriteString("\n")
		}
	case "interface.method.declaration", "interface.constant.declaration", "interface.field.declaration", "annotation.element.declaration":
		{
			level := getDeclarationIndentLevel(c.Node)
			for i := 0; i < level; i++ {
				out.WriteString("\t")
			}
			out.WriteString(content)
			out.WriteString("\n")
		}
	case "class.constructor.modifiers", "class.method.modifiers", "class.method.type":
		{
			out.WriteString(content)
			out.WriteString(" ")
		}
	case "class.constructor.declaration", "class.method.declaration":
		{
			level := getDeclarationIndentLevel(c.Node)
			for i := 0; i < level; i++ {
				out.WriteString("\t")
			}
		}
	case "class.method.parameters", "class.constructor.parameters":
		{
			out.WriteString(content)
			out.WriteString("\n")
		}
	case "class.field.declaration":
		{
			level := getDeclarationIndentLevel(c.Node)
			for i := 0; i < level; i++ {
				out.WriteString("\t")
			}
			out.WriteString(content)
			out.WriteString("\n")
		}
	}
}

// getDeclarationIndentLevel returns the number of declaration ancestors between the node and the program node
func getDeclarationIndentLevel(node *sitter.Node) int {
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
