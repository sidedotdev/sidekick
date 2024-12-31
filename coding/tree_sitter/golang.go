package tree_sitter

import (
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
)

func writeGolangSymbolCapture(out *strings.Builder, sourceCode *[]byte, c sitter.QueryCapture, name string) {
	content := c.Node.Content(*sourceCode)
	switch name {
	case "function.name", "method.name", "const.name", "type.name", "var.name":
		{
			out.WriteString(content)
		}
	case "method.receiver_type":
		{
			out.WriteString(content)
			out.WriteString(".")
		}
		// NOTE: we don't yet allow the full receiver as part of a symbol in
		// retrieve_code_context, so we don't include it here either to avoid
		// confusing the LLM. we do support the dot syntax above though.
		/*
			case "method.receiver":
				{
					out.WriteString(content)
					out.WriteString(" ")
				}
		*/
	}
}

func writeGolangSignatureCapture(out *strings.Builder, sourceCode *[]byte, c sitter.QueryCapture, name string) {
	content := c.Node.Content(*sourceCode)
	switch name {
	case "method.doc", "function.doc", "type.doc":
		{
			out.WriteString(content)
			out.WriteString("\n")
		}
	case "method.result", "function.result", "var.type":
		{
			out.WriteString(" ")
			out.WriteString(content)
		}
	case "method.name", "method.parameters", "function.parameters", "type.declaration", "const.declaration", "var.name":
		{
			out.WriteString(content)
		}
	case "var.declaration":
		{
			out.WriteString("var ")
		}
	case "method.receiver":
		{
			out.WriteString("func ")
			out.WriteString(content)
			out.WriteString(" ")
		}
	case "function.name":
		{
			out.WriteString("func ")
			out.WriteString(content)
		}
	}
}