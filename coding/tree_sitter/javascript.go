package tree_sitter

import (
	"strings"

	tree_sitter "github.com/tree-sitter/go-tree-sitter"
)

func writeJavascriptSignatureCapture(out *strings.Builder, sourceCode *[]byte, c tree_sitter.QueryCapture, name string) {
	content := c.Node.Utf8Text(*sourceCode)
	switch name {
	case "function.declaration":
		{
			if strings.HasPrefix(content, "async ") || strings.Contains(content, " async ") {
				out.WriteString("async ")
			}
		}
	case "function.name":
		{
			out.WriteString("function ")
			out.WriteString(content)
		}
	case "class.declaration":
		{
			writeJavascriptIndentLevel(&c.Node, out)
			// Check if parent is an export_statement
			if parent := c.Node.Parent(); parent != nil && parent.Kind() == "export_statement" {
				content := parent.Utf8Text(*sourceCode)
				if strings.HasPrefix(content, "export default ") {
					out.WriteString("export default ")
				} else {
					out.WriteString("export ")
				}
			}
			out.WriteString("class ")
		}
	case "class.body", "class.method.body":
		{
			// no output for body
		}
	case "class.heritage":
		{
			out.WriteString(" ")
			out.WriteString(content)
		}
	case "class.method.declaration", "class.field.declaration":
		{
			out.WriteString("\n")
			writeJavascriptIndentLevel(&c.Node, out)
		}
	case "class.field.name":
		{
			out.WriteString(content)
		}
	case "lexical.declaration", "var.declaration":
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
	case "export.declaration":
		{
			if strings.Contains(content, "export default ") {
				out.WriteString("export default ")
			} else {
				out.WriteString("export ")
			}
		}
	case "function.parameters",
		"class.name",
		"class.method.name", "class.method.parameters":
		{
			out.WriteString(content)
		}
	}
}

func writeJavascriptSymbolCapture(out *strings.Builder, sourceCode *[]byte, c tree_sitter.QueryCapture, name string) {
	content := c.Node.Utf8Text(*sourceCode)
	switch name {
	case "method.class_name":
		{
			out.WriteString(content)
			out.WriteString(".")
		}
	case "class.name", "method.name", "function.name", "lexical.name", "var.name", "export.name":
		{
			out.WriteString(content)
		}
	}
}

// getJavascriptIndentLevel returns the number of declaration ancestors between the node and the program node
func getJavascriptIndentLevel(node *tree_sitter.Node) int {
	level := 0
	current := node.Parent()
	for current != nil {
		if strings.HasSuffix(current.Kind(), "_declaration") || current.Kind() == "class" {
			level++
		}
		current = current.Parent()
	}
	return level
}

func writeJavascriptIndentLevel(node *tree_sitter.Node, out *strings.Builder) {
	level := getJavascriptIndentLevel(node)
	for i := 0; i < level; i++ {
		out.WriteString("\t")
	}
}
