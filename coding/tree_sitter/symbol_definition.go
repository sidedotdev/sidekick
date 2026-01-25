package tree_sitter

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/cbroglie/mustache"
	"github.com/rs/zerolog/log"
	tree_sitter "github.com/tree-sitter/go-tree-sitter"
	tree_sitter_typescript "github.com/tree-sitter/tree-sitter-typescript/bindings/go"
)

func GetSymbolDefinitions(filePath string, symbolName string, numContextLines int) ([]SourceBlock, error) {
	sourceCodeBytes, err := os.ReadFile(filePath)
	if err != nil {
		// TODO return specific err var if file not found
		return []SourceBlock{}, fmt.Errorf("failed to obtain source code when getting symbol definitions: %v", err)
	}
	languageName, sitterLanguage, err := inferLanguageFromFilePath(filePath)
	if err != nil {
		return []SourceBlock{}, err
	}
	parser := tree_sitter.NewParser()
	defer parser.Close()
	parser.SetLanguage(sitterLanguage)
	tree := parser.Parse(sourceTransform(languageName, &sourceCodeBytes), nil)
	if tree != nil {
		defer tree.Close()
	}
	symbolDefinitions, err := getSymbolDefinitionsInternal(languageName, sitterLanguage, tree, &sourceCodeBytes, symbolName)
	if err != nil {
		return nil, err
	}

	symbolDefinitions = ExpandContextLines(symbolDefinitions, numContextLines, sourceCodeBytes)
	// TODO call this in BulkGetSymbolDefinitions
	//sourceCodeLines := strings.Split(string(sourceCodeBytes), "\n")
	//symbolDefinitions = mergeAdjacentOrOverlappingSourceBlocks(symbolDefinitions, sourceCodeLines)

	return symbolDefinitions, nil
}

func GetSymbolDefinitionsString(filePath string, symbolName string, numContextLines int) (string, error) {
	symbolDefinitions, err := GetSymbolDefinitions(filePath, symbolName, numContextLines)
	if err != nil {
		return "", err
	}

	out := strings.Builder{}
	for _, symbolDefinition := range symbolDefinitions {
		out.WriteString(symbolDefinition.String())
	}

	return out.String(), nil
}

func getSymbolDefinitionsInternal(languageName string, sitterLanguage *tree_sitter.Language, tree *tree_sitter.Tree, sourceCode *[]byte, symbolName string) ([]SourceBlock, error) {
	childSymbolName, parentSymbolName := splitSymbolNameIntoChildAndParent(symbolName)
	if parentSymbolName != "" {
		return getSymbolDefinitionsWithParent(languageName, sitterLanguage, tree, sourceCode, childSymbolName, parentSymbolName)
	}

	if languageName == "markdown" {
		return getMarkdownSymbolDefinitions(sitterLanguage, tree, sourceCode, symbolName)
	}

	queryString, err := renderSymbolDefinitionQuery(languageName, symbolName)
	if err != nil {
		return nil, fmt.Errorf("error rendering symbol definition query: %w", err)
	}
	q, qErr := tree_sitter.NewQuery(sitterLanguage, queryString)
	if qErr != nil {
		return nil, fmt.Errorf("error creating sitter symbol definition query: %s", qErr.Message)
	}
	defer q.Close()
	sourceBlocks := symbolDefinitionSourceBlocks(q, tree, sourceCode, symbolName)

	if len(sourceBlocks) == 0 {
		embeddedSourceBlocks, err := getEmbeddedLanguageSymbolDefinition(languageName, tree, sourceCode, symbolName)
		if err != nil {
			return nil, fmt.Errorf("error getting embedded language symbol definition: %w", err)
		}
		if len(embeddedSourceBlocks) == 0 {
			return nil, fmt.Errorf(`symbol not found: %s`, symbolName)
		}
		return embeddedSourceBlocks, nil
	}

	return sourceBlocks, nil
}

