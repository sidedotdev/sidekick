package tree_sitter

import (
	"embed"
	"errors"
	"fmt"
	"os"
	"strings"
	"unsafe"

	tree_sitter "github.com/tree-sitter/go-tree-sitter"
	tree_sitter_typescript "github.com/tree-sitter/tree-sitter-typescript/bindings/go"
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
	parser := tree_sitter.NewParser()
	defer parser.Close()
	parser.SetLanguage(sitterLanguage)
	tree := parser.Parse(sourceTransform(languageName, &sourceCode), nil)
	if tree != nil {
		defer tree.Close()
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

func getHeadersInternal(languageName string, sitterLanguage *tree_sitter.Language, tree *tree_sitter.Tree, sourceCode *[]byte) (headers []SourceBlock, err error) {
	queryString, err := getHeaderQuery(languageName)
	if err != nil {
		return headers, fmt.Errorf("error getting header query: %w", err)
	}

	q, qErr := tree_sitter.NewQuery(sitterLanguage, queryString)
	if qErr != nil {
		return headers, fmt.Errorf("error creating sitter header query: %s", qErr.Message)
	}
	defer q.Close()

	qc := tree_sitter.NewQueryCursor()
	defer qc.Close()
	matches := qc.Matches(q, tree.RootNode(), *sourceCode)
	for match := matches.Next(); match != nil; match = matches.Next() {
		for _, c := range match.Captures {
			name := q.CaptureNames()[c.Index]
			if name == "header" {
				header := SourceBlock{
					Source: sourceCode,
					Range: tree_sitter.Range{
						StartPoint: c.Node.StartPosition(),
						EndPoint:   c.Node.EndPosition(),
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

func getEmbeddedLanguageHeaders(languageName string, tree *tree_sitter.Tree, sourceCode *[]byte) (headers []SourceBlock, err error) {
	switch languageName {
	case "vue":
		{
			return getVueEmbeddedLanguageHeaders(tree, sourceCode)
		}
	}
	return headers, nil
}

func getVueEmbeddedLanguageHeaders(vueTree *tree_sitter.Tree, sourceCode *[]byte) (headers []SourceBlock, err error) {
	tsTree, err := GetVueEmbeddedTypescriptTree(vueTree, sourceCode)
	if err != nil {
		return headers, err
	}
	if tsTree == nil {
		return headers, nil
	}

	tsLang := tree_sitter.NewLanguage(unsafe.Pointer(tree_sitter_typescript.LanguageTypescript()))
	return getHeadersInternal("typescript", tsLang, tsTree, sourceCode)
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
