package check

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sidekick/coding/tree_sitter"
	"sidekick/env"
	"sidekick/utils"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/python"
)

// checkEmbeddedFileValidity checks the syntax of embedded languages within a file.
// Currently supports TypeScript embedded in Vue files.
func checkEmbeddedFileValidity(tree *sitter.Tree, sourceCode []byte, languageName string) (bool, string) {
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
	tree, err := tree_sitter.GetTree(filePath)
	if err != nil {
		if errors.Is(err, tree_sitter.ErrFailedInferLanguage) {
			return true, fmt.Sprintf("Warning: Failed to infer language from file extension: %v", err), nil
		} else {
			return false, fmt.Sprintf("Failed to get tree: %v", err), err
		}
	}
	sourceCode, err := os.ReadFile(filePath)
	hasError := tree.RootNode().HasError()
	if hasError {
		if err != nil {
			return false, "", fmt.Errorf("failed to obtain source code: %v", err)
		}
		errorDetails := ExtractErrorMessages(sourceCode, tree.RootNode())
		return false, errorDetails, nil
	}

	if err != nil {
		return false, fmt.Sprintf("Failed to read file: %v", err), err
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
		valid, errorString := checkGoTree(tree.RootNode())

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

func checkGoTree(node *sitter.Node) (bool, string) {
	// check for multiple import declarations, which is not allowed in go and
	// LLMs tend to keep adding and have a hard time fixing
	var importDeclarations []*sitter.Node
	for i := 0; i < int(node.ChildCount()); i++ {
		child := node.Child(i)
		if child.Type() == "import_declaration" {
			importDeclarations = append(importDeclarations, child)
		}
	}

	// TODO include the line number of the error and the content around those lines
	if len(importDeclarations) > 1 {
		return false, "Multiple import statements found"
	}

	return true, ""
}

func checkPythonTree(sourceCode *[]byte, node *sitter.Node) (bool, string) {
	passedCheck, errorString := checkPythonEmptyBodies(sourceCode, node)
	// TODO add check for duplicate classes and functions
	return passedCheck, errorString
}

func checkPythonEmptyBodies(sourceCode *[]byte, node *sitter.Node) (bool, string) {
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
	sitterLanguage := python.GetLanguage()
	q, err := sitter.NewQuery([]byte(emptyBodiesQuery), sitterLanguage)
	if err != nil {
		return false, fmt.Sprintf("Failed to create query: %v", err)
	}

	errorsWriter := strings.Builder{}

	qc := sitter.NewQueryCursor()
	qc.Exec(q, node)
	// Iterate over query results
	for {
		m, ok := qc.NextMatch()
		if !ok {
			break
		}

		// Apply predicates filtering
		m = qc.FilterPredicates(m, *sourceCode)
		for _, c := range m.Captures {
			name := q.CaptureNameForId(c.Index)
			content := c.Node.Content(*sourceCode)
			if name == "empty_function" {
				// TODO include context around the error. We could return a
				// slice of structs containing the error message and the
				// SourceBlock instead of a single boolean and string. an empty
				// slice would indicate that the check passed due to no errors
				// being detected. We may also include the severity of the
				// issue, eg "error" vs "warning", where warnings might not
				// revert edits but errors would.
				errorsWriter.WriteString("Empty function body found:\n\n")
				errorsWriter.WriteString(fmt.Sprintf("Line: %d\n", c.Node.StartPoint().Row))
				errorsWriter.WriteString(content)
			} else if name == "empty_class" {
				errorsWriter.WriteString("Empty class body found:\n\n")
				errorsWriter.WriteString(fmt.Sprintf("Line: %d\n", c.Node.StartPoint().Row))
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
func checkTypescriptTree(sourceCode []byte, node *sitter.Node) (bool, string) {
	if node.HasError() {
		errorDetails := ExtractErrorMessages(sourceCode, node)
		return false, errorDetails
	}
	return true, ""
}

// ExtractErrorNodes traverses a tree-sitter tree and returns a slice of nodes that are errors.
func ExtractErrorNodes(node *sitter.Node) []*sitter.Node {
	var errors []*sitter.Node
	var collectErrors func(*sitter.Node)
	collectErrors = func(n *sitter.Node) {
		if n.IsError() {
			errors = append(errors, n)
		}
		for i := 0; i < int(n.ChildCount()); i++ {
			collectErrors(n.Child(i))
		}
	}
	collectErrors(node)
	return errors
}

func ExtractErrorMessages(sourceCode []byte, node *sitter.Node) string {
	errorNodes := ExtractErrorNodes(node)
	errorDetails := fmt.Sprintf("%s: %v", SyntaxError, utils.Map(errorNodes, func(errorNode *sitter.Node) string {
		sourceBlock := tree_sitter.SourceBlock{
			Source: &sourceCode,
			Range: sitter.Range{
				StartByte:  errorNode.StartByte(),
				EndByte:    errorNode.EndByte(),
				StartPoint: errorNode.StartPoint(),
				EndPoint:   errorNode.EndPoint(),
			},
		}

		expanded := tree_sitter.ExpandContextLines([]tree_sitter.SourceBlock{sourceBlock}, 5, sourceCode)
		return fmt.Sprintf("Syntax Error in following content:\n%s", expanded[0].String())
	}))
	return errorDetails
}