func getSymbolDefinitionsWithParent(languageName string, sitterLanguage *tree_sitter.Language, tree *tree_sitter.Tree, sourceCode *[]byte, childSymbolName, parentSymbolName string) ([]SourceBlock, error) {
	queryString, err := renderSymbolDefinitionWithParentQuery(languageName, childSymbolName, parentSymbolName)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			// parent-specific queries for this language don't yet exist, so we try again without it
			log.Warn().Msgf("no parent-specific symbol definition query for %s, trying without parent", languageName)
			return getSymbolDefinitionsInternal(languageName, sitterLanguage, tree, sourceCode, childSymbolName)
		}
		return nil, fmt.Errorf("error rendering symbol definition query: %w", err)
	}
	q, qErr := tree_sitter.NewQuery(sitterLanguage, queryString)
	if qErr != nil {
		return nil, fmt.Errorf("error creating sitter symbol definition query: %s", qErr.Message)
	}
	defer q.Close()
	sourceBlocks := symbolDefinitionSourceBlocks(q, tree, sourceCode, childSymbolName)

	if len(sourceBlocks) > 0 {
		return sourceBlocks, nil
	}

	// if there is a parent specifier, but we didn't find a symbol with the
	// parent specifier, we try again without it recursively.
	// NOTE: this really only works for a single parent, not multiple levels
	// with grandparent etc, since the query will fail to match a single
	// string containing all with a single specific symbol name.  but we
	// could adjust so we have a list of ancestors instead if we needed
	// this.
	return getSymbolDefinitionsInternal(languageName, sitterLanguage, tree, sourceCode, childSymbolName)
}

func splitSymbolNameIntoChildAndParent(symbolName string) (string, string) {
	// if the symbol name contains a dot, we assume it's a parent specifier. we may wish to add more here per language, eg ->, ::, etc
	if strings.Contains(symbolName, ".") {
		parts := strings.Split(symbolName, ".")
		parentSymbolName := parts[0]
		childSymbolName := strings.Join(parts[1:], ".")
		return childSymbolName, parentSymbolName
	}
	return symbolName, ""
}

// getMarkdownSymbolDefinitions handles markdown-specific symbol definition retrieval.
// It normalizes the symbol name and filters matches in Go since tree-sitter queries
// cannot perform slugification.
func getMarkdownSymbolDefinitions(sitterLanguage *tree_sitter.Language, tree *tree_sitter.Tree, sourceCode *[]byte, symbolName string) ([]SourceBlock, error) {
	queryString, err := renderSymbolDefinitionQuery("markdown", "")
	if err != nil {
		return nil, fmt.Errorf("error rendering markdown symbol definition query: %w", err)
	}
	q, qErr := tree_sitter.NewQuery(sitterLanguage, queryString)
	if qErr != nil {
		return nil, fmt.Errorf("error creating markdown symbol definition query: %s", qErr.Message)
	}
	defer q.Close()

	// Normalize the target symbol name
	normalizedTarget := NormalizeMarkdownSymbol(symbolName)

	// Collect all heading candidates with their metadata
	candidates := collectMarkdownDefinitionCandidates(q, tree, sourceCode)

	// Filter candidates by normalized symbol name
	var matches []markdownDefinitionCandidate
	for _, c := range candidates {
		if c.normalizedSymbol == normalizedTarget {
			matches = append(matches, c)
		}
	}

	if len(matches) == 0 {
		return nil, fmt.Errorf("symbol not found: %s", symbolName)
	}

	// Compute section ranges and convert to SourceBlocks
	sourceBlocks := computeMarkdownDefinitionRanges(matches, candidates, sourceCode)
	return sourceBlocks, nil
}

type markdownDefinitionCandidate struct {
	normalizedSymbol string
	headingLevel     int
	startRow         uint
	startByte        uint
	endRow           uint
	endByte          uint
	isFrontmatter    bool
}

