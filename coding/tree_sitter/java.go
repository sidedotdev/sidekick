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
			out.WriteString("class ")
		}
	case "class.name":
		{
			out.WriteString(content)
		}
	case "constructor.name", "method.name":
		{
			// Check if the method/constructor is private
			modifiers := c.Node.Parent().ChildByFieldName("modifiers")
			if modifiers != nil && strings.Contains(modifiers.Content(*sourceCode), "private") {
				return
			}
			out.WriteString("\t")
			out.WriteString(content)
		}
	case "method.parameters", "constructor.parameters":
		{
			// Only write parameters if the method/constructor was public (written)
			if !strings.HasSuffix(out.String(), "\t") {
				out.WriteString(content)
			}
		}
	case "field.declaration":
		{
			modifiers := c.Node.ChildByFieldName("modifiers")
			if modifiers == nil || !strings.Contains(modifiers.Content(*sourceCode), "public") {
				return
			}
			out.WriteString("\t")
			// Get type and name
			typeNode := c.Node.ChildByFieldName("type")
			nameNode := c.Node.ChildByFieldName("name")
			if typeNode != nil && nameNode != nil {
				out.WriteString(typeNode.Content(*sourceCode))
				out.WriteString(" ")
				out.WriteString(nameNode.Content(*sourceCode))
			}
		}
	}
}
