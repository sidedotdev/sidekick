package tree_sitter

import (
	"context"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/typescript/typescript"
)

func writeVueSignatureCapture(out *strings.Builder, sourceCode *[]byte, c sitter.QueryCapture, name string) {
	switch name {
	case "template":
		out.WriteString("<template>")
	case "script":
		out.WriteString("<script>")
	case "style":
		out.WriteString("<style>")
	}
}

func getVueEmbeddedLanguageSymbols(vueTree *sitter.Tree, sourceCode *[]byte) ([]Symbol, error) {
	// Call GetVueEmbeddedTypescriptTree to get the embedded typescript
	tsTree, err := GetVueEmbeddedTypescriptTree(vueTree, sourceCode)
	if err != nil {
		return nil, err
	}
	if tsTree == nil {
		return []Symbol{}, nil
	}
	// Get symbols from tsTree and return them
	symbols, err := getSourceSymbolsInternal("typescript", typescript.GetLanguage(), tsTree, sourceCode)
	if err != nil {
		return nil, err
	}
	return symbols, nil
}

func writeVueSymbolCapture(out *strings.Builder, sourceCode *[]byte, c sitter.QueryCapture, name string) {
	switch name {
	case "template":
		out.WriteString("<template>")
	case "script":
		out.WriteString("<script>")
	case "style":
		out.WriteString("<style>")
	}
}

func getVueEmbeddedLanguageSignatures(vueTree *sitter.Tree, sourceCode *[]byte) ([]Signature, error) {
	// Call GetVueEmbeddedTypescriptTree to get the embedded typescript
	tsTree, err := GetVueEmbeddedTypescriptTree(vueTree, sourceCode)
	if err != nil {
		return []Signature{}, err
	}
	if tsTree == nil {
		return []Signature{}, nil
	}

	signatureSlice, err := getFileSignaturesInternal("typescript", typescript.GetLanguage(), tsTree, sourceCode, true)
	if err != nil {
		return nil, err
	}

	return signatureSlice, nil
}

func GetVueEmbeddedTypescriptTree(vueTree *sitter.Tree, sourceCode *[]byte) (*sitter.Tree, error) {
	//vueRanges := []sitter.Range{}
	tsRanges := []sitter.Range{}

	rootNode := vueTree.RootNode()
	childCount := rootNode.ChildCount()

	for i := 0; i < int(childCount); i++ {
		node := rootNode.Child(i)
		switch node.Type() {
		case "template_element", "style_element":
			// vueRanges = append(vueRanges, sitter.Range{
			// 	StartPoint: node.StartPoint(),
			// 	EndPoint:   node.EndPoint(),
			// 	StartByte:  node.StartByte(),
			// 	EndByte:    node.EndByte(),
			// })
		case "script_element":
			codeNode := node.NamedChild(1)
			tsRanges = append(tsRanges, sitter.Range{
				StartPoint: codeNode.StartPoint(),
				EndPoint:   codeNode.EndPoint(),
				StartByte:  codeNode.StartByte(),
				EndByte:    codeNode.EndByte(),
			})
		}
	}

	if len(tsRanges) == 0 {
		return nil, nil
	}

	parser := sitter.NewParser()
	// TODO make sure the lang attribute is ts before committing to typescript
	parser.SetLanguage(typescript.GetLanguage())
	parser.SetIncludedRanges(tsRanges)
	return parser.ParseCtx(context.Background(), nil, *sourceCode)
}
