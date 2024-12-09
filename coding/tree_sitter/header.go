package tree_sitter

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"os"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/typescript/typescript"
)

func GetFileHeaders(filePath string, numContextLines int) ([]SourceBlock, error) {
	languageName, sitterLanguage, err := inferLanguageFromFilePath(filePath)
	if err != nil {
		return nil, err
	}
	sourceCode, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to obtain source code when getting headers for file %s: %v", filePath, err)
	}
	parser := sitter.NewParser()
	parser.SetLanguage(sitterLanguage)
	tree, err := parser.ParseCtx(context.Background(), nil, sourceTransform(languageName, &sourceCode))
	if err != nil {
		return nil, err
	}
	headers, err := getHeadersInternal(languageName, sitterLanguage, tree, &sourceCode)
	if err != nil && !errors.Is(err, ErrNoHeadersFound) {
		return nil, err
	}

	sourceCodeLines := strings.Split(string(sourceCode), "\n")
	headers = ExpandContextLines(headers, numContextLines, sourceCode)
	headers = MergeAdjacentOrOverlappingSourceBlocks(headers, sourceCodeLines)

	return headers, nil
}

func GetFileHeadersString(filePath string, numContextLines int) (string, error) {
	headers, err := GetFileHeaders(filePath, numContextLines)
	if err != nil {
		return "", err
	}
	return FormatHeaders(headers), nil
}

func FormatHeaders(headers []SourceBlock) string {
	out := strings.Builder{}
	for i, header := range headers {
		if i > 0 {
			out.WriteString("\n---\n")
		}
		out.WriteString(strings.Trim(header.String(), "\r\n"))
	}
	outString := strings.Trim(out.String(), "\r\n")
	if len(outString) > 0 {
		return outString + "\n"
	}
	return ""
}

var ErrNoHeadersFound = errors.New("no headers found")

func getHeadersInternal(languageName string, sitterLanguage *sitter.Language, tree *sitter.Tree, sourceCode *[]byte) (headers []SourceBlock, err error) {
	queryString, err := getHeaderQuery(languageName)
	if err != nil {
		return headers, fmt.Errorf("error getting header query: %w", err)
	}

	q, err := sitter.NewQuery([]byte(queryString), sitterLanguage)
	if err != nil {
		return headers, fmt.Errorf("error creating sitter header query: %w", err)
	}

	qc := sitter.NewQueryCursor()
	qc.Exec(q, tree.RootNode())
	for {
		m, ok := qc.NextMatch()
		if !ok {
			break
		}
		m = qc.FilterPredicates(m, *sourceCode)
		for _, c := range m.Captures {
			name := q.CaptureNameForId(c.Index)
			if name == "header" {
				header := SourceBlock{
					Source: sourceCode,
					Range: sitter.Range{
						StartPoint: c.Node.StartPoint(),
						EndPoint:   c.Node.EndPoint(),
						StartByte:  c.Node.StartByte(),
						EndByte:    c.Node.EndByte(),
					},
				}
				headers = append(headers, header)
			}
		}
	}

	if len(headers) == 0 {
		embeddedHeaders, err := getEmbeddedLanguageHeaders(languageName, tree, sourceCode)
		if err != nil {
			return nil, fmt.Errorf("error getting embedded language headers: %w", err)
		}
		if len(embeddedHeaders) == 0 {
			return nil, ErrNoHeadersFound
		}
		return embeddedHeaders, nil
	}

	return headers, nil
}

func getEmbeddedLanguageHeaders(languageName string, tree *sitter.Tree, sourceCode *[]byte) (headers []SourceBlock, err error) {
	switch languageName {
	case "vue":
		{
			return getVueEmbeddedLanguageHeaders(tree, sourceCode)
		}
	}
	return headers, nil
}

func getVueEmbeddedLanguageHeaders(vueTree *sitter.Tree, sourceCode *[]byte) (headers []SourceBlock, err error) {
	tsTree, err := GetVueEmbeddedTypescriptTree(vueTree, sourceCode)
	if err != nil {
		return headers, err
	}
	if tsTree == nil {
		return headers, nil
	}

	return getHeadersInternal("typescript", typescript.GetLanguage(), tsTree, sourceCode)
}

//go:embed header_queries/*
var headerQueriesFS embed.FS

func getHeaderQuery(languageName string) (string, error) {
	queryPath := fmt.Sprintf("header_queries/header_%s.scm", languageName)
	queryBytes, err := headerQueriesFS.ReadFile(queryPath)
	if err != nil {
		return "", fmt.Errorf("error reading header query file: %w", err)
	}

	return string(queryBytes), nil
}
