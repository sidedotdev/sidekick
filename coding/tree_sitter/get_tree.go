package tree_sitter

import (
	tree_sitter "github.com/tree-sitter/go-tree-sitter"
)

// GetTreeWithSourceFromBytes parses source bytes and returns the tree-sitter tree.
func GetTreeWithSourceFromBytes(languageName string, sourceCode []byte) (*tree_sitter.Tree, []byte, error) {
	sitterLanguage, err := getSitterLanguage(languageName)
	if err != nil {
		return nil, nil, err
	}
	parser := tree_sitter.NewParser()
	defer parser.Close()
	parser.SetLanguage(sitterLanguage)
	transformedSource := sourceTransform(languageName, &sourceCode)
	tree := parser.Parse(transformedSource, nil)
	return tree, transformedSource, nil
}
