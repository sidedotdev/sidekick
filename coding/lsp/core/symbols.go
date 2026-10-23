package core

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/url"
	"path"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"sidekick/coding/tree_sitter/parser"
	"sidekick/utils"
)

type SymbolDefinitionLocation struct {
	Symbol   string   `json:"symbol"`
	Location Location `json:"location"`
	Error    string   `json:"error"`
}

// DefinitionLocations resolves go-to-definition for each symbol in fileBytes,
// which is the content of filePath relative to repoDir. When referenceLine is
// set only occurrences on that line are resolved; when it cannot be located,
// every symbol gets an error listing the lines where it does occur. Results
// are de-duplicated per symbol.
func DefinitionLocations(ctx context.Context, client LSPClient, repoDir, filePath string, fileBytes []byte, symbols []string, referenceLine string) []SymbolDefinitionLocation {
	var lineRange *Range
	if referenceLine != "" {
		lineRange = findReferenceLineRange(fileBytes, referenceLine)
		if lineRange == nil {
			symbolDefinitions := make([]SymbolDefinitionLocation, 0, len(symbols))
			for _, symbol := range symbols {
				symbolDefinitions = append(symbolDefinitions, SymbolDefinitionLocation{
					Symbol: symbol,
					Error:  buildReferenceLineMismatchError(fileBytes, filePath, referenceLine, symbol),
				})
			}
			return symbolDefinitions
		}
	}

	langName := utils.InferLanguageNameFromFilePath(filePath)
	fileURI := ConvertFilePathToURI(repoDir, filePath)
	symbolDefinitions := make([]SymbolDefinitionLocation, 0, len(symbols))
	for _, symbol := range symbols {
		positions := FindSymbolOccurrencePositions(fileBytes, lineRange, symbol, langName)
		if len(positions) == 0 {
			symbolDefinitions = append(symbolDefinitions, SymbolDefinitionLocation{
				Symbol: symbol,
				Error:  "symbol not found in file content",
			})
			continue
		}

		seen := make(map[Location]struct{})
		var lastErr error
		foundAny := false
		for _, pos := range positions {
			locations, defErr := client.TextDocumentDefinition(ctx, fileURI, pos.Line, pos.Character)
			if defErr != nil {
				lastErr = defErr
				continue
			}
			for _, location := range locations {
				if _, ok := seen[location]; ok {
					continue
				}
				seen[location] = struct{}{}
				symbolDefinitions = append(symbolDefinitions, SymbolDefinitionLocation{
					Symbol:   symbol,
					Location: location,
				})
				foundAny = true
			}
		}
		if !foundAny && lastErr != nil {
			symbolDefinitions = append(symbolDefinitions, SymbolDefinitionLocation{
				Symbol: symbol,
				Error:  lastErr.Error(),
			})
		}
	}
	return symbolDefinitions
}

// References finds references to the first occurrence of symbolText in
// fileBytes, optionally restricted to fileRange.
func References(ctx context.Context, client LSPClient, baseDir, relativeFilePath string, fileBytes []byte, symbolText string, fileRange *Range) ([]Location, error) {
	position, err := findSymbolPosition(bufio.NewReader(bytes.NewReader(fileBytes)), fileRange, symbolText)
	if err != nil {
		return nil, fmt.Errorf("failed to find symbol position: %w", err)
	}
	absoluteFilepath := filepath.Join(baseDir, relativeFilePath)
	uri, err := url.Parse("file://" + absoluteFilepath)
	if err != nil {
		return nil, fmt.Errorf("failed to parse uri file://%s: %w", absoluteFilepath, err)
	}
	references, err := client.TextDocumentReferences(ctx, uri.String(), position.Line, position.Character)
	if err != nil {
		return nil, fmt.Errorf("failed to invoke lsp text document references: %w", err)
	}
	return references, nil
}

// FindSymbolOccurrencePositions returns the positions to use for
// go-to-definition requests for symbolText within data. Where the language has
// a grammar, only whole syntax tokens naming the symbol are returned: a
// language server resolves the entire token enclosing the requested position,
// so a substring match (eg "run" inside "runtime", or inside a comment or
// string literal) would otherwise yield definitions of an unrelated symbol.
// Languages without a grammar fall back to a plain text scan.
func FindSymbolOccurrencePositions(data []byte, fileRange *Range, symbolText, langName string) []Position {
	occurrences, err := parser.FindSymbolOccurrences(langName, data, symbolText)
	if err != nil {
		return findAllSymbolPositions(data, fileRange, symbolText)
	}

	var positions []Position
	for _, occurrence := range occurrences {
		line := int(occurrence.EndPoint.Row)
		if fileRange != nil && (line < fileRange.Start.Line || line > fileRange.End.Line) {
			continue
		}
		positions = append(positions, Position{
			Line:      line,
			Character: lastCharacterOffset(data, occurrence),
		})
	}
	return positions
}

