package tree_sitter

import (
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
)

func writeTypescriptSignatureCapture(out *strings.Builder, sourceCode *[]byte, c sitter.QueryCapture, name string) {
	content := c.Node.Content(*sourceCode)
	switch name {
	case "function.declaration":
		{
			if strings.HasPrefix(content, "async ") {
				out.WriteString("async ")
			}
			// an alternative is to replace the full function declaration's body
			// with an empty string, but that seems like it would be slower
			//fmt.Println(c.Node.ChildByFieldName("body").Content(sourceCode))
		}
	case "function.name":
		{
			out.WriteString("function ")
			out.WriteString(content)
		}
	case "class.declaration":
		{
			out.WriteString("class ")
		}
	case "class.body", "class.method.body":
		{
			out.WriteString("\n")
		}
	case "class.heritage":
		{
			out.WriteString(" ")
			out.WriteString(content)
		}
	case "class.method.mod":
		{
			out.WriteString(content)
			out.WriteString(" ")
		}
	case "class.field":
		{
			out.WriteString("  ")
			out.WriteString(content)
			out.WriteString("\n")
		}
	case "class.method":
		{
			out.WriteString("  ")
		}
	case "lexical.declaration":
		{
			lines := strings.Split(content, "\n")
			for i, line := range lines {
				out.WriteString(line)
				out.WriteRune('\n')
				// only output first 3 lines at most
				if i >= 2 && i < len(lines)-1 {
					lastIndent := line[:len(line)-len(strings.TrimLeft(line, "\t "))]
					out.WriteString(lastIndent)
					out.WriteString("[...]")
					out.WriteRune('\n')
					break
				}
			}
		}
	case "function.parameters", "function.return_type", "interface.declaration", "type.declaration", "type_alias.declaration", "class.name", "class.method.name", "class.method.parameters", "class.method.return_type":
		{
			out.WriteString(content)
		}
		/*
			// FIXME didn't implement this in the corresponding query yet
			case "method.doc", "function.doc", "type.doc":
				{
					out.WriteString(content)
					out.WriteString("\n")
				}
		*/
	}
}

func writeTypescriptSymbolCapture(out *strings.Builder, sourceCode *[]byte, c sitter.QueryCapture, name string) {
	content := c.Node.Content(*sourceCode)
	switch name {
	case "method.class_name":
		{
			out.WriteString(content)
			out.WriteString(".")
		}
	case "class.name", "method.name", "function.name", "interface.name", "lexical.name", "var.name", "type_alias.name", "type.name", "enum.name", "enum_member.name":
		{
			out.WriteString(content)
		}
	}
}
