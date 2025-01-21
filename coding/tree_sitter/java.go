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

			// TODO get write amount of indentation based on traversing
			// ancestors, until node of type "program" is reached
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
			// TODO get write amount of indentation based on traversing
			// ancestors, until node of type "program" is reached
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
	case "annotation.element.declaration":
		{
			out.WriteString("\t")
			out.WriteString(content)
			out.WriteString("\n")
		}
	case "interface.body", "class.body", "annotation.body":
		{
			out.WriteString("\n")
		}
	case "interface.method.declaration", "interface.constant.declaration", "interface.field.declaration":
		{
			out.WriteString("\t") // TODO get write amount of indentation based on traversing ancestors
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
			out.WriteString("\t") // TODO get write amount of indentation based on traversing ancestors
		}
	case "class.method.parameters", "class.constructor.parameters":
		{
			out.WriteString(content)
			out.WriteString("\n")
		}
	case "class.field.declaration":
		{
			out.WriteString("\t") // TODO get write amount of indentation based on traversing ancestors
			out.WriteString(content)
			out.WriteString("\n")
		}
	}
}
