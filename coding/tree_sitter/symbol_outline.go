package tree_sitter

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sidekick/coding/tree_sitter/language_bindings/vue"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/golang"
	"github.com/smacker/go-tree-sitter/java"
	"github.com/smacker/go-tree-sitter/python"
	"github.com/smacker/go-tree-sitter/typescript/typescript"
)

type SourceCapture struct {
	Name    string
	Content string
}

type Symbol struct {
	Content        string
	SymbolType     string
	StartPoint     sitter.Point
	EndPoint       sitter.Point
	SourceCaptures []SourceCapture
	Declaration    Declaration
}

type Declaration struct {
	StartPoint sitter.Point
	EndPoint   sitter.Point
}

func GetFileSymbols(filePath string) ([]Symbol, error) {
	languageName, sitterLanguage, err := inferLanguageFromFilePath(filePath)
	if err != nil {
		return nil, err
	}
	sourceCode, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to obtain source code when getting symbol definition for file %s: %v", filePath, err)
	}
	parser := sitter.NewParser()
	parser.SetLanguage(sitterLanguage)
	tree, err := parser.ParseCtx(context.Background(), nil, sourceTransform(languageName, &sourceCode))
	if err != nil {
		return nil, err
	}
	symbolSlice, err := getSourceSymbolsInternal(languageName, sitterLanguage, tree, &sourceCode)
	if err != nil {
		return nil, err
	}

	filenameSymbols, err := getFilenameSymbols(languageName, filePath)
	if err != nil {
		return nil, err
	}
	symbolSlice = append(symbolSlice, filenameSymbols...)

	return symbolSlice, nil
}

func GetAllAlternativeFileSymbols(filePath string) ([]Symbol, error) {
	languageName, sitterLanguage, err := inferLanguageFromFilePath(filePath)
	if err != nil {
		return nil, err
	}
	sourceCode, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to obtain source code when getting symbol definition for file %s: %v", filePath, err)
	}
	parser := sitter.NewParser()
	parser.SetLanguage(sitterLanguage)
	tree, err := parser.ParseCtx(context.Background(), nil, sourceTransform(languageName, &sourceCode))
	if err != nil {
		return nil, err
	}
	symbolSlice, err := getSourceSymbolsInternal(languageName, sitterLanguage, tree, &sourceCode)
	if err != nil {
		return nil, err
	}

	filenameSymbols, err := getFilenameSymbols(languageName, filePath)
	if err != nil {
		return nil, err
	}
	symbolSlice = append(symbolSlice, filenameSymbols...)

	symbolSlice = expandWithAlternativeSymbols(languageName, sitterLanguage, tree, &sourceCode, symbolSlice)

	return symbolSlice, nil
}

// Generates alternative forms of the symbol name to maximize the chance of
// finding the symbol in the code based on LLM output
// TODO: provide the canonical form of the symbol name as well in the result (need a new type or field for this)
// TODO: make this language-specific
// TODO in golang, append the module name to the symbol name with a dot as well as another alternative symbol
func expandWithAlternativeSymbols(languageName string, sitterLanguage *sitter.Language, tree *sitter.Tree, sourceCode *[]byte, symbolSlice []Symbol) []Symbol {
	for _, symbol := range symbolSlice {
		if symbol.SymbolType == "method" {
			// Extract the receiver type and method name using the symbol.SourceCaptures
			var receiverMaybePointer, receiverNoPointer, receiverTypeNoPointer, methodName string
			for _, capture := range symbol.SourceCaptures {
				if capture.Name == "method.receiver" {
					receiverMaybePointer = capture.Content
					receiverNoPointer = strings.ReplaceAll(capture.Content, "*", "")
				} else if capture.Name == "method.receiver_type" {
					receiverTypeNoPointer = strings.Trim(capture.Content, "*")
				} else if capture.Name == "method.name" {
					methodName = capture.Content
				}
			}

			// If we didn't find the receiver type or method name, continue to the next symbol
			if receiverTypeNoPointer == "" || methodName == "" {
				continue
			}

			// Generate the alternative symbols
			alternativeSymbols := []string{
				fmt.Sprintf("%s.%s", receiverMaybePointer, methodName),
				fmt.Sprintf("%s.%s", receiverNoPointer, methodName),
				fmt.Sprintf("%s %s", receiverMaybePointer, methodName),
				fmt.Sprintf("%s %s", receiverNoPointer, methodName),
				fmt.Sprintf("(%s).%s", receiverTypeNoPointer, methodName),
				fmt.Sprintf("(*%s).%s", receiverTypeNoPointer, methodName),
				fmt.Sprintf("(%s) %s", receiverTypeNoPointer, methodName),
				fmt.Sprintf("(*%s) %s", receiverTypeNoPointer, methodName),
				fmt.Sprintf("%s.%s", receiverTypeNoPointer, methodName),
				fmt.Sprintf("*%s.%s", receiverTypeNoPointer, methodName),
				fmt.Sprintf("%s %s", receiverTypeNoPointer, methodName),
				fmt.Sprintf("*%s %s", receiverTypeNoPointer, methodName),
				methodName,
			}

			// Add the alternative symbols to the symbol slice
			for _, altSymbol := range alternativeSymbols {
				symbolSlice = append(symbolSlice, Symbol{
					Content:    altSymbol,
					SymbolType: "alt_" + symbol.SymbolType,
					StartPoint: symbol.StartPoint,
					EndPoint:   symbol.EndPoint,
				})
			}
		}
	}

	// return unique symbols
	uniqueSymbols := make(map[string]Symbol)
	for _, symbol := range symbolSlice {
		uniqueSymbols[symbol.Content] = symbol
	}
	var uniqueSymbolSlice []Symbol
	for _, symbol := range uniqueSymbols {
		uniqueSymbolSlice = append(uniqueSymbolSlice, symbol)
	}
	return uniqueSymbolSlice
}