// lastCharacterOffset returns the LSP character offset of the final character
// of an occurrence, ie its offset in UTF-16 code units from the line start.
func lastCharacterOffset(data []byte, occurrence parser.SymbolOccurrence) int {
	lineStartByte := occurrence.EndByte - uint(occurrence.EndPoint.Column)
	_, lastRuneSize := utf8.DecodeLastRune(data[occurrence.StartByte:occurrence.EndByte])
	lastCharByte := occurrence.EndByte - uint(lastRuneSize)
	if lastCharByte < lineStartByte {
		lastCharByte = lineStartByte
	}
	return utf16Len(data[lineStartByte:lastCharByte])
}

// utf16Len returns the number of UTF-16 code units encoding data, which is the
// character offset unit used by LSP positions.
func utf16Len(data []byte) int {
	count := 0
	for _, r := range string(data) {
		count++
		if r > 0xFFFF {
			count++
		}
	}
	return count
}

// findAllSymbolPositions returns the position of every occurrence of
// symbolText within data, pointing at the last character of each match (matching
// findSymbolPosition's convention). When fileRange is non-nil, only matches on
// lines within that range are returned. Multiple matches on the same line are
// each reported.
func findAllSymbolPositions(data []byte, fileRange *Range, symbolText string) []Position {
	if symbolText == "" {
		return nil
	}
	var positions []Position
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for i := 0; scanner.Scan(); i++ {
		if fileRange != nil && (i < fileRange.Start.Line || i > fileRange.End.Line) {
			continue
		}
		line := scanner.Text()
		searchFrom := 0
		for searchFrom <= len(line) {
			idx := strings.Index(line[searchFrom:], symbolText)
			if idx < 0 {
				break
			}
			absoluteIdx := searchFrom + idx
			positions = append(positions, Position{
				Line:      i,
				Character: absoluteIdx + len(symbolText) - 1,
			})
			searchFrom = absoluteIdx + len(symbolText)
		}
	}
	return positions
}

// buildReferenceLineMismatchError formats an error message explaining that
// the caller-provided reference_line could not be located in the file, and
// lists the actual lines (1-based numbers + literal content) where the symbol
// appears so the caller can retry with a correct reference line.
func buildReferenceLineMismatchError(data []byte, filePath, referenceLine, symbol string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "reference_line %q did not match any line in %s", referenceLine, filePath)
	occurrences := findSymbolOccurrenceLines(data, symbol)
	if len(occurrences) == 0 {
		fmt.Fprintf(&b, "; symbol %q does not appear in the file", symbol)
		return b.String()
	}
	fmt.Fprintf(&b, "; symbol %q occurs on the following lines:", symbol)
	for _, occ := range occurrences {
		fmt.Fprintf(&b, "\n  - line %d: %s", occ.lineNumber, occ.content)
	}
	return b.String()
}

type symbolOccurrenceLine struct {
	lineNumber int
	content    string
}

// findSymbolOccurrenceLines returns each line in data that contains symbolText,
// reported as a 1-based line number and the literal line content. A line that
// contains multiple occurrences is reported once.
func findSymbolOccurrenceLines(data []byte, symbolText string) []symbolOccurrenceLine {
	if symbolText == "" {
		return nil
	}
	var occurrences []symbolOccurrenceLine
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for i := 0; scanner.Scan(); i++ {
		line := scanner.Text()
		if strings.Contains(line, symbolText) {
			occurrences = append(occurrences, symbolOccurrenceLine{
				lineNumber: i + 1,
				content:    line,
			})
		}
	}
	return occurrences
}

// findReferenceLineRange returns a single-line Range covering the first line in
// data that contains the given snippet (after trimming surrounding whitespace),
// or nil if no such line is found. Callers use a nil return to signal that the
// caller-provided reference_line could not be located.
func findReferenceLineRange(data []byte, snippet string) *Range {
	trimmed := strings.TrimSpace(snippet)
	if trimmed == "" {
		return nil
	}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for i := 0; scanner.Scan(); i++ {
		line := scanner.Text()
		if strings.Contains(line, trimmed) {
			return &Range{
				Start: Position{Line: i, Character: 0},
				End:   Position{Line: i, Character: len(line)},
			}
		}
	}
	return nil
}

// ConvertFilePathToURI converts a file path to a URI.
func ConvertFilePathToURI(repoDir, filePath string) string {
	return "file://" + path.Join(repoDir, filePath)
}

// findSymbolPosition finds the position of the last character of the first matching text in the file content
func findSymbolPosition(reader io.Reader, fileRange *Range, symbolText string) (Position, error) {
	scanner := bufio.NewScanner(reader)
	lineNum := 0
	for scanner.Scan() {
		line := scanner.Text()
		if fileRange != nil && (lineNum < fileRange.Start.Line || lineNum > fileRange.End.Line) {
			lineNum++
			continue
		}
		index := strings.Index(line, symbolText)
		if index != -1 {
			return Position{
				Line:      lineNum,
				Character: index + len(symbolText) - 1,
			}, nil
		}
		lineNum++
	}
	return Position{}, fmt.Errorf("symbol not found in file content")
}
