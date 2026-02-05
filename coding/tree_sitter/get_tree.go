package tree_sitter

import (
	"fmt"
	"os"

	tree_sitter "github.com/tree-sitter/go-tree-sitter"
)

func GetTree(filePath string) (*tree_sitter.Tree, error) {
	tree, _, err := GetTreeWithSource(filePath)
	return tree, err
}

func GetTreeWithSource(filePath string) (*tree_sitter.Tree, []byte, error) {
	languageName, sitterLanguage, err := inferLanguageFromFilePath(filePath)
	if err != nil {
		return nil, nil, err
	}
	sourceCode, err := os.ReadFile(filePath)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read source code: %v", err)
	}
	parser := tree_sitter.NewParser()
	defer parser.Close()
	parser.SetLanguage(sitterLanguage)
	transformedSource := sourceTransform(languageName, &sourceCode)
	tree := parser.Parse(transformedSource, nil)
	return tree, transformedSource, nil
}