func GetFileSymbolsString(filePath string) (string, error) {
	symbolSlice, err := GetFileSymbols(filePath)
	if err != nil {
		return "", err
	}
	return FormatSymbols(symbolSlice), nil
}

func getSourceSymbolsInternal(languageName string, sitterLanguage *sitter.Language, tree *sitter.Tree, sourceCode *[]byte) ([]Symbol, error) {
	// NOTE: signature query does double-duty as the symbol query. FIXME should be renamed!
	queryString, err := getSignatureQuery(languageName)
	if err != nil {
		return []Symbol{}, fmt.Errorf("error rendering symbol definition query: %w", err)
	}

	q, err := sitter.NewQuery([]byte(queryString), sitterLanguage)
	if err != nil {
		return []Symbol{}, fmt.Errorf("error creating sitter symbol definition query: %w", err)
	}

	var symbols []Symbol
	qc := sitter.NewQueryCursor()
	qc.Exec(q, tree.RootNode())
	// Iterate over query results
	for {
		m, ok := qc.NextMatch()
		if !ok {
			break
		}

		sigWriter := strings.Builder{}

		// Apply predicates filtering
		m = qc.FilterPredicates(m, *sourceCode)
		var names []string
		var sourceCaptures []SourceCapture
		var declaration Declaration
		for _, c := range m.Captures {
			name := q.CaptureNameForId(c.Index)
			names = append(names, name)
			content := c.Node.Content(*sourceCode)
			if strings.HasSuffix(name, ".declaration") {
				declaration = Declaration{
					StartPoint: c.Node.StartPoint(),
					EndPoint:   c.Node.EndPoint(),
				}
			} else {
				sourceCaptures = append(sourceCaptures, SourceCapture{
					Name:    name,
					Content: content,
				})
			}
			writeSymbolCapture(languageName, &sigWriter, sourceCode, c, name)
		}
		if sigWriter.Len() > 0 {
			symbol := Symbol{
				Content:        sigWriter.String(),
				SymbolType:     getSymbolType(languageName, names),
				StartPoint:     sitter.Point{},
				EndPoint:       sitter.Point{},
				SourceCaptures: sourceCaptures,
				Declaration:    declaration,
			}
			symbols = append(symbols, symbol)
		}
	}

	embeddedSymbols, err := getEmbeddedLanguageSymbols(languageName, tree, sourceCode)
	if err != nil {
		return nil, fmt.Errorf("error getting embedded language file map: %w", err)
	}
	symbols = append(symbols, embeddedSymbols...)

	return symbols, nil
}

// cross language symbol types
var nameToSymbolType map[string]string = map[string]string{
	"function.name":   "function",
	"method.name":     "method",
	"type.name":       "type",
	"type_alias.name": "type",
	"lexical.name":    "variable",
	"var.name":        "non_lexical_variable",
	"const.name":      "const_variable",
	"interface.name":  "interface",
	"class.name":      "class",
	"enum.name":       "enum",
}

