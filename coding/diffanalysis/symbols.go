package diffanalysis

import (
	"errors"
	"fmt"
	"path/filepath"
	"sidekick/coding/tree_sitter"
)

var (
	// ErrBinaryFile is returned when attempting symbol extraction on a binary file.
	ErrBinaryFile = errors.New("binary file")
	// ErrUnsupportedLanguage is returned when the file's language is not supported for symbol extraction.
	ErrUnsupportedLanguage = errors.New("unsupported language")
)

// symbolInfo holds symbol name and its full definition range (1-indexed lines).
type symbolInfo struct {
	name      string
	startLine int // 1-indexed, inclusive
	endLine   int // 1-indexed, inclusive
}

// SymbolDelta represents the changes to symbols between old and new versions of a file.
type SymbolDelta struct {
	FilePath       string
	AddedSymbols   []string
	RemovedSymbols []string
	ChangedSymbols []string
}

// GetSymbolDelta computes the symbol changes for a file diff.
// It extracts symbols from both the old content (reconstructed via reverse patch)
// and the new content, then computes added, removed, and changed symbols.
//
// Parameters:
//   - fileDiff: The parsed diff for a single file
//   - newContent: The current/new content of the file
//
// Returns ErrBinaryFile for binary files, ErrUnsupportedLanguage for unsupported file types.
func GetSymbolDelta(fileDiff FileDiff, newContent string) (SymbolDelta, error) {
	result := SymbolDelta{
		FilePath: fileDiff.NewPath,
	}

	if fileDiff.IsBinary {
		return result, ErrBinaryFile
	}

	// Determine the file path for language inference
	filePath := fileDiff.NewPath
	if fileDiff.IsDeleted {
		filePath = fileDiff.OldPath
	}

	languageName := inferLanguageName(filePath)
	if languageName == "" {
		return result, ErrUnsupportedLanguage
	}

	// Get old content via reverse patch
	oldContent, err := ReversePatch(newContent, fileDiff)
	if err != nil {
		return result, fmt.Errorf("failed to reverse patch: %w", err)
	}

	// Extract symbols with full definition ranges from old content
	oldSymbols, err := extractSymbolDefinitions(oldContent, languageName)
	if err != nil {
		return result, fmt.Errorf("failed to extract old symbols: %w", err)
	}

	// Extract symbols with full definition ranges from new content
	newSymbols, err := extractSymbolDefinitions(newContent, languageName)
	if err != nil {
		return result, fmt.Errorf("failed to extract new symbols: %w", err)
	}

	// Build symbol name sets
	oldSymbolNames := make(map[string]bool)
	for _, sym := range oldSymbols {
		oldSymbolNames[sym.name] = true
	}

	newSymbolNames := make(map[string]bool)
	for _, sym := range newSymbols {
		newSymbolNames[sym.name] = true
	}

	// Compute added symbols (in new but not in old)
	for name := range newSymbolNames {
		if !oldSymbolNames[name] {
			result.AddedSymbols = append(result.AddedSymbols, name)
		}
	}

	// Compute removed symbols (in old but not in new)
	for name := range oldSymbolNames {
		if !newSymbolNames[name] {
			result.RemovedSymbols = append(result.RemovedSymbols, name)
		}
	}

	// Compute changed symbols by intersecting changed line ranges with symbol ranges
	changedRanges := fileDiff.GetChangedLineRanges()
	changedSymbolNames := make(map[string]bool)

	for _, sym := range newSymbols {
		// Skip symbols that were added (they're already in AddedSymbols)
		if !oldSymbolNames[sym.name] {
			continue
		}

		// Check if any changed line range overlaps with this symbol's range
		for _, r := range changedRanges {
			// Check for overlap: ranges overlap if start < other.end && end >= other.start
			// sym lines are 1-indexed inclusive, r.Start is 1-indexed inclusive, r.End is 1-indexed exclusive
			if r.Start <= sym.endLine && r.End > sym.startLine {
				changedSymbolNames[sym.name] = true
				break
			}
		}
	}

	for name := range changedSymbolNames {
		result.ChangedSymbols = append(result.ChangedSymbols, name)
	}

	return result, nil
}

// extractSymbolDefinitions extracts symbols with their full definition ranges from source code.
func extractSymbolDefinitions(content string, languageName string) ([]symbolInfo, error) {
	if content == "" {
		return nil, nil
	}

	defs, err := tree_sitter.GetAllSymbolDefinitionsFromSource(languageName, []byte(content))
	if err != nil {
		return nil, err
	}

	var symbols []symbolInfo
	for _, def := range defs {
		symbols = append(symbols, symbolInfo{
			name:      def.SymbolName,
			startLine: int(def.Range.StartPoint.Row) + 1, // Convert 0-indexed to 1-indexed
			endLine:   int(def.Range.EndPoint.Row) + 1,   // Convert 0-indexed to 1-indexed
		})
	}

	return symbols, nil
}

// inferLanguageName infers the tree-sitter language name from a file path.
func inferLanguageName(filePath string) string {
	ext := filepath.Ext(filePath)
	switch ext {
	case ".go":
		return "golang"
	case ".py":
		return "python"
	case ".ts":
		return "typescript"
	case ".tsx":
		return "tsx"
	case ".vue":
		return "vue"
	case ".java":
		return "java"
	case ".kt":
		return "kotlin"
	default:
		return ""
	}
}
