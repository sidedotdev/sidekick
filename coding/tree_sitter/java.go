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
			if !strings.HasPrefix(content, "class") {
				out.WriteString("class ")
			}
		}
	case "class.name", "constructor.name", "method.name":
		{
			out.WriteString(content)
		}
	case "method.parameters", "constructor.parameters":
		{
			out.WriteString(content)
		}
	case "field.declaration":
		{
			if !strings.HasSuffix(content, ";") {
				out.WriteString(content)
				out.WriteString(";")
			} else {
				out.WriteString(content)
			}
		}
	case "field.type":
		{
			out.WriteString(content)
			out.WriteString(" ")
		}
	case "field.name":
		{
			out.WriteString(content)
		}
	}
}
