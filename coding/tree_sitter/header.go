package tree_sitter

import (
	"embed"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"

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

	if languageName == "markdown" {
		headers = extractMarkdownFrontmatterKeyBlocks(headers, &sourceCode)
		if len(headers) == 0 {
			return nil, ErrNoHeadersFound
		}
	}

	return headers, nil
}

func GetFileHeadersString(filePath string, numContextLines int) (string, error) {
	headers, err := GetFileHeaders(filePath, numContextLines)
	if err != nil {
		if errors.Is(err, ErrNoHeadersFound) {
			return "", nil
		}
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

	tsLang := tree_sitter.NewLanguage(tree_sitter_typescript.LanguageTypescript())
	return getHeadersInternal("typescript", tsLang, tsTree, sourceCode)
}

var markdownHeaderKeyRegex = regexp.MustCompile(`^(name|title|description|tags):`)

// extractMarkdownFrontmatterKeyBlocks takes the captured frontmatter block(s)
// and returns individual SourceBlocks for each supported top-level key.
func extractMarkdownFrontmatterKeyBlocks(headers []SourceBlock, sourceCode *[]byte) []SourceBlock {
	var result []SourceBlock

	for _, header := range headers {
		frontmatterContent := header.String()
		lines := strings.Split(frontmatterContent, "\n")

		var currentKeyStart int = -1
		var currentKeyLines []string

		flushCurrentKey := func() {
			if currentKeyStart >= 0 && len(currentKeyLines) > 0 {
				keyBlock := createKeySourceBlock(header, currentKeyStart, currentKeyLines, sourceCode)
				result = append(result, keyBlock)
			}
			currentKeyStart = -1
			currentKeyLines = nil
		}

		for i, line := range lines {
			// Skip top-level frontmatter delimiters (must not be indented)
			if line == "---" {
				flushCurrentKey()
				continue
			}

			// Check if this is a top-level key (not indented)
			if len(line) > 0 && line[0] != ' ' && line[0] != '\t' {
				// Flush previous key before starting a new one
				flushCurrentKey()
				// Check if it's one of our supported keys
				if markdownHeaderKeyRegex.MatchString(line) {
					currentKeyStart = i
					currentKeyLines = []string{line}
				}
			} else if currentKeyStart >= 0 {
				// Continuation of current key value (indented line or empty)
				currentKeyLines = append(currentKeyLines, line)
			}
		}
		flushCurrentKey()
	}

	return result
}

func createKeySourceBlock(frontmatter SourceBlock, lineOffset int, keyLines []string, sourceCode *[]byte) SourceBlock {
	// Calculate byte offset for the key block within the frontmatter
	frontmatterContent := frontmatter.String()
	lines := strings.Split(frontmatterContent, "\n")

	// Find byte offset to the start of the key
	byteOffset := uint(0)
	for i := 0; i < lineOffset; i++ {
		byteOffset += uint(len(lines[i])) + 1 // +1 for newline
	}

	// Preserve original YAML slice including all lines
	keyContent := strings.Join(keyLines, "\n")
	keyLength := uint(len(keyContent))

	startByte := frontmatter.Range.StartByte + byteOffset
	endByte := startByte + keyLength

	// Calculate line numbers
	startRow := frontmatter.Range.StartPoint.Row + uint(lineOffset)
	endRow := startRow + uint(len(keyLines)) - 1
	endCol := uint(len(keyLines[len(keyLines)-1]))

	return SourceBlock{
		Source: sourceCode,
		Range: tree_sitter.Range{
			StartPoint: tree_sitter.Point{Row: startRow, Column: 0},
			EndPoint:   tree_sitter.Point{Row: endRow, Column: endCol},
			StartByte:  startByte,
			EndByte:    endByte,
		},
	}
}

//go:embed header_queries/*
var headerQueriesFS embed.FS

func getHeaderQuery(languageName string) (string, error) {
	queryLang := normalizeLanguageForQueryFile(languageName)
	queryPath := fmt.Sprintf("header_queries/header_%s.scm", queryLang)
	queryBytes, err := headerQueriesFS.ReadFile(queryPath)
	if err != nil {
		return "", fmt.Errorf("error reading header query file: %w", err)
	}

	return string(queryBytes), nil
}
