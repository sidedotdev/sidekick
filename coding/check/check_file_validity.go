package check

import (
	"errors"
	"fmt"
	"path/filepath"
	"sidekick/coding/tree_sitter"
	"sidekick/env"
	"sidekick/utils"
	"strings"
	"unsafe"

	tree_sitter_lib "github.com/tree-sitter/go-tree-sitter"
	tree_sitter_python "github.com/tree-sitter/tree-sitter-python/bindings/go"
)

// checkEmbeddedFileValidity checks the syntax of embedded languages within a file.
// Currently supports TypeScript embedded in Vue files.
func checkEmbeddedFileValidity(tree *tree_sitter_lib.Tree, sourceCode []byte, languageName string) (bool, string) {
	switch languageName {
	case "vue":
		tsTree, err := tree_sitter.GetVueEmbeddedTypescriptTree(tree, &sourceCode)
		if err != nil {
			return false, fmt.Sprintf("Error extracting TypeScript from Vue file: %v", err)
		}
		if tsTree == nil {
			return true, "No TypeScript code found in Vue file."
		}
		valid, errorString := checkTypescriptTree(sourceCode, tsTree.RootNode())
		return valid, errorString
	default:
		return true, "No embedded language checking needed."
	}
}

const SyntaxError = "Syntax error(s)"

// CheckFileValidity checks a source file for bad syntax or other particularly bad issues.
// Returns true if the file is valid, false otherwise, along with a string
// containing any errors found, or warnings for errors that should not revert
// edits.
func CheckFileValidity(envContainer env.EnvContainer, relativeFilePath string) (bool, string, error) {
	filePath := filepath.Join(envContainer.Env.GetWorkingDirectory(), relativeFilePath)
	tree, sourceCode, err := tree_sitter.GetTreeWithSource(filePath)
	if err != nil {
		if errors.Is(err, tree_sitter.ErrFailedInferLanguage) {
			return true, fmt.Sprintf("Warning: Failed to infer language from file extension: %v", err), nil
		} else {
			return false, fmt.Sprintf("Failed to get tree: %v", err), err
		}
	}
	hasError := tree.RootNode().HasError()
	if hasError {
		errorDetails := ExtractErrorMessages(sourceCode, tree.RootNode())
		return false, errorDetails, nil
	}

	if strings.TrimSpace(string(sourceCode)) == "" {
		return false, "File is blank", nil
	}

	languageName := utils.InferLanguageNameFromFilePath(filePath)
	valid, errorString := checkEmbeddedFileValidity(tree, sourceCode, languageName)
	if !valid {
		return false, errorString, nil
	}

	// check for language-specific errors
	switch languageName {
	case "golang":
		var err error
		valid, errorString := checkGoTree(sourceCode, tree.RootNode())

		if valid {
			valid, errorString, err = CheckViaGoBuild(envContainer, relativeFilePath)
		}

		return valid, errorString, err
	case "python":
		valid, errorString := checkPythonTree(&sourceCode, tree.RootNode())
		return valid, errorString, nil
	default:
		return true, "", nil
	}
}

func checkGoTree(sourceCode []byte, node *tree_sitter_lib.Node) (bool, string) {
	// check for multiple import declarations, which is not allowed in go and
	// LLMs tend to keep adding and have a hard time fixing
	var importDeclarations []*tree_sitter_lib.Node
	for i := uint(0); i < node.ChildCount(); i++ {
		child := node.Child(i)
		if child.Kind() == "import_declaration" {
			importDeclarations = append(importDeclarations, child)
		}
	}

	// TODO include the line number of the error and the content around those lines
	if len(importDeclarations) > 1 {
		// Allow exactly 2 import declarations if one is `import "C"` (cgo requirement)
		if len(importDeclarations) == 2 && hasImportC(sourceCode, importDeclarations) {
			return true, ""
		}
		return false, "Multiple import statements found"
	}

	return true, ""
}

// hasImportC checks if any of the import declarations is `import "C"` (cgo)
func hasImportC(sourceCode []byte, importDeclarations []*tree_sitter_lib.Node) bool {
	for _, decl := range importDeclarations {
		if isCgoImport(sourceCode, decl) {
			return true
		}
	}
	return false
}

// isCgoImport checks if an import declaration is specifically `import "C"`
func isCgoImport(sourceCode []byte, importDecl *tree_sitter_lib.Node) bool {
	for i := uint(0); i < importDecl.ChildCount(); i++ {
		child := importDecl.Child(i)
		if child.Kind() == "import_spec" {
			// import_spec directly contains interpreted_string_literal
			for j := uint(0); j < child.ChildCount(); j++ {
				specChild := child.Child(j)
				if specChild.Kind() == "interpreted_string_literal" {
					content := specChild.Utf8Text(sourceCode)
					if content == `"C"` {
						return true
					}
				}
			}
		}
	}
	return false
}

