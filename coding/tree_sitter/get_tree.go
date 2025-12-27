package tree_sitter

import (
	"context"
	"fmt"
	"os"

	sitter "github.com/smacker/go-tree-sitter"
)

func GetTree(filePath string) (*sitter.Tree, error) {
	tree, _, err := GetTreeWithSource(filePath)
	return tree, err
}

func GetTreeWithSource(filePath string) (*sitter.Tree, []byte, error) {
	languageName, sitterLanguage, err := inferLanguageFromFilePath(filePath)
	if err != nil {
		return nil, nil, err
	}
	sourceCode, err := os.ReadFile(filePath)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read source code: %v", err)
	}
	parser := sitter.NewParser()
	parser.SetLanguage(sitterLanguage)
	transformedSource := sourceTransform(languageName, &sourceCode)
	tree, err := parser.ParseCtx(context.Background(), nil, transformedSource)
	return tree, transformedSource, err
}
