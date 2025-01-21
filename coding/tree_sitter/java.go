package tree_sitter

import (
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
)

func writeJavaSymbolCapture(out *strings.Builder, sourceCode *[]byte, c sitter.QueryCapture, name string) {
	content := c.Node.Content(*sourceCode)
	switch name {
	case "class.name", "method.name", "constructor.name", "field.name":
		{
			out.WriteString(content)
		}
	}
}

func writeJavaSignatureCapture(out *strings.Builder, sourceCode *[]byte, c sitter.QueryCapture, name string) {
	content := c.Node.Content(*sourceCode)
	switch name {
	case "class.declaration":
		{
			// TODO get write amount of indentation based on traversing
			// ancestors, until node of type "program" is reached
			out.WriteString("class ")
		}
	case "class.name", "class.constructor.name", "class.method.name":
		{
			out.WriteString(content)
		}
	case "class.body":
		{
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