func checkPythonTree(sourceCode *[]byte, node *tree_sitter_lib.Node) (bool, string) {
	passedCheck, errorString := checkPythonEmptyBodies(sourceCode, node)
	// TODO add check for duplicate classes and functions
	return passedCheck, errorString
}

func checkPythonEmptyBodies(sourceCode *[]byte, node *tree_sitter_lib.Node) (bool, string) {
	// each match is a missing body in python, which is a syntax error that the
	// python parser doesn't tag as an ERROR, hence this explicit check
	emptyBodiesQuery := `
		(function_definition
			body: (block) @block
			(#eq? @block "")
		) @empty_function

		(class_definition
			body: (block) @block
			(#eq? @block "")
		) @empty_class
	`
	sitterLanguage := tree_sitter_lib.NewLanguage(unsafe.Pointer(tree_sitter_python.Language()))
	q, err := tree_sitter_lib.NewQuery(sitterLanguage, emptyBodiesQuery)
	if err != nil {
		return false, fmt.Sprintf("Failed to create query: %v", err)
	}
	defer q.Close()

	errorsWriter := strings.Builder{}

	qc := tree_sitter_lib.NewQueryCursor()
	defer qc.Close()
	matches := qc.Matches(q, node, *sourceCode)
	// Iterate over query results
	for match := matches.Next(); match != nil; match = matches.Next() {
		for _, c := range match.Captures {
			name := q.CaptureNames()[c.Index]
			content := c.Node.Utf8Text(*sourceCode)
			if name == "empty_function" {
				// TODO include context around the error. We could return a
				// slice of structs containing the error message and the
				// SourceBlock instead of a single boolean and string. an empty
				// slice would indicate that the check passed due to no errors
				// being detected. We may also include the severity of the
				// issue, eg "error" vs "warning", where warnings might not
				// revert edits but errors would.
				errorsWriter.WriteString("Empty function body found:\n\n")
				errorsWriter.WriteString(fmt.Sprintf("Line: %d\n", c.Node.StartPosition().Row))
				errorsWriter.WriteString(content)
			} else if name == "empty_class" {
				errorsWriter.WriteString("Empty class body found:\n\n")
				errorsWriter.WriteString(fmt.Sprintf("Line: %d\n", c.Node.StartPosition().Row))
				errorsWriter.WriteString(content)
			}
		}
	}

	if errorsWriter.Len() > 0 {
		return false, errorsWriter.String()
	}
	return true, ""
}

// checkTypescriptTree checks the syntax of a TypeScript AST and returns a boolean indicating if it is valid and a string containing any errors.
func checkTypescriptTree(sourceCode []byte, node *tree_sitter_lib.Node) (bool, string) {
	if node.HasError() {
		errorDetails := ExtractErrorMessages(sourceCode, node)
		return false, errorDetails
	}
	return true, ""
}

// ExtractErrorNodes traverses a tree-sitter tree and returns a slice of nodes that are errors.
func ExtractErrorNodes(node *tree_sitter_lib.Node) []*tree_sitter_lib.Node {
	var errors []*tree_sitter_lib.Node
	var collectErrors func(*tree_sitter_lib.Node)
	collectErrors = func(n *tree_sitter_lib.Node) {
		if n.IsError() {
			errors = append(errors, n)
		}
		for i := uint(0); i < n.ChildCount(); i++ {
			collectErrors(n.Child(i))
		}
	}
	collectErrors(node)
	return errors
}

func ExtractErrorMessages(sourceCode []byte, node *tree_sitter_lib.Node) string {
	errorNodes := ExtractErrorNodes(node)
	errorDetails := fmt.Sprintf("%s: %v", SyntaxError, utils.Map(errorNodes, func(errorNode *tree_sitter_lib.Node) string {
		sourceBlock := tree_sitter.SourceBlock{
			Source: &sourceCode,
			Range: tree_sitter_lib.Range{
				StartByte:  errorNode.StartByte(),
				EndByte:    errorNode.EndByte(),
				StartPoint: errorNode.StartPosition(),
				EndPoint:   errorNode.EndPosition(),
			},
		}

		expanded := tree_sitter.ExpandContextLines([]tree_sitter.SourceBlock{sourceBlock}, 5, sourceCode)
		return fmt.Sprintf("Syntax Error in following content:\n%s", expanded[0].String())
	}))
	return errorDetails
}
