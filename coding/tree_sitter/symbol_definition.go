package tree_sitter

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/cbroglie/mustache"
	"github.com/rs/zerolog/log"
	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/typescript/typescript"
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
	parser := sitter.NewParser()
	parser.SetLanguage(sitterLanguage)
	tree, err := parser.ParseCtx(context.Background(), nil, sourceTransform(languageName, &sourceCodeBytes))
	if err != nil {
		return []SourceBlock{}, err
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

func getSymbolDefinitionsInternal(languageName string, sitterLanguage *sitter.Language, tree *sitter.Tree, sourceCode *[]byte, symbolName string) ([]SourceBlock, error) {
	childSymbolName, parentSymbolName := splitSymbolNameIntoChildAndParent(symbolName)
	if parentSymbolName != "" {
		return getSymbolDefinitionsWithParent(languageName, sitterLanguage, tree, sourceCode, childSymbolName, parentSymbolName)
	}

	queryString, err := renderSymbolDefinitionQuery(languageName, symbolName)
	if err != nil {
		return nil, fmt.Errorf("error rendering symbol definition query: %w", err)
	}
	q, err := sitter.NewQuery([]byte(queryString), sitterLanguage)
	if err != nil {
		return nil, fmt.Errorf("error creating sitter symbol definition query: %w", err)
	}
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

func getSymbolDefinitionsWithParent(languageName string, sitterLanguage *sitter.Language, tree *sitter.Tree, sourceCode *[]byte, childSymbolName, parentSymbolName string) ([]SourceBlock, error) {
	queryString, err := renderSymbolDefinitionWithParentQuery(languageName, childSymbolName, parentSymbolName)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			// parent-specific queries for this language don't yet exist, so we try again without it
			log.Warn().Msgf("no parent-specific symbol definition query for %s, trying without parent", languageName)
			return getSymbolDefinitionsInternal(languageName, sitterLanguage, tree, sourceCode, childSymbolName)
		}
		return nil, fmt.Errorf("error rendering symbol definition query: %w", err)
	}
	q, err := sitter.NewQuery([]byte(queryString), sitterLanguage)
	if err != nil {
		return nil, fmt.Errorf("error creating sitter symbol definition query: %w", err)
	}
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

func symbolDefinitionSourceBlocks(q *sitter.Query, tree *sitter.Tree, sourceCode *[]byte, symbolName string) []SourceBlock {
	var sourceBlocks []SourceBlock
	qc := sitter.NewQueryCursor()
	qc.Exec(q, tree.RootNode())

	// Iterate over query results
	var tempSourceBlock *SourceBlock
	for {
		m, ok := qc.NextMatch()
		if !ok {
			break
		}
		// Apply predicates filtering
		m = qc.FilterPredicates(m, *sourceCode)
		var nameRange sitter.Range
		for _, c := range m.Captures {
			name := q.CaptureNameForId(c.Index)
			if name == "name" {
				nameRange = sitter.Range{
					StartPoint: c.Node.StartPoint(),
					EndPoint:   c.Node.EndPoint(),
					StartByte:  c.Node.StartByte(),
					EndByte:    c.Node.EndByte(),
				}
			} else if name == "definition" || name == symbolName {
				sourceBlock := SourceBlock{
					Source: sourceCode,
					Range: sitter.Range{
						StartPoint: c.Node.StartPoint(),
						EndPoint:   c.Node.EndPoint(),
						StartByte:  c.Node.StartByte(),
						EndByte:    c.Node.EndByte(),
					},
					NameRange: &nameRange,
				}
				if c.Node.Type() == "comment" {
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

func getEmbeddedLanguageSymbolDefinition(languageName string, tree *sitter.Tree, sourceCode *[]byte, symbolName string) ([]SourceBlock, error) {
	switch languageName {
	case "vue":
		{
			return getVueEmbeddedLanguageSymbolDefinition(tree, sourceCode, symbolName)
		}
	}
	return []SourceBlock{}, nil
}

func getVueEmbeddedLanguageSymbolDefinition(vueTree *sitter.Tree, sourceCode *[]byte, symbolName string) ([]SourceBlock, error) {
	// Call GetVueEmbeddedTypescriptTree to get the embedded typescript
	tsTree, err := GetVueEmbeddedTypescriptTree(vueTree, sourceCode)
	if err != nil {
		return []SourceBlock{}, err
	}
	if tsTree == nil {
		return []SourceBlock{}, nil
	}

	return getSymbolDefinitionsInternal("typescript", typescript.GetLanguage(), tsTree, sourceCode, symbolName)
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
	parser := sitter.NewParser()
	parser.SetLanguage(sitterLanguage)
	transformedSource := sourceTransform(languageName, &sourceCode)
	tree, err := parser.ParseCtx(context.Background(), nil, transformedSource)
	if err != nil {
		return nil, fmt.Errorf("failed to parse source code: %w", err)
	}
	return getAllSymbolDefinitionsInternal(languageName, sitterLanguage, tree, &transformedSource)
}

// GetAllSymbolDefinitionsFromSource returns all symbol definitions from source code content.
func GetAllSymbolDefinitionsFromSource(languageName string, sourceCode []byte) ([]SymbolDefinition, error) {
	sitterLanguage, err := getSitterLanguage(languageName)
	if err != nil {
		return nil, err
	}
	parser := sitter.NewParser()
	parser.SetLanguage(sitterLanguage)
	transformedSource := sourceTransform(languageName, &sourceCode)
	tree, err := parser.ParseCtx(context.Background(), nil, transformedSource)
	if err != nil {
		return nil, fmt.Errorf("failed to parse source code: %w", err)
	}
	return getAllSymbolDefinitionsInternal(languageName, sitterLanguage, tree, &transformedSource)
}

func getAllSymbolDefinitionsInternal(languageName string, sitterLanguage *sitter.Language, tree *sitter.Tree, sourceCode *[]byte) ([]SymbolDefinition, error) {
	// Render query with empty SymbolName to get all symbols
	queryString, err := renderSymbolDefinitionQuery(languageName, "")
	if err != nil {
		return nil, fmt.Errorf("error rendering symbol definition query: %w", err)
	}

	q, err := sitter.NewQuery([]byte(queryString), sitterLanguage)
	if err != nil {
		return nil, fmt.Errorf("error creating sitter symbol definition query: %w", err)
	}

	var definitions []SymbolDefinition
	qc := sitter.NewQueryCursor()
	qc.Exec(q, tree.RootNode())

	var tempSourceBlock *SourceBlock
	var tempSymbolName string
	for {
		m, ok := qc.NextMatch()
		if !ok {
			break
		}
		m = qc.FilterPredicates(m, *sourceCode)

		// First pass: collect name and definition captures from this match
		var nameRange sitter.Range
		var symbolName string
		var definitionNode *sitter.Node

		for _, c := range m.Captures {
			captureName := q.CaptureNameForId(c.Index)
			if captureName == "name" {
				nameRange = sitter.Range{
					StartPoint: c.Node.StartPoint(),
					EndPoint:   c.Node.EndPoint(),
					StartByte:  c.Node.StartByte(),
					EndByte:    c.Node.EndByte(),
				}
				symbolName = c.Node.Content(*sourceCode)
			} else if captureName == "definition" {
				definitionNode = c.Node
			}
		}

		// Second pass: process the definition now that we have the name
		if definitionNode != nil {
			sourceBlock := SourceBlock{
				Source: sourceCode,
				Range: sitter.Range{
					StartPoint: definitionNode.StartPoint(),
					EndPoint:   definitionNode.EndPoint(),
					StartByte:  definitionNode.StartByte(),
					EndByte:    definitionNode.EndByte(),
				},
				NameRange: &nameRange,
			}
			if definitionNode.Type() == "comment" {
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