func getSymbolType(languageName string, names []string) string {
	for _, name := range names {
		if symbolType, ok := nameToSymbolType[name]; ok {
			return symbolType
		}
	}
	return ""
}

func writeSymbolCapture(languageName string, out *strings.Builder, sourceCode *[]byte, c sitter.QueryCapture, name string) {
	//out.WriteString(name + "\n")
	//out.WriteString(c.Node.Type() + "\n")
	switch languageName {
	case "golang":
		{
			writeGolangSymbolCapture(out, sourceCode, c, name)
		}
	case "typescript":
		{
			writeTypescriptSymbolCapture(out, sourceCode, c, name)
		}
	case "vue":
		{
			writeVueSymbolCapture(out, sourceCode, c, name)
		}
	case "python":
		{
			writePythonSymbolCapture(out, sourceCode, c, name)
		}
	default:
		{
			// NOTE this is expected to provide quite bad output until tweaked per language
			if strings.HasSuffix(name, ".name") {
				out.WriteString(c.Node.Content(*sourceCode))
			}
		}
	}
}

func FormatSymbols(symbols []Symbol) string {
	var out strings.Builder
	for i, symbol := range symbols {
		out.WriteString(symbol.Content)
		if i < len(symbols)-1 {
			out.WriteString(", ")
		}
	}
	return out.String()
}

// getEmbeddedLanguageSymbols is a placeholder function for getting embedded language symbols.
// This function needs to be implemented.
func getEmbeddedLanguageSymbols(languageName string, tree *sitter.Tree, sourceCode *[]byte) ([]Symbol, error) {
	switch languageName {
	case "vue":
		{
			return getVueEmbeddedLanguageSymbols(tree, sourceCode)
		}
	}
	return []Symbol{}, nil
}

func (sc SourceCode) GetSymbols() ([]Symbol, error) {
	sitterLanguage, err := getSitterLanguage(sc.LanguageName)
	if err != nil {
		return nil, err
	}
	parser := sitter.NewParser()
	parser.SetLanguage(sitterLanguage)
	sourceBytes := []byte(sc.Content)
	tree, err := parser.ParseCtx(context.Background(), nil, sourceTransform(sc.LanguageName, &sourceBytes))
	if err != nil {
		return nil, err
	}
	symbolSlice, err := getSourceSymbolsInternal(sc.LanguageName, sitterLanguage, tree, &sourceBytes)
	if err != nil {
		return nil, err
	}

	return symbolSlice, nil
}

func normalizeLanguageName(s string) string {
	switch s {
	case "go", "golang":
		return "golang"
	case "py", "python":
		return "python"
	case "java":
		return "java"
	case "ts", "typescript":
		return "typescript"
	default:
		return s
	}
}

func getSitterLanguage(languageName string) (*sitter.Language, error) {
	switch languageName {
	case "go", "golang":
		return golang.GetLanguage(), nil
	case "py", "python":
		return python.GetLanguage(), nil
	case "java":
		return java.GetLanguage(), nil
	case "ts", "typescript":
		return typescript.GetLanguage(), nil
	case "vue":
		return vue.GetLanguage(), nil
	// Add more languages as needed
	default:
		return nil, fmt.Errorf("unsupported language: %s", languageName)
	}
}

func getFilenameSymbols(langName, filename string) ([]Symbol, error) {
	filenameSymbols := []Symbol{}

	switch langName {
	case "vue", "svelte", "riot", "marko":
		maybeComponentName := strings.TrimSuffix(filepath.Base(filename), filepath.Ext(filename))
		maybeComponentName = strings.ReplaceAll(maybeComponentName, "-", "")
		maybeComponentName = strings.ReplaceAll(maybeComponentName, "_", "")
		maybeComponentName = strings.ToLower(maybeComponentName)

		if maybeComponentName != "" {
			contents, err := os.ReadFile(filename)
			if err != nil {
				return nil, fmt.Errorf("failed to read file %s: %w", filename, err)
			}
			lines := strings.Split(string(contents), "\n")
			filenameSymbols = append(filenameSymbols, Symbol{
				Content:    maybeComponentName,
				SymbolType: "sfc",
				Declaration: Declaration{
					StartPoint: sitter.Point{},
					EndPoint: sitter.Point{
						Row:    uint32(len(lines) - 1),
						Column: uint32(len(lines[len(lines)-1]) - 1),
					},
				},
			})
		}
	}

	return filenameSymbols, nil
}
