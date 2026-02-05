package tree_sitter

import (
	"strings"

	tree_sitter "github.com/tree-sitter/go-tree-sitter"
)

// extracts full "signatures", which provided detailed information about the code
// based on the corresponding signatures_queries/signature_python.scm
func writePythonSignatureCapture(out *strings.Builder, sourceCode *[]byte, c tree_sitter.QueryCapture, name string) {
	content := c.Node.Utf8Text(*sourceCode)
	switch name {
	case "type.declaration", "assignment.name", "function.parameters", "class.method.parameters", "class.superclasses":
		{
			out.WriteString(content)
		}
	case "assignment.type":
		{
			out.WriteString(": ")
			out.WriteString(content)
		}
	case "assignment.right":
		{
			if strings.Contains(content, "NewType(") {
				out.WriteString(" = ")
				out.WriteString(content)
			}
		}
	case "function.return_type", "class.method.return_type":
		{
			out.WriteString(" -> ")
			out.WriteString(content)
		}
	case "function.name":
		{
			out.WriteString("def ")
			out.WriteString(content)
		}
	case "function.docstring", "class.docstring", "class.method.decorator":
		{
			// TODO detect whitespace from content and only use "\t" if it's
			// empty, otherwise use detected whitespace after a newline character
			out.WriteString("\t")
			out.WriteString(content)
			out.WriteString("\n")
		}
	case "class.method.name":
		{
			out.WriteString("\tdef ")
			out.WriteString(content)
		}
	case "class.method.docstring":
		{
			// TODO detect whitespace from content and only use "\t\t" if it's
			// empty, otherwise use detected whitespace after a newline character
			out.WriteString("\t\t")
			out.WriteString(content)
			out.WriteString("\n")
		}
	case "class.name":
		{
			out.WriteString("class ")
			out.WriteString(content)
		}
	case "class.body", "class.method.body", "function.body":
		{
			// need class and method on separate lines and:in case of multiple
			// methods, we need to separate them. and functions need a newline
			// after for the docstring
			out.WriteString("\n")
		}
	case "function.comments", "class.method.comments", "class.comments", "function.decorator", "class.decorator":
		{
			out.WriteString(content)
			out.WriteString("\n")
		}
	default:
		// Handle other Python-specific elements here in the future
	}
}

// extracts symbol names only
// based on the corresponding signatures_queries/signature_python.scm
func writePythonSymbolCapture(out *strings.Builder, sourceCode *[]byte, c tree_sitter.QueryCapture, name string) {
	content := c.Node.Utf8Text(*sourceCode)
	switch name {
	case "class.method.name":
		// skip as it's expected to be included in the method.name match instead
	default:
		{
			if strings.HasSuffix(name, ".name") {
				out.WriteString(content)
			}
		}
	}
}
