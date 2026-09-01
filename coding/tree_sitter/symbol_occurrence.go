package tree_sitter

import (
	"fmt"
	"strings"
	"unicode/utf8"

	tree_sitter "github.com/tree-sitter/go-tree-sitter"
)

// SymbolOccurrence locates a syntax token naming a symbol, as opposed to a
// mere textual match such as a substring of a longer identifier or text within
// a comment or string literal.
type SymbolOccurrence struct {
	StartByte  uint
	EndByte    uint
	StartPoint tree_sitter.Point
	EndPoint   tree_sitter.Point
}

// FindSymbolOccurrences returns the smallest syntax nodes naming symbolName in
// sourceCode. Dotted symbol names (eg "pkg.Foo" or "Type.method") match their
// final segment, but only where the source spells out the whole dotted name.
func FindSymbolOccurrences(languageName string, sourceCode []byte, symbolName string) ([]SymbolOccurrence, error) {
	name, qualifiedName := splitOccurrenceName(symbolName)
	if name == "" {
		return nil, nil
	}

	sitterLanguage, err := getSitterLanguage(languageName)
	if err != nil {
		return nil, err
	}
	parser := tree_sitter.NewParser()
	defer parser.Close()
	parser.SetLanguage(sitterLanguage)
	tree := parser.Parse(sourceTransform(languageName, &sourceCode), nil)
	if tree == nil {
		return nil, fmt.Errorf("failed to parse source code as %s", languageName)
	}
	defer tree.Close()

	var occurrences []SymbolOccurrence
	var visit func(node *tree_sitter.Node)
	visit = func(node *tree_sitter.Node) {
		childCount := node.ChildCount()
		if childCount == 0 {
			if isSymbolNameToken(node, sourceCode, name, qualifiedName) {
				occurrences = append(occurrences, SymbolOccurrence{
					StartByte:  node.StartByte(),
					EndByte:    node.EndByte(),
					StartPoint: node.StartPosition(),
					EndPoint:   node.EndPosition(),
				})
			}
			return
		}
		for i := uint(0); i < childCount; i++ {
			if child := node.Child(i); child != nil {
				visit(child)
			}
		}
	}
	visit(tree.RootNode())

	return occurrences, nil
}

// splitOccurrenceName returns the final identifier segment of a symbol name
// along with the whole dotted name, dropping pointer and address-of
// decorations that callers commonly include.
func splitOccurrenceName(symbolName string) (name, qualifiedName string) {
	var segments []string
	for _, segment := range strings.Split(symbolName, ".") {
		segment = strings.TrimLeft(strings.TrimSpace(segment), "*&")
		if segment == "" {
			return "", ""
		}
		segments = append(segments, segment)
	}
	if len(segments) == 0 {
		return "", ""
	}
	return segments[len(segments)-1], strings.Join(segments, ".")
}

func isSymbolNameToken(node *tree_sitter.Node, sourceCode []byte, name, qualifiedName string) bool {
	if !node.IsNamed() {
		return false
	}
	kind := node.Kind()
	if strings.Contains(kind, "comment") || strings.Contains(kind, "string") {
		return false
	}
	if node.Utf8Text(sourceCode) != name {
		return false
	}
	if qualifiedName == name {
		return true
	}

	qualifiedStart := int(node.EndByte()) - len(qualifiedName)
	if qualifiedStart < 0 || string(sourceCode[qualifiedStart:node.EndByte()]) != qualifiedName {
		return false
	}
	// a preceding word character means a longer qualifier, eg "mypkg.Foo" when
	// "pkg.Foo" was requested
	return qualifiedStart == 0 || !isWordByte(sourceCode[qualifiedStart-1])
}

func isWordByte(b byte) bool {
	return b == '_' || b >= utf8.RuneSelf ||
		(b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}
