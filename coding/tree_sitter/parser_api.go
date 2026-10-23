package tree_sitter

import (
	"sidekick/coding/tree_sitter/parser"

	tree_sitter "github.com/tree-sitter/go-tree-sitter"
)

// The parser subpackage owns environment-independent parsing so it can be
// shared with code that must not depend on this package's env and storage
// integrations. These aliases keep the existing API for current callers.

type (
	SourceBlock      = parser.SourceBlock
	SourceCode       = parser.SourceCode
	Symbol           = parser.Symbol
	SourceCapture    = parser.SourceCapture
	Declaration      = parser.Declaration
	Signature        = parser.Signature
	FileSymbols      = parser.FileSymbols
	SymbolDefinition = parser.SymbolDefinition
	SymbolOccurrence = parser.SymbolOccurrence
)

var (
	ErrNoHeadersFound      = parser.ErrNoHeadersFound
	ErrFailedInferLanguage = parser.ErrFailedInferLanguage
	ErrNoSymbolParsed      = parser.ErrNoSymbolParsed
)

func GetTreeWithSourceFromBytes(languageName string, sourceCode []byte) (*tree_sitter.Tree, []byte, error) {
	return parser.GetTreeWithSourceFromBytes(languageName, sourceCode)
}

func GetVueEmbeddedTypescriptTree(vueTree *tree_sitter.Tree, sourceCode *[]byte) (*tree_sitter.Tree, error) {
	return parser.GetVueEmbeddedTypescriptTree(vueTree, sourceCode)
}

func GetFileHeadersFromBytes(languageName string, sourceCode []byte, numContextLines int) ([]SourceBlock, error) {
	return parser.GetFileHeadersFromBytes(languageName, sourceCode, numContextLines)
}

func GetFileHeadersStringFromBytes(languageName string, sourceCode []byte, numContextLines int) (string, error) {
	return parser.GetFileHeadersStringFromBytes(languageName, sourceCode, numContextLines)
}

func FormatHeaders(headers []SourceBlock) string {
	return parser.FormatHeaders(headers)
}

func GetSymbolDefinitionsFromBytes(languageName string, sourceCode []byte, symbolName string, numContextLines int) ([]SourceBlock, error) {
	return parser.GetSymbolDefinitionsFromBytes(languageName, sourceCode, symbolName, numContextLines)
}

func GetSymbolDefinitionsStringFromBytes(languageName string, sourceCode []byte, symbolName string, numContextLines int) (string, error) {
	return parser.GetSymbolDefinitionsStringFromBytes(languageName, sourceCode, symbolName, numContextLines)
}

func GetAllSymbolDefinitionsFromSource(languageName string, sourceCode []byte) ([]SymbolDefinition, error) {
	return parser.GetAllSymbolDefinitionsFromSource(languageName, sourceCode)
}

func FindSymbolOccurrences(languageName string, sourceCode []byte, symbolName string) ([]SymbolOccurrence, error) {
	return parser.FindSymbolOccurrences(languageName, sourceCode, symbolName)
}

func GetFileSymbolsFromBytes(filePath string, languageName string, sourceCode []byte) ([]Symbol, error) {
	return parser.GetFileSymbolsFromBytes(filePath, languageName, sourceCode)
}

func GetAllAlternativeFileSymbolsFromBytes(filePath string, languageName string, sourceCode []byte) ([]Symbol, error) {
	return parser.GetAllAlternativeFileSymbolsFromBytes(filePath, languageName, sourceCode)
}

func GetFileSymbolsStringFromBytes(filePath string, languageName string, sourceCode []byte) (string, error) {
	return parser.GetFileSymbolsStringFromBytes(filePath, languageName, sourceCode)
}

func FormatSymbols(symbols []Symbol) string {
	return parser.FormatSymbols(symbols)
}

func NormalizeSymbolFromSnippet(languageName string, snippet string) (string, error) {
	return parser.NormalizeSymbolFromSnippet(languageName, snippet)
}

func NormalizeMarkdownSymbol(input string) string {
	return parser.NormalizeMarkdownSymbol(input)
}

func GetFileSignaturesFromBytes(languageName string, sourceCode []byte) ([]Signature, error) {
	return parser.GetFileSignaturesFromBytes(languageName, sourceCode)
}

func GetFileSignaturesStringFromBytes(languageName string, sourceCode []byte) (string, error) {
	return parser.GetFileSignaturesStringFromBytes(languageName, sourceCode)
}

func FormatSignatures(signatures []Signature) string {
	return parser.FormatSignatures(signatures)
}

func MergeAdjacentOrOverlappingSourceBlocks(sourceBlocks []SourceBlock, sourceCodeLines []string) []SourceBlock {
	return parser.MergeAdjacentOrOverlappingSourceBlocks(sourceBlocks, sourceCodeLines)
}

func ExpandContextLines(sourceBlocks []SourceBlock, numContextLines int, sourceCode []byte) []SourceBlock {
	return parser.ExpandContextLines(sourceBlocks, numContextLines, sourceCode)
}

func ExtractSourceCodes(allContent string) []SourceCode {
	return parser.ExtractSourceCodes(allContent)
}

func SymbolizeEmbeddedCode(content string) string {
	return parser.SymbolizeEmbeddedCode(content)
}

func ShrinkEmbeddedCodeContext(content string, longestFirst bool, maxLength int) (string, bool) {
	return parser.ShrinkEmbeddedCodeContext(content, longestFirst, maxLength)
}