func collectMarkdownDefinitionCandidates(q *tree_sitter.Query, tree *tree_sitter.Tree, sourceCode *[]byte) []markdownDefinitionCandidate {
	var candidates []markdownDefinitionCandidate
	qc := tree_sitter.NewQueryCursor()
	defer qc.Close()
	matches := qc.Matches(q, tree.RootNode(), *sourceCode)

	for match := matches.Next(); match != nil; match = matches.Next() {
		var headingContent string
		var headingLevel int
		var definitionNode *tree_sitter.Node
		var isFrontmatter bool

		for _, c := range match.Captures {
			name := q.CaptureNames()[c.Index]
			switch name {
			case "heading.content", "setext_heading.content":
				headingContent = c.Node.Utf8Text(*sourceCode)
			case "heading.marker":
				headingLevel = getMarkdownHeadingLevel(c.Node.Utf8Text(*sourceCode))
			case "setext_heading.underline":
				underline := c.Node.Utf8Text(*sourceCode)
				if len(underline) > 0 && underline[0] == '=' {
					headingLevel = 1
				} else {
					headingLevel = 2
				}
			case "definition":
				definitionNode = &c.Node
			case "frontmatter.definition":
				definitionNode = &c.Node
				isFrontmatter = true
			}
		}

		if definitionNode != nil {
			var normalizedSymbol string
			if isFrontmatter {
				normalizedSymbol = "yaml_frontmatter"
			} else if headingContent != "" {
				slug := slugifyHeading(headingContent)
				normalizedSymbol = "#" + slug
			}

			if normalizedSymbol != "" {
				candidates = append(candidates, markdownDefinitionCandidate{
					normalizedSymbol: normalizedSymbol,
					headingLevel:     headingLevel,
					startRow:         definitionNode.StartPosition().Row,
					startByte:        definitionNode.StartByte(),
					endRow:           definitionNode.EndPosition().Row,
					endByte:          definitionNode.EndByte(),
					isFrontmatter:    isFrontmatter,
				})
			}
		}
	}

	return candidates
}

// computeMarkdownDefinitionRanges computes section ranges for matched headings.
// Each heading's range extends from its start to just before the next heading
// of the same or higher level (or EOF). Frontmatter uses its captured node range.
func computeMarkdownDefinitionRanges(matches, allCandidates []markdownDefinitionCandidate, sourceCode *[]byte) []SourceBlock {
	// Count total lines in source
	totalLines := uint(strings.Count(string(*sourceCode), "\n"))
	if len(*sourceCode) > 0 && (*sourceCode)[len(*sourceCode)-1] != '\n' {
		totalLines++
	}

	var sourceBlocks []SourceBlock

	for _, m := range matches {
		var endRow, endByte uint

		if m.isFrontmatter {
			// Frontmatter uses its captured node range exactly
			endRow = m.endRow
			endByte = m.endByte
		} else {
			// Headings extend to the next heading of same or higher level (or EOF)
			endRow = totalLines
			endByte = uint(len(*sourceCode))

			for _, c := range allCandidates {
				if c.isFrontmatter || c.startRow <= m.startRow {
					continue
				}
				if c.headingLevel <= m.headingLevel {
					endRow = c.startRow
					endByte = c.startByte
					break
				}
			}
		}

		sourceBlocks = append(sourceBlocks, SourceBlock{
			Source: sourceCode,
			Range: tree_sitter.Range{
				StartPoint: tree_sitter.Point{Row: m.startRow, Column: 0},
				EndPoint:   tree_sitter.Point{Row: endRow, Column: 0},
				StartByte:  m.startByte,
				EndByte:    endByte,
			},
		})
	}

	return sourceBlocks
}

func symbolDefinitionSourceBlocks(q *tree_sitter.Query, tree *tree_sitter.Tree, sourceCode *[]byte, symbolName string) []SourceBlock {
	var sourceBlocks []SourceBlock
	qc := tree_sitter.NewQueryCursor()
	defer qc.Close()
	matches := qc.Matches(q, tree.RootNode(), *sourceCode)

	// Iterate over query results
	var tempSourceBlock *SourceBlock
	for match := matches.Next(); match != nil; match = matches.Next() {
		var nameRange tree_sitter.Range
		for _, c := range match.Captures {
			name := q.CaptureNames()[c.Index]
			if name == "name" {
				nameRange = tree_sitter.Range{
					StartPoint: c.Node.StartPosition(),
					EndPoint:   c.Node.EndPosition(),
					StartByte:  c.Node.StartByte(),
					EndByte:    c.Node.EndByte(),
				}
			} else if name == "definition" || name == symbolName {
				sourceBlock := SourceBlock{
					Source: sourceCode,
					Range: tree_sitter.Range{
						StartPoint: c.Node.StartPosition(),
						EndPoint:   c.Node.EndPosition(),
						StartByte:  c.Node.StartByte(),
						EndByte:    c.Node.EndByte(),
					},
					NameRange: &nameRange,
				}
				if c.Node.Kind() == "comment" {
					tempSourceBlock = &sourceBlock
				} else {
					if tempSourceBlock != nil {
						//sourceBlock.Range.StartPoint.Row = min(tempSourceBlock.Range.StartPoint.Row, sourceBlock.Range.StartPoint.Row)
						//sourceBlock.Range.EndPoint.Row = max(tempSourceBlock.Range.EndPoint.Row, sourceBlock.Range.EndPoint.Row)
						sourceBlock.Range.StartByte = tempSourceBlock.Range.StartByte
						sourceBlock.Range.StartPoint = tempSourceBlock.Range.StartPoint

						tempSourceBlock = nil
					}
					sourceBlocks = append(sourceBlocks, sourceBlock)
				}
			}
		}
	}

	return sourceBlocks
}

