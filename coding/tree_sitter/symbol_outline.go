package tree_sitter

import (
	"cmp"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sidekick/coding/tree_sitter/language_bindings/vue"
	"slices"
	"strings"

	tree_sitter_kotlin "github.com/tree-sitter-grammars/tree-sitter-kotlin/bindings/go"
	tree_sitter "github.com/tree-sitter/go-tree-sitter"
	tree_sitter_go "github.com/tree-sitter/tree-sitter-go/bindings/go"
	tree_sitter_java "github.com/tree-sitter/tree-sitter-java/bindings/go"
	tree_sitter_python "github.com/tree-sitter/tree-sitter-python/bindings/go"
	tree_sitter_typescript "github.com/tree-sitter/tree-sitter-typescript/bindings/go"
)

type SourceCapture struct {
	Name    string
	Content string
}

type Symbol struct {
	Content        string
	SymbolType     string
	StartPoint     tree_sitter.Point
	EndPoint       tree_sitter.Point
	SourceCaptures []SourceCapture
	Declaration    Declaration
}

type Declaration struct {
	StartPoint tree_sitter.Point
	EndPoint   tree_sitter.Point
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
	parser := tree_sitter.NewParser()
	defer parser.Close()
	parser.SetLanguage(sitterLanguage)
	tree := parser.Parse(sourceTransform(languageName, &sourceCode), nil)
	if tree != nil {
		defer tree.Close()
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
	parser := tree_sitter.NewParser()
	defer parser.Close()
	parser.SetLanguage(sitterLanguage)
	tree := parser.Parse(sourceTransform(languageName, &sourceCode), nil)
	if tree != nil {
		defer tree.Close()
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
func expandWithAlternativeSymbols(languageName string, sitterLanguage *tree_sitter.Language, tree *tree_sitter.Tree, sourceCode *[]byte, symbolSlice []Symbol) []Symbol {
	for _, symbol := range symbolSlice {
		// Handle Kotlin backtick-escaped symbols
		if languageName == "kotlin" {
			content := symbol.Content
			if len(content) >= 2 && strings.HasPrefix(content, "`") && strings.HasSuffix(content, "`") {
				unescaped := strings.Trim(content, "`")
				symbolSlice = append(symbolSlice, Symbol{
					Content:    unescaped,
					SymbolType: "alt_" + symbol.SymbolType,
					StartPoint: symbol.StartPoint,
					EndPoint:   symbol.EndPoint,
				})
			}
		}

		// Handle Markdown heading symbols
		if languageName == "markdown" && (symbol.SymbolType == "heading" || symbol.SymbolType == "setext_heading") {
			var headingContent, headingMarker string
			for _, capture := range symbol.SourceCaptures {
				switch capture.Name {
				case "heading.content":
					headingContent = strings.TrimSpace(capture.Content)
				case "setext_heading.h1.content", "setext_heading.h2.content":
					// Setext heading content may have trailing newlines
					headingContent = strings.TrimSpace(capture.Content)
				case "heading.marker":
					headingMarker = capture.Content
				}
			}

			if headingContent != "" {
				slug := slugifyHeading(headingContent)
				alternativeSymbols := []string{
					headingContent, // Raw heading text without any # prefix
					slug,           // Slug only (without leading #)
				}
				// Original heading line (including # prefix for ATX headings only)
				if headingMarker != "" {
					alternativeSymbols = append(alternativeSymbols, headingMarker+" "+headingContent)
				}

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

func getSourceSymbolsInternal(languageName string, sitterLanguage *tree_sitter.Language, tree *tree_sitter.Tree, sourceCode *[]byte) ([]Symbol, error) {
	// NOTE: signature query does double-duty as the symbol query. FIXME should be renamed!
	queryString, err := getSignatureQuery(languageName, false)
	if err != nil {
		return []Symbol{}, fmt.Errorf("error rendering symbol definition query: %w", err)
	}

	q, qErr := tree_sitter.NewQuery(sitterLanguage, queryString)
	if qErr != nil {
		return []Symbol{}, fmt.Errorf("error creating sitter symbol definition query: %s", qErr.Message)
	}
	defer q.Close()

	var symbols []Symbol
	qc := tree_sitter.NewQueryCursor()
	defer qc.Close()
	matches := qc.Matches(q, tree.RootNode(), []byte(*sourceCode))
	// Iterate over query results
	for match := matches.Next(); match != nil; match = matches.Next() {
		sigWriter := strings.Builder{}

		var names []string
		var sourceCaptures []SourceCapture
		var declaration Declaration
		startPoint := tree_sitter.Point{Row: ^uint(0), Column: ^uint(0)}
		endPoint := tree_sitter.Point{Row: 0, Column: 0}
		for _, c := range match.Captures {
			name := q.CaptureNames()[c.Index]
			names = append(names, name)
			content := c.Node.Utf8Text(*sourceCode)
			if strings.HasSuffix(name, ".declaration") {
				declaration = Declaration{
					StartPoint: c.Node.StartPosition(),
					EndPoint:   c.Node.EndPosition(),
				}
			} else {
				sourceCaptures = append(sourceCaptures, SourceCapture{
					Name:    name,
					Content: content,
				})
			}
			writeSymbolCapture(languageName, &sigWriter, sourceCode, c, name)
			if shouldExtendSymbolRange(languageName, name) {
				if c.Node.StartPosition().Row < startPoint.Row || (c.Node.StartPosition().Row == startPoint.Row && c.Node.StartPosition().Column < startPoint.Column) {
					startPoint = c.Node.StartPosition()
				}
				if c.Node.EndPosition().Row > endPoint.Row || (c.Node.EndPosition().Row == endPoint.Row && c.Node.EndPosition().Column > endPoint.Column) {
					endPoint = c.Node.EndPosition()
				}
			}

		}
		if sigWriter.Len() > 0 {
			symbol := Symbol{
				Content:        sigWriter.String(),
				SymbolType:     getSymbolType(languageName, names),
				StartPoint:     startPoint,
				EndPoint:       endPoint,
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

	// sort symbols by start point
	slices.SortFunc(symbols, func(i, j Symbol) int {
		c := cmp.Compare(i.StartPoint.Row, j.StartPoint.Row)
		if c == 0 {
			c = cmp.Compare(i.StartPoint.Column, j.StartPoint.Column)
		}
		return c
	})

	// Markdown-specific: compute section ranges for headings
	if languageName == "markdown" {
		symbols = computeMarkdownSectionRanges(symbols, sourceCode)
	}

	return symbols, nil
}

func shouldExtendSymbolRange(languageName, captureName string) bool {
	// all top-level name captures should extend the range
	if strings.HasSuffix(captureName, ".name") && strings.Count(captureName, ".") == 1 {
		return true
	}

	switch languageName {
	case "vue":
		{
			// extend the range for <template>, <script>, and <style>
			if captureName == "template" || captureName == "script" || captureName == "style" {
				return true
			}
		}
	case "typescript", "tsx":
		{
			// these capture names just
			if captureName == "interface.declaration" || captureName == "type.declaration" || captureName == "type_alias.declaration" {
				return false
			}
		}
	case "markdown":
		{
			// Markdown heading and frontmatter captures extend the range
			if captureName == "heading.name" || captureName == "setext_heading.name" || captureName == "frontmatter.name" {
				return true
			}
		}
	}

	return false
}

// cross language symbol types
var nameToSymbolType map[string]string = map[string]string{
	"function.name":       "function",
	"method.name":         "method",
	"type.name":           "type",
	"type_alias.name":     "type",
	"lexical.name":        "variable",
	"var.name":            "non_lexical_variable",
	"const.name":          "const_variable",
	"interface.name":      "interface",
	"class.name":          "class",
	"enum.name":           "enum",
	"heading.name":        "heading",
	"setext_heading.name": "setext_heading",
	"frontmatter.name":    "frontmatter",
}

func getSymbolType(languageName string, names []string) string {
	for _, name := range names {
		if symbolType, ok := nameToSymbolType[name]; ok {
			return symbolType
		}
	}
	return ""
}

func writeSymbolCapture(languageName string, out *strings.Builder, sourceCode *[]byte, c tree_sitter.QueryCapture, name string) {
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
	case "tsx":
		{
			writeTsxSymbolCapture(out, sourceCode, c, name)
		}
	case "vue":
		{
			writeVueSymbolCapture(out, sourceCode, c, name)
		}
	case "python":
		{
			writePythonSymbolCapture(out, sourceCode, c, name)
		}
	case "java":
		{
			writeJavaSymbolCapture(out, sourceCode, c, name)
		}
	case "kotlin":
		{
			writeKotlinSymbolCapture(out, sourceCode, c, name)
		}
	case "markdown":
		{
			writeMarkdownSymbolCapture(out, sourceCode, c, name)
		}
	default:
		{
			// NOTE this is expected to provide quite bad output until tweaked per language
			if strings.HasSuffix(name, ".name") {
				out.WriteString(c.Node.Utf8Text(*sourceCode))
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
func getEmbeddedLanguageSymbols(languageName string, tree *tree_sitter.Tree, sourceCode *[]byte) ([]Symbol, error) {
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
	parser := tree_sitter.NewParser()
	defer parser.Close()
	parser.SetLanguage(sitterLanguage)
	sourceBytes := []byte(sc.Content)
	tree := parser.Parse(sourceTransform(sc.LanguageName, &sourceBytes), nil)
	if tree != nil {
		defer tree.Close()
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
	case "md", "markdown":
		return "markdown"
	default:
		return s
	}
}

func NormalizeSymbolFromSnippet(languageName string, snippet string) (string, error) {
	lang := normalizeLanguageName(languageName)
	sc := SourceCode{
		LanguageName: lang,
		Content:      snippet,
	}
	symbols, err := sc.GetSymbols()
	if err != nil {
		return "", err
	}
	if len(symbols) == 0 || symbols[0].Content == "" {
		return "", ErrNoSymbolParsed
	}
	return symbols[0].Content, nil
}

var ErrNoSymbolParsed = errors.New("no symbol parsed")

func getSitterLanguage(languageName string) (*tree_sitter.Language, error) {
	switch languageName {
	case "go", "golang":
		return tree_sitter.NewLanguage(tree_sitter_go.Language()), nil
	case "kt", "kotlin":
		return tree_sitter.NewLanguage(tree_sitter_kotlin.Language()), nil
	case "java":
		return tree_sitter.NewLanguage(tree_sitter_java.Language()), nil
	case "py", "python":
		return tree_sitter.NewLanguage(tree_sitter_python.Language()), nil
	case "ts", "typescript":
		return tree_sitter.NewLanguage(tree_sitter_typescript.LanguageTypescript()), nil
	case "tsx":
		return tree_sitter.NewLanguage(tree_sitter_typescript.LanguageTSX()), nil
	case "vue":
		return tree_sitter.NewLanguage(vue.Language()), nil
	case "md", "markdown":
		return getMarkdownLanguage(), nil
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
					StartPoint: tree_sitter.Point{},
					EndPoint: tree_sitter.Point{
						Row:    uint(len(lines) - 1),
						Column: uint(len(lines[len(lines)-1]) - 1),
					},
				},
			})
		}
	}

	return filenameSymbols, nil
}
