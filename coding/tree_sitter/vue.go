package tree_sitter

import (
	"strings"

	tree_sitter "github.com/tree-sitter/go-tree-sitter"
	tree_sitter_typescript "github.com/tree-sitter/tree-sitter-typescript/bindings/go"
)

func writeVueSignatureCapture(out *strings.Builder, sourceCode *[]byte, c tree_sitter.QueryCapture, name string) {
	switch name {
	case "template":
		out.WriteString("<template>")
	case "script":
		out.WriteString("<script>")
	case "style":
		out.WriteString("<style>")
	}
}

func getVueEmbeddedLanguageSymbols(vueTree *tree_sitter.Tree, sourceCode *[]byte) ([]Symbol, error) {
	// Call GetVueEmbeddedTypescriptTree to get the embedded typescript
	tsTree, err := GetVueEmbeddedTypescriptTree(vueTree, sourceCode)
	if err != nil {
		return nil, err
	}
	if tsTree == nil {
		return []Symbol{}, nil
	}
	defer tsTree.Close()
	// Get symbols from tsTree and return them
	tsLang := tree_sitter.NewLanguage(tree_sitter_typescript.LanguageTypescript())
	symbols, err := getSourceSymbolsInternal("typescript", tsLang, tsTree, sourceCode)
	if err != nil {
		return nil, err
	}
	return symbols, nil
}

func writeVueSymbolCapture(out *strings.Builder, sourceCode *[]byte, c tree_sitter.QueryCapture, name string) {
	switch name {
	case "template":
		out.WriteString("<template>")
	case "script":
		out.WriteString("<script>")
	case "style":
		out.WriteString("<style>")
	}
}

func getVueEmbeddedLanguageSignatures(vueTree *tree_sitter.Tree, sourceCode *[]byte) ([]Signature, error) {
	// Call GetVueEmbeddedTypescriptTree to get the embedded typescript
	tsTree, err := GetVueEmbeddedTypescriptTree(vueTree, sourceCode)
	if err != nil {
		return []Signature{}, err
	}
	if tsTree == nil {
		return []Signature{}, nil
	}
	defer tsTree.Close()

	tsLang := tree_sitter.NewLanguage(tree_sitter_typescript.LanguageTypescript())
	signatureSlice, err := getFileSignaturesInternal("typescript", tsLang, tsTree, sourceCode, true)
	if err != nil {
		return nil, err
	}

	return signatureSlice, nil
}

func GetVueEmbeddedTypescriptTree(vueTree *tree_sitter.Tree, sourceCode *[]byte) (*tree_sitter.Tree, error) {
	tsRanges := []tree_sitter.Range{}

	rootNode := vueTree.RootNode()
	childCount := rootNode.ChildCount()

	for i := uint(0); i < childCount; i++ {
		node := rootNode.Child(i)
		switch node.Kind() {
		case "template_element", "style_element":
			// skip
		case "script_element":
			codeNode := node.NamedChild(1)
			tsRanges = append(tsRanges, tree_sitter.Range{
				StartPoint: codeNode.StartPosition(),
				EndPoint:   codeNode.EndPosition(),
				StartByte:  uint(codeNode.StartByte()),
				EndByte:    uint(codeNode.EndByte()),
			})
		}
	}

	if len(tsRanges) == 0 {
		return nil, nil
	}

	parser := tree_sitter.NewParser()
	defer parser.Close()
	// TODO make sure the lang attribute is ts before committing to typescript
	tsLang := tree_sitter.NewLanguage(tree_sitter_typescript.LanguageTypescript())
	parser.SetLanguage(tsLang)
	parser.SetIncludedRanges(tsRanges)
	return parser.Parse(*sourceCode, nil), nil
}