func getEmbeddedLanguageSymbolDefinition(languageName string, tree *tree_sitter.Tree, sourceCode *[]byte, symbolName string) ([]SourceBlock, error) {
	switch languageName {
	case "vue":
		{
			return getVueEmbeddedLanguageSymbolDefinition(tree, sourceCode, symbolName)
		}
	}
	return []SourceBlock{}, nil
}

func getVueEmbeddedLanguageSymbolDefinition(vueTree *tree_sitter.Tree, sourceCode *[]byte, symbolName string) ([]SourceBlock, error) {
	// Call GetVueEmbeddedTypescriptTree to get the embedded typescript
	tsTree, err := GetVueEmbeddedTypescriptTree(vueTree, sourceCode)
	if err != nil {
		return []SourceBlock{}, err
	}
	if tsTree == nil {
		return []SourceBlock{}, nil
	}

	tsLang := tree_sitter.NewLanguage(tree_sitter_typescript.LanguageTypescript())
	return getSymbolDefinitionsInternal("typescript", tsLang, tsTree, sourceCode, symbolName)
}

//go:embed symbol_definition_queries/*
var symbolDefinitionQueriesFS embed.FS

func renderSymbolDefinitionQuery(languageName, symbolName string) (string, error) {
	// Read the mustache template file
	templatePath := fmt.Sprintf("symbol_definition_queries/symbol_definition_%s.scm.mustache", languageName)
	templateBytes, err := symbolDefinitionQueriesFS.ReadFile(templatePath)
	if err != nil {
		return "", fmt.Errorf("error reading mustache template file: %w", err)
	}

	// Render the mustache template
	rendered, err := mustache.Render(string(templateBytes), map[string]interface{}{
		"SymbolName": symbolName,
		// hack to simulate boolean conditions in mustache
		fmt.Sprintf("SymbolName=%s", symbolName): true,
	})
	if err != nil {
		return "", fmt.Errorf("error rendering mustache template: %w", err)
	}

	return rendered, nil
}

func renderSymbolDefinitionWithParentQuery(languageName, childSymbolName, parentSymbolName string) (string, error) {
	// Read the mustache template file
	templatePath := fmt.Sprintf("symbol_definition_queries/symbol_definition_with_parent_%s.scm.mustache", languageName)
	templateBytes, err := symbolDefinitionQueriesFS.ReadFile(templatePath)
	if err != nil {
		return "", fmt.Errorf("error reading mustache template file: %w", err)
	}

	// Render the mustache template
	rendered, err := mustache.Render(string(templateBytes), map[string]interface{}{
		"childSymbolName":  childSymbolName,
		"parentSymbolName": parentSymbolName,
		// hack to simulate boolean conditions in mustache
		fmt.Sprintf("childSymbolName=%s", childSymbolName):   true,
		fmt.Sprintf("parentSymbolName=%s", parentSymbolName): true,
	})
	if err != nil {
		return "", fmt.Errorf("error rendering mustache template: %w", err)
	}

	return rendered, nil
}

type FileSymbols struct {
	FilePath string
	Symbols  []string
}

// SymbolDefinition represents a symbol with its full definition range and name.
type SymbolDefinition struct {
	SourceBlock
	SymbolName string
}

// GetAllSymbolDefinitions returns all symbol definitions in a file with their ranges.
// This is useful for determining which symbols overlap with changed line ranges.
func GetAllSymbolDefinitions(filePath string) ([]SymbolDefinition, error) {
	languageName, sitterLanguage, err := inferLanguageFromFilePath(filePath)
	if err != nil {
		return nil, err
	}
	sourceCode, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read source code: %w", err)
	}
	parser := tree_sitter.NewParser()
	defer parser.Close()
	parser.SetLanguage(sitterLanguage)
	transformedSource := sourceTransform(languageName, &sourceCode)
	tree := parser.Parse(transformedSource, nil)
	if tree != nil {
		defer tree.Close()
	}
	return getAllSymbolDefinitionsInternal(languageName, sitterLanguage, tree, &transformedSource)
}

// GetAllSymbolDefinitionsFromSource returns all symbol definitions from source code content.
func GetAllSymbolDefinitionsFromSource(languageName string, sourceCode []byte) ([]SymbolDefinition, error) {
	sitterLanguage, err := getSitterLanguage(languageName)
	if err != nil {
		return nil, err
	}
	parser := tree_sitter.NewParser()
	defer parser.Close()
	parser.SetLanguage(sitterLanguage)
	transformedSource := sourceTransform(languageName, &sourceCode)
	tree := parser.Parse(transformedSource, nil)
	if tree != nil {
		defer tree.Close()
	}
	return getAllSymbolDefinitionsInternal(languageName, sitterLanguage, tree, &transformedSource)
}

func getAllSymbolDefinitionsInternal(languageName string, sitterLanguage *tree_sitter.Language, tree *tree_sitter.Tree, sourceCode *[]byte) ([]SymbolDefinition, error) {
	// Render query with empty SymbolName to get all symbols
	queryString, err := renderSymbolDefinitionQuery(languageName, "")
	if err != nil {
		return nil, fmt.Errorf("error rendering symbol definition query: %w", err)
	}

	q, qErr := tree_sitter.NewQuery(sitterLanguage, queryString)
	if qErr != nil {
		return nil, fmt.Errorf("error creating sitter symbol definition query: %s", qErr.Message)
	}
	defer q.Close()

	var definitions []SymbolDefinition
	qc := tree_sitter.NewQueryCursor()
	defer qc.Close()
	matches := qc.Matches(q, tree.RootNode(), *sourceCode)

	var tempSourceBlock *SourceBlock
	var tempSymbolName string
	for match := matches.Next(); match != nil; match = matches.Next() {
		// First pass: collect name and definition captures from this match
		var nameRange tree_sitter.Range
		var symbolName string
		var definitionNode *tree_sitter.Node

		for _, c := range match.Captures {
			captureName := q.CaptureNames()[c.Index]
			if captureName == "name" {
				nameRange = tree_sitter.Range{
					StartPoint: c.Node.StartPosition(),
					EndPoint:   c.Node.EndPosition(),
					StartByte:  c.Node.StartByte(),
					EndByte:    c.Node.EndByte(),
				}
				symbolName = c.Node.Utf8Text(*sourceCode)
			} else if captureName == "definition" {
				definitionNode = &c.Node
			}
		}

		// Second pass: process the definition now that we have the name
		if definitionNode != nil {
			sourceBlock := SourceBlock{
				Source: sourceCode,
				Range: tree_sitter.Range{
					StartPoint: definitionNode.StartPosition(),
					EndPoint:   definitionNode.EndPosition(),
					StartByte:  definitionNode.StartByte(),
					EndByte:    definitionNode.EndByte(),
				},
				NameRange: &nameRange,
			}
			if definitionNode.Kind() == "comment" {
				tempSourceBlock = &sourceBlock
				tempSymbolName = symbolName
			} else {
				if tempSourceBlock != nil {
					sourceBlock.Range.StartByte = tempSourceBlock.Range.StartByte
					sourceBlock.Range.StartPoint = tempSourceBlock.Range.StartPoint
					tempSourceBlock = nil
				}
				definitions = append(definitions, SymbolDefinition{
					SourceBlock: sourceBlock,
					SymbolName:  symbolName,
				})
				// Reset tempSymbolName after use
				if tempSymbolName != "" {
					tempSymbolName = ""
				}
			}
		}
	}

	return definitions, nil
}
