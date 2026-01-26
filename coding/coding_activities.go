package coding

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"sidekick/coding/lsp"
	"sidekick/coding/tree_sitter"
	"sidekick/common"
	"sidekick/env"
	"sidekick/utils"
	"slices"
	"strings"
	"sync"

	tree_sitter_lib "github.com/tree-sitter/go-tree-sitter"
)

type CodingActivities struct {
	LSPActivities        *lsp.LSPActivities
	TreeSitterActivities *tree_sitter.TreeSitterActivities
}

type FileSymDefRequest struct {
	FilePath    string   `json:"file_path" jsonschema:"description=The name of the file\\, including relative path\\, eg: \"foo/bar/something.go\""`
	SymbolNames []string `json:"symbol_names,omitempty" jsonschema:"description=Each string in this array is a case-sensitive name of a code symbol (eg name of a function\\, type\\, alias\\, interface\\, class\\, method\\, enum/member\\, constant\\, etc\\, depending on the language) defined in the given file\\, eg: \"someFunction\"\\, or \"SomeType\"\\, or \"SOME_CONSTANT\" etc. These are the symbol names for which the full symbol definition will be returned. Eg for a function name\\, this would be the entire function declaration including the function body. If no symbol names are provided\\, the entire file will be returned\\, but this usage is generally discouraged except for non-code files. Specifying the desired symbol names is strongly recommended\\, even when all symbols are desired."`
}

type SymDefResults struct {
	SymbolDefinitions string
	Failures          string
}

// SymbolRetrievalResult encapsulates the outcome for a single symbol or header retrieval.
type SymbolRetrievalResult struct {
	SourceBlocks   []tree_sitter.SourceBlock
	SymbolName     string
	RelativePath   string
	RelatedSymbols []RelatedSymbol
	Error          error
}

// MergedSymbolRetrievalResult represents multiple symbol retrieval results for a single file
// that have been merged based on overlapping source blocks.
type MergedSymbolRetrievalResult struct {
	// Errors maps symbol names to their retrieval errors
	Errors map[string]error
	// MergedSourceBlocks maps comma-delimited symbol names to their merged source blocks
	MergedSourceBlocks map[string][]tree_sitter.SourceBlock
	// RelatedSymbols maps comma-delimited symbol names to their related symbols
	RelatedSymbols map[string][]RelatedSymbol
	// RelativePath is the file path relative to the workspace root
	RelativePath string
}

// mergeSymbolResults combines multiple SymbolRetrievalResults for a single file into MergedSymbolRetrievalResults,
// where source blocks that overlap or are adjacent (separated only by whitespace) are merged together.
func mergeSymbolResults(results []SymbolRetrievalResult) MergedSymbolRetrievalResult {
	if len(results) == 0 {
		return MergedSymbolRetrievalResult{}
	}

	// All results should be for the same file
	relativePath := results[0].RelativePath

	// Collect all source blocks and map them back to their symbols
	var allSourceBlocks []tree_sitter.SourceBlock
	symbolsByRange := make(map[string][]string) // key is "startRow,endRow"
	errors := make(map[string]error)
	relatedSymbols := make(map[string][]RelatedSymbol)

	// Extract source code from first non-empty source block
	var sourceCode *[]byte
	for _, result := range results {
		if len(result.SourceBlocks) > 0 && result.SourceBlocks[0].Source != nil {
			sourceCode = result.SourceBlocks[0].Source
			break
		}
	}
	if sourceCode == nil {
		return MergedSymbolRetrievalResult{}
	}

	// Split source code into lines for merging
	sourceCodeLines := strings.Split(string(*sourceCode), "\n")

	// Collect all blocks and track symbol mappings
	for _, result := range results {
		if result.Error != nil {
			errors[result.SymbolName] = result.Error
			continue
		}
		if len(result.SourceBlocks) > 0 {
			for _, block := range result.SourceBlocks {
				allSourceBlocks = append(allSourceBlocks, block)
				// used for the "Symbol:" or "Symbols:" line, and related symbols, so header is not relevant
				if result.SymbolName != "" {
					key := fmt.Sprintf("%d,%d", block.Range.StartPoint.Row, block.Range.EndPoint.Row)
					symbolsByRange[key] = append(symbolsByRange[key], result.SymbolName)
				}
			}
		}
		if len(result.RelatedSymbols) > 0 {
			relatedSymbols[result.SymbolName] = result.RelatedSymbols
		}
	}

	// Sort blocks by start position
	slices.SortFunc(allSourceBlocks, func(a, b tree_sitter.SourceBlock) int {
		return cmp.Compare(a.Range.StartPoint.Row, b.Range.StartPoint.Row)
	})

	// Merge overlapping or adjacent blocks
	mergedBlocks := tree_sitter.MergeAdjacentOrOverlappingSourceBlocks(allSourceBlocks, sourceCodeLines)

	// Create merged results
	mergedResult := MergedSymbolRetrievalResult{
		Errors:             errors,
		MergedSourceBlocks: make(map[string][]tree_sitter.SourceBlock),
		RelatedSymbols:     make(map[string][]RelatedSymbol),
		RelativePath:       relativePath,
	}

	// For each merged block, determine which symbols it contains
	for _, mergedBlock := range mergedBlocks {
		var symbolsForBlock []string
		mergedStart := mergedBlock.Range.StartPoint.Row
		mergedEnd := mergedBlock.Range.EndPoint.Row

		// Check which original ranges are contained within this merged range
		for rangeKey, symbols := range symbolsByRange {
			var start, end uint
			fmt.Sscanf(rangeKey, "%d,%d", &start, &end)

			// If range is contained within merged
			if mergedEnd >= end && mergedStart <= start {
				symbolsForBlock = append(symbolsForBlock, symbols...)
			}
		}

		// Sort and deduplicate symbols
		slices.Sort(symbolsForBlock)
		symbolsForBlock = slices.Compact(symbolsForBlock)

		// Create key from sorted symbols
		symbolKey := strings.Join(symbolsForBlock, ", ")

		// Store merged block
		mergedResult.MergedSourceBlocks[symbolKey] = append(mergedResult.MergedSourceBlocks[symbolKey], mergedBlock)

		// Combine related symbols for all symbols in this block
		var combinedRelated []RelatedSymbol
		for _, symbol := range symbolsForBlock {
			combinedRelated = append(combinedRelated, relatedSymbols[symbol]...)
		}
		if len(combinedRelated) > 0 {
			mergedResult.RelatedSymbols[symbolKey] = combinedRelated
		}
	}

	return mergedResult
}

type DirectorySymDefRequest struct {
	EnvContainer          env.EnvContainer
	Requests              []FileSymDefRequest
	NumContextLines       *int
	IncludeRelatedSymbols bool
}

const DefaultNumContextLines = 5
const codeFenceEnd = "```\n\n"
const maxBulkSymDefOutputBytes = 1024 * 1024 // 1MB limit for bulk symbol definition output

type fileOutput struct {
	filePath string
	content  string
	failures string
	size     int
}

// Given a list of symbol definition requests for a directory, this method
// outputs symbol definitions formatted per file. Any symbols that were not
// found are included in the failures
func (ca *CodingActivities) BulkGetSymbolDefinitions(ctx context.Context, dirSymDefRequest DirectorySymDefRequest) (SymDefResults, error) {
	var wg sync.WaitGroup
	var mu sync.Mutex
	var results []SymbolRetrievalResult

	baseDir := dirSymDefRequest.EnvContainer.Env.GetWorkingDirectory()
	numContextLines := DefaultNumContextLines
	if dirSymDefRequest.NumContextLines != nil {
		numContextLines = *dirSymDefRequest.NumContextLines
	}

	for _, req := range dirSymDefRequest.Requests {
		absolutePath := filepath.Join(baseDir, req.FilePath)
		if shouldRetrieveFullFile(req.SymbolNames, absolutePath) {
			result := getWildcardRetrievalResult(req.SymbolNames, absolutePath, req.FilePath, dirSymDefRequest.EnvContainer.Env.GetWorkingDirectory())
			mu.Lock()
			results = append(results, result)
			mu.Unlock()
			continue
		}

		if len(req.SymbolNames) == 0 {
			continue
		}

		wg.Add(1)
		request := req
		go func(req FileSymDefRequest) {
			defer wg.Done()
			symbolResults := ca.retrieveSymbolDefinitions(dirSymDefRequest.EnvContainer, req, numContextLines, dirSymDefRequest.IncludeRelatedSymbols)

			if symbolResults[0].Error == nil {
				// include headers only when no failure
				result := getHeaderRetrievalResult(absolutePath, req.FilePath, numContextLines)
				mu.Lock()
				results = append(results, result)
				mu.Unlock()
			}

			mu.Lock()
			results = append(results, symbolResults...)
			mu.Unlock()
		}(request)
	}

	wg.Wait()

	// Group results by filepath
	resultsByFile := make(map[string][]SymbolRetrievalResult)
	for _, result := range results {
		resultsByFile[result.RelativePath] = append(resultsByFile[result.RelativePath], result)
	}

	var relativeFilePathsBySymbolName map[string][]string
	fileOutputs := make([]fileOutput, 0, len(resultsByFile))

	// Process results file by file
	for filePath, fileResults := range resultsByFile {
		var fileContentBuilder, fileFailureBuilder strings.Builder

		// Handle errors first
		for _, result := range fileResults {
			if result.Error != nil {
				if relativeFilePathsBySymbolName == nil {
					filePaths, err := getRelativeFilePathsBySymbolName(baseDir)
					if err != nil {
						msg := fmt.Sprintf("error getting file paths by symbol name: %v\n", err)
						fileContentBuilder.WriteString(msg)
						fileFailureBuilder.WriteString(msg)
					}
					relativeFilePathsBySymbolName = filePaths
				}

				hint := getHintForSymbolDefResultFailure(result.Error, baseDir, result.RelativePath, result.SymbolName, &relativeFilePathsBySymbolName)
				fileContentBuilder.WriteString(hint)
				fileContentBuilder.WriteString("\n")
				fileFailureBuilder.WriteString(hint)
				fileFailureBuilder.WriteString("\n")
			}
		}

		// Merge results for this file
		merged := mergeSymbolResults(fileResults)
		langName := utils.InferLanguageNameFromFilePath(filePath)

		// Skip if no source blocks
		if len(merged.MergedSourceBlocks) == 0 {
			if fileContentBuilder.Len() > 0 {
				fileOutputs = append(fileOutputs, fileOutput{
					filePath: filePath,
					content:  fileContentBuilder.String(),
					failures: fileFailureBuilder.String(),
					size:     fileContentBuilder.Len(),
				})
			}
			continue
		}

		sortedSymbolNames := getSortedSymbolNames(merged.MergedSourceBlocks)

		// Process each set of merged blocks
		for _, symbolNames := range sortedSymbolNames {
			blocks := merged.MergedSourceBlocks[symbolNames]
			symbols := strings.Split(symbolNames, ", ")
			onlyHeaders := utils.All(symbols, func(s string) bool { return s == "" })
			anyWildcard := slices.Contains(symbols, "*")

			// Sort blocks by start position
			slices.SortFunc(blocks, func(a, b tree_sitter.SourceBlock) int {
				return cmp.Compare(a.Range.StartPoint.Row, b.Range.StartPoint.Row)
			})

			for _, block := range blocks {
				// Write block header
				fileContentBuilder.WriteString("File: ")
				fileContentBuilder.WriteString(filePath)
				if len(symbols) > 0 && !onlyHeaders && !anyWildcard {
					if len(symbols) == 1 {
						fileContentBuilder.WriteString("\nSymbol: ")
					} else {
						fileContentBuilder.WriteString("\nSymbols: ")
					}
					fileContentBuilder.WriteString(symbolNames)
				}

				// Write line numbers
				fileContentBuilder.WriteString("\nLines: ")
				fileContentBuilder.WriteString(fmt.Sprintf("%d-%d",
					block.Range.StartPoint.Row+1,
					block.Range.EndPoint.Row+1))

				if anyWildcard {
					fileContentBuilder.WriteString(" (full file)")
				}
				fileContentBuilder.WriteString("\n")

				// Write source block content
				fileContentBuilder.WriteString(CodeFenceStartForLanguage(langName))
				content := block.String()
				fileContentBuilder.WriteString(content)
				if !strings.HasSuffix(content, "\n") {
					fileContentBuilder.WriteString("\n")
				}
				fileContentBuilder.WriteString(codeFenceEnd)
			}

			// Write related symbols if any
			if relatedSyms, ok := merged.RelatedSymbols[symbolNames]; ok && len(relatedSyms) > 0 {
				fileContentBuilder.WriteString(getRelatedSymbolsHint(merged, symbolNames))
			}

			// Warn about dups
			for _, symbol := range symbols {
				if symbol == "" || symbol == "*" {
					continue
				}
				for _, result := range fileResults {
					if result.SymbolName == symbol && len(result.SourceBlocks) > 1 {
						fileContentBuilder.WriteString(fmt.Sprintf("NOTE: Multiple definitions were found for symbol %s\n", symbol))
					}
				}
			}
		}

		fileOutputs = append(fileOutputs, fileOutput{
			filePath: filePath,
			content:  fileContentBuilder.String(),
			failures: fileFailureBuilder.String(),
			size:     fileContentBuilder.Len(),
		})
	}

	return applyTruncationToFileOutputs(fileOutputs)
}

func applyTruncationToFileOutputs(fileOutputs []fileOutput) (SymDefResults, error) {
	// Calculate total size of content (SymbolDefinitions output)
	totalSize := 0
	for _, fo := range fileOutputs {
		totalSize += fo.size
	}

	// If under limit, just concatenate everything
	if totalSize <= maxBulkSymDefOutputBytes {
		var symbolDefBuilder, failureBuilder strings.Builder
		for _, fo := range fileOutputs {
			symbolDefBuilder.WriteString(fo.content)
			failureBuilder.WriteString(fo.failures)
		}
		return SymDefResults{
			SymbolDefinitions: symbolDefBuilder.String(),
			Failures:          failureBuilder.String(),
		}, nil
	}

	// Sort by size descending - we'll truncate/exclude largest files first
	slices.SortFunc(fileOutputs, func(a, b fileOutput) int {
		return cmp.Compare(b.size, a.size) // descending
	})

	// Track the effective size of each file's output (content or exclusion message)
	type fileStatus struct {
		fo              fileOutput
		excluded        bool
		truncatedAmount int
		effectiveSize   int // size this file contributes to final output
	}
	statuses := make([]fileStatus, len(fileOutputs))
	for i, fo := range fileOutputs {
		statuses[i] = fileStatus{fo: fo, effectiveSize: fo.size}
	}

	// Iteratively reduce from largest files until under limit
	// Process files from largest to smallest
	for i := range statuses {
		currentTotal := 0
		for j := range statuses {
			currentTotal += statuses[j].effectiveSize
		}
		if currentTotal <= maxBulkSymDefOutputBytes {
			break
		}

		// Calculate how much we need to remove
		excess := currentTotal - maxBulkSymDefOutputBytes

		// Try to handle this file
		fileSize := statuses[i].fo.size
		exclusionMsgSize := len(fmt.Sprintf("%d bytes: exceeded 1MB limit for a single bulk request\n\n", fileSize))

		// Calculate savings from excluding this file entirely
		savingsFromExclusion := statuses[i].effectiveSize - exclusionMsgSize

		if savingsFromExclusion >= excess {
			// Partial truncation is sufficient
			// We need to remove 'excess' bytes, but add a truncation notice
			// Find the right truncation amount that results in exactly the right size
			// truncatedSize = originalSize - truncatedAmount + noticeSize <= limit
			// So: truncatedAmount >= originalSize + noticeSize - limit + otherFilesSize
			// But we want minimal truncation, so: truncatedAmount = excess + noticeSize
			truncationNoticeSize := len(fmt.Sprintf("NOTE: %d bytes were truncated from this file's output.\n", excess))
			truncatedAmount := excess + truncationNoticeSize

			// Recalculate with actual truncation amount (notice size may change)
			truncationNoticeSize = len(fmt.Sprintf("NOTE: %d bytes were truncated from this file's output.\n", truncatedAmount))
			truncatedAmount = excess + truncationNoticeSize

			// One more iteration to stabilize
			truncationNoticeSize = len(fmt.Sprintf("NOTE: %d bytes were truncated from this file's output.\n", truncatedAmount))
			truncatedAmount = excess + truncationNoticeSize

			if truncatedAmount < statuses[i].effectiveSize {
				statuses[i].truncatedAmount = truncatedAmount
				statuses[i].effectiveSize = statuses[i].fo.size - truncatedAmount + truncationNoticeSize
				continue
			}
		}

		// Exclude this file entirely
		if savingsFromExclusion > 0 {
			statuses[i].excluded = true
			statuses[i].truncatedAmount = 0
			statuses[i].effectiveSize = exclusionMsgSize
		} else {
			// Exclusion message is larger than content - just keep the content
			// This shouldn't happen for reasonably sized files, but handle it
			statuses[i].excluded = false
			statuses[i].truncatedAmount = 0
			statuses[i].effectiveSize = statuses[i].fo.size
		}
	}

	// Build final output, sorted by file path for consistent ordering
	slices.SortFunc(statuses, func(a, b fileStatus) int {
		return cmp.Compare(a.fo.filePath, b.fo.filePath)
	})

	var symbolDefBuilder, failureBuilder strings.Builder

	for _, status := range statuses {
		// Always include failures
		failureBuilder.WriteString(status.fo.failures)

		if status.excluded {
			// Output only the size and the required message
			symbolDefBuilder.WriteString(fmt.Sprintf("%d bytes: exceeded 1MB limit for a single bulk request\n\n", status.fo.size))
		} else if status.truncatedAmount > 0 {
			// Prepend truncation notice at the start of this file's output
			symbolDefBuilder.WriteString(fmt.Sprintf("NOTE: %d bytes were truncated from this file's output.\n", status.truncatedAmount))
			// Truncate from the end of the content
			keepBytes := status.fo.size - status.truncatedAmount
			if keepBytes > 0 {
				symbolDefBuilder.WriteString(status.fo.content[:keepBytes])
			}
		} else {
			symbolDefBuilder.WriteString(status.fo.content)
		}
	}

	return SymDefResults{
		SymbolDefinitions: symbolDefBuilder.String(),
		Failures:          failureBuilder.String(),
	}, nil
}

// Sort symbol names by lowest block start row
func getSortedSymbolNames(mergedSourceBlocks map[string][]tree_sitter.SourceBlock) []string {
	sortedSymbolNames := make([]string, 0, len(mergedSourceBlocks))
	for symbolName := range mergedSourceBlocks {
		sortedSymbolNames = append(sortedSymbolNames, symbolName)
	}
	startRow := func(block tree_sitter.SourceBlock) int {
		return int(block.Range.StartPoint.Row)
	}
	minInt := func(ints ...int) int {
		min := math.MaxInt32
		for _, r := range ints {
			if r < min {
				min = r
			}
		}
		return min
	}
	slices.SortFunc(sortedSymbolNames, func(a, b string) int {
		aBlocks := mergedSourceBlocks[a]
		bBlocks := mergedSourceBlocks[b]
		aStartRows := utils.Map(aBlocks, startRow)
		bStartRows := utils.Map(bBlocks, startRow)
		return cmp.Compare(minInt(aStartRows...), minInt(bStartRows...))
	})
	return sortedSymbolNames
}

func CodeFenceStartForLanguage(langName string) string {
	switch langName {
	case "golang":
		return "```go\n"
	case "unknown":
		return "```\n"
	default:
		return fmt.Sprintf("```%s\n", langName)
	}
}

func getRelativeFilePathsBySymbolName(directoryPath string) (map[string][]string, error) {
	symbolToPaths := make(map[string][]string, 0)
	num := 0
	err := common.WalkCodeDirectory(directoryPath, func(path string, entry fs.DirEntry) error {
		num++
		if entry.IsDir() {
			return nil
		}
		symbols, err := tree_sitter.GetAllAlternativeFileSymbols(path)

		if err != nil {
			if !errors.Is(err, tree_sitter.ErrFailedInferLanguage) {
				return fmt.Errorf("error getting symbols for file %s: %w", path, err)
			}
			// If it's a language inference error, continue processing other files
			return nil
		}
		relativePath := strings.TrimPrefix(path, filepath.Clean(directoryPath)+string(filepath.Separator))
		for _, symbol := range symbols {
			symbolToPaths[symbol.Content] = append(symbolToPaths[symbol.Content], relativePath)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return symbolToPaths, nil
}

func getHintForSymbolDefResultFailure(err error, directory, relativePath, symbolName string, filePathsBySymbolName *map[string][]string) string {
	hints := []string{}
	absolutePath := filepath.Join(directory, relativePath)
	directory = filepath.Clean(directory) + string(filepath.Separator)

	// symbol not found is not an error we need to relay as we have later hints for this situation
	// same thing for no such file or directory. but we need the error in other
	// cases that don't yet have customized hints
	if !strings.Contains(err.Error(), " not found") && !strings.Contains(err.Error(), "no such file or directory") {
		hints = append(hints, strings.ReplaceAll(err.Error(), directory, ""))
	}

	// if os.IsNotExist(err) {
	if !utils.FileExists(absolutePath) {
		hint := getHintForNonExistentFile(directory, absolutePath)
		hints = append(hints, hint)
	} else {
		rawFileSymbols, err := tree_sitter.GetFileSymbols(absolutePath)
		if err == nil {
			if len(rawFileSymbols) == 0 {
				hints = append(hints, fmt.Sprintf("The file at '%s' exists, but does not contain any symbols. Try requesting the special symbol name '*' to see the entire file.", relativePath))
			} else {
				fileSymbols := utils.Map(rawFileSymbols, func(symbol tree_sitter.Symbol) string {
					return symbol.Content
				})
				hints = append(hints, fmt.Sprintf("The file at '%s' does not contain the symbol '%s'. However, it does contain the following symbols: %s", relativePath, symbolName, strings.Join(fileSymbols, ", ")))
			}
		}
	}

	matchingFilePaths, ok := (*filePathsBySymbolName)[symbolName]
	if ok && len(matchingFilePaths) > 0 {
		bullets := utils.Map(matchingFilePaths, func(s string) string {
			return "  - " + s
		})
		hints = append(hints, fmt.Sprintf("The symbol '%s' is defined in the following files:\n%v\n", symbolName, strings.Join(bullets, "\n")))
		//hints = append(hints, fmt.Sprintf("The following file or files may contain %s:\n%s", symbolName, strings.Join(bullets, "\n"))
	} else {
		hints = append(hints, fmt.Sprintf("The symbol '%s' is not defined in any repo files.", symbolName))
	}

	return strings.Join(hints, "\n")
}

func getHeaderRetrievalResult(absolutePath, relativePath string, numContextLines int) SymbolRetrievalResult {
	headers, err := tree_sitter.GetFileHeaders(absolutePath, numContextLines)
	if err != nil && !errors.Is(err, tree_sitter.ErrNoHeadersFound) {
		return SymbolRetrievalResult{
			RelativePath: relativePath,
			Error:        fmt.Errorf("error getting file headers: %v", err),
		}
	}
	return SymbolRetrievalResult{
		SourceBlocks: headers,
		RelativePath: relativePath,
	}
}

type candidate struct {
	content         string
	segmentDistance int
	segmentRatio    float64
}

const maxSegmentDistance = 4

// provides a hint that shows similar files based on path-segment-wise levenshtein distance ratio
func getHintForNonExistentFile(directoryPath, absolutePath string) string {
	relativePath := strings.TrimPrefix(absolutePath, filepath.Clean(directoryPath)+string(filepath.Separator))
	pathSegments := strings.Split(relativePath, string(filepath.Separator))
	candidates := []candidate{}
	err := common.WalkCodeDirectory(directoryPath, func(path string, entry fs.DirEntry) error {
		if entry.IsDir() {
			return nil
		}
		relativeEntryPath := strings.TrimPrefix(path, filepath.Clean(directoryPath)+string(filepath.Separator))
		entrySegments := strings.Split(relativeEntryPath, string(filepath.Separator))
		segmentDistance, ratio := utils.SliceLevenshtein(pathSegments, entrySegments)

		// filter out paths that are too different
		if segmentDistance <= maxSegmentDistance {
			candidates = append(candidates, candidate{
				content:         relativeEntryPath,
				segmentDistance: segmentDistance,
				segmentRatio:    ratio,
			})
		}

		return nil
	})

	// limit candidates to the top 3 sorted by ratio
	slices.SortFunc(candidates, func(a, b candidate) int {
		if a.segmentRatio > b.segmentRatio {
			return -1
		} else if a.segmentRatio < b.segmentRatio {
			return 1
		}

		// for equal ratios, sort by descending StringSimilarity
		return cmp.Compare(
			utils.StringSimilarity(b.content, relativePath),
			utils.StringSimilarity(a.content, relativePath),
		)
	})

	var filteredCandidates []candidate
	// increase distance threshold until we have candidates
	for threshold := 1; threshold <= maxSegmentDistance && len(filteredCandidates) == 0; threshold++ {
		filteredCandidates = utils.Filter(candidates, func(c candidate) bool {
			return c.segmentDistance <= threshold
		})
	}

	bestCandidates := filteredCandidates[:min(3, len(filteredCandidates))]
	bestPaths := utils.Map(bestCandidates, func(c candidate) string { return c.content })

	if len(bestPaths) > 0 {
		return fmt.Sprintf("No file at '%s' exists in the repository. Did you mean one of the following?:\n%s", relativePath, strings.Join(bestPaths, "\n"))
	}

	if err != nil || len(bestPaths) == 0 {
		return fmt.Sprintf("No file at '%s' exists in the repository. Please check the file path and try again.", relativePath)
	}

	panic("unimplemented")
}

func getWildcardRetrievalResult(symbols []string, absolutePath, relativePath, directoryPath string) SymbolRetrievalResult {
	if !shouldRetrieveFullFile(symbols, absolutePath) {
		return SymbolRetrievalResult{RelativePath: relativePath}
	}

	fileBytes, err := os.ReadFile(absolutePath)
	if err != nil {
		var errMsg string
		if os.IsNotExist(err) {
			errMsg = getHintForNonExistentFile(directoryPath, absolutePath)
		} else {
			relativeErr := errors.New(strings.ReplaceAll(err.Error(), directoryPath, ""))
			errMsg = fmt.Sprintf("error reading file %s: %v", relativePath, relativeErr)
		}
		return SymbolRetrievalResult{
			RelativePath: relativePath,
			Error:        errors.New(errMsg),
		}
	}

	// Create a range covering the entire file
	lineCount := bytes.Count(fileBytes, []byte{'\n'})
	if len(fileBytes) > 0 && !bytes.HasSuffix(fileBytes, []byte{'\n'}) {
		lineCount++ // Account for files not ending in newline
	}
	fullRange := tree_sitter_lib.Range{
		StartPoint: tree_sitter_lib.Point{Row: 0, Column: 0},
		StartByte:  0,
		EndPoint:   tree_sitter_lib.Point{Row: uint(lineCount) - 1, Column: 0},
		EndByte:    uint(len(fileBytes)),
	}

	return SymbolRetrievalResult{
		SymbolName: "*",
		SourceBlocks: []tree_sitter.SourceBlock{{
			Source: &fileBytes,
			Range:  fullRange,
		}},
		RelativePath: relativePath,
	}
}

func shouldRetrieveFullFile(symbols []string, absolutePath string) bool {
	langName := utils.InferLanguageNameFromFilePath(absolutePath)

	isWildcard := slices.Contains(symbols, "*") || slices.Contains(symbols, "") || len(symbols) == 0

	// special-casing SFCs: handle the case where the file is a '.vue' or
	// '.svelte' (etc) file and the symbol name matches the file name, given the
	// lack of an explicit export with a corresponding symbol name
	switch langName {
	case "vue", "svelte", "riot", "marko":
		var maybeComponentName string
		if strings.HasPrefix(filepath.Base(absolutePath), "index.") {
			// this handles a case like "components/MyComponent/index.vue"
			// TODO /gen add a test for this case in TestBulkGetSymbolDefinitions
			dirName := filepath.Base(filepath.Dir(absolutePath))
			maybeComponentName = dirName
		} else {
			cleanedFileName := strings.TrimSuffix(filepath.Base(absolutePath), filepath.Ext(absolutePath))
			maybeComponentName = cleanedFileName
		}
		maybeComponentName = strings.ReplaceAll(maybeComponentName, "-", "")
		maybeComponentName = strings.ReplaceAll(maybeComponentName, "_", "")
		maybeComponentName = strings.ToLower(maybeComponentName)

		if maybeComponentName != "" {
			isWildcard = isWildcard || slices.ContainsFunc(symbols, func(s string) bool {
				cleanedSymbol := strings.ReplaceAll(s, "_", "")
				cleanedSymbol = strings.ToLower(cleanedSymbol)
				return cleanedSymbol == maybeComponentName
			})
		}
	}
	return isWildcard
}

func (ca *CodingActivities) retrieveSymbolDefinitions(envContainer env.EnvContainer, symDefRequest FileSymDefRequest, numContextLines int, includeRelatedSymbols bool) []SymbolRetrievalResult {
	results := make([]SymbolRetrievalResult, len(symDefRequest.SymbolNames))
	var wg sync.WaitGroup

	baseDir := envContainer.Env.GetWorkingDirectory()
	absolutePath := filepath.Join(baseDir, symDefRequest.FilePath)
	for i, symbol := range symDefRequest.SymbolNames {
		if symbol == "" || symbol == "*" {
			continue
		}
		i, symbol := i, symbol // avoid loop variable capture
		wg.Add(1)
		go func() {
			defer wg.Done()
			result := &results[i]
			result.SymbolName = symbol
			result.RelativePath = symDefRequest.FilePath

			// TODO optimize: don't re-parse the file for each symbol
			sourceBlocks, err := tree_sitter.GetSymbolDefinitions(absolutePath, symbol, numContextLines)
			if err != nil {
				// Attempt to normalize snippet-like inputs to a canonical symbol name and retry.
				langName := utils.InferLanguageNameFromFilePath(absolutePath)
				if langName != "" {
					if normalized, nErr := tree_sitter.NormalizeSymbolFromSnippet(langName, symbol); nErr == nil && normalized != "" && normalized != symbol {
						sourceBlocks, err = tree_sitter.GetSymbolDefinitions(absolutePath, normalized, numContextLines)
					}
				}
				// If still failing and the original contains a ".", retry with only the part after the last dot.
				if err != nil && strings.Contains(symbol, ".") {
					// TODO make this language-specific and try several different alternative forms
					lastDotIndex := strings.LastIndex(symbol, ".")
					if lastDotIndex != -1 {
						sourceBlocks, err = tree_sitter.GetSymbolDefinitions(absolutePath, symbol[lastDotIndex+1:], numContextLines)
					}
				}
			}

			result.SourceBlocks = sourceBlocks
			result.Error = err

			if err == nil && includeRelatedSymbols && len(sourceBlocks) > 0 {
				symbolNameRange := sitterToLspRange(*sourceBlocks[0].NameRange)
				related, err := ca.RelatedSymbolsActivity(context.Background(), RelatedSymbolsActivityInput{
					RelativeFilePath: symDefRequest.FilePath,
					SymbolText:       symbol,
					EnvContainer:     envContainer,
					SymbolRange:      &symbolNameRange,
				})
				if err == nil {
					result.RelatedSymbols = related
				} else {
					result.RelatedSymbols = []RelatedSymbol{
						{
							Symbol:    tree_sitter.Symbol{Content: fmt.Sprintf("error getting related symbols: %v", err)},
							Signature: tree_sitter.Signature{Content: fmt.Sprintf("error getting related symbols: %v", err)},
						},
					}
				}
			}
		}()
	}
	wg.Wait()

	return results
}

// TODO: make this configurable, and/or more dynamic depending on the codebase's symbol graph structure
var (
	maxSameFileRelatedSymbols   = 25
	maxOtherFilesRelatedSymbols = 50
	maxOtherFiles               = 20
	maxSameFileSignatureLines   = 10
	maxOtherFileSignatureLines  = 10
)

func getRelatedSymbolsHint(mergedResult MergedSymbolRetrievalResult, symbolNames string) string {
	sameFileSymbols := make([]string, 0)
	otherFileSymbols := make(map[string][]string)
	numSameFileSignatureLines := 0
	totalOtherFileSignatureLines := 0
	numSameFileReferences := 0
	totalOtherFileReferences := 0
	totalOtherFileSymbols := 0
	hintBuilder := strings.Builder{}

	relatedSymbols := mergedResult.RelatedSymbols[symbolNames]
	for _, rs := range relatedSymbols {
		if rs.RelativeFilePath == mergedResult.RelativePath {
			sameFileSymbols = append(sameFileSymbols, rs.Symbol.Content)
			numSameFileReferences += len(rs.Locations)
			numSameFileSignatureLines += strings.Count(rs.Signature.Content, "\n") + 1
		} else {
			otherFileSymbols[rs.RelativeFilePath] = append(otherFileSymbols[rs.RelativeFilePath], rs.Symbol.Content)
			totalOtherFileReferences += len(rs.Locations)
			totalOtherFileSignatureLines += strings.Count(rs.Signature.Content, "\n") + 1
			totalOtherFileSymbols += 1
		}
	}

	// Write same-file references
	if len(sameFileSymbols) > 0 {
		if numSameFileSignatureLines <= maxSameFileSignatureLines {
			hintBuilder.WriteString(fmt.Sprintf("%s is referenced in the same file by:\n", symbolNames))
			for _, rs := range relatedSymbols {
				if rs.RelativeFilePath == mergedResult.RelativePath {
					hintBuilder.WriteString(fmt.Sprintf("\t%s\n", rs.Signature.Content))
				}
			}
		} else if len(sameFileSymbols) <= maxSameFileRelatedSymbols {
			hintBuilder.WriteString(fmt.Sprintf("%s is referenced in the same file by: %s\n", symbolNames, strings.Join(sameFileSymbols, ", ")))
		} else {
			hintBuilder.WriteString(fmt.Sprintf("%s is referenced in the same file by %d other symbols %d times\n", symbolNames, len(sameFileSymbols), numSameFileReferences))
			hintBuilder.WriteString(fmt.Sprintf("There are %d other symbols that reference %s in the same file.\n", len(sameFileSymbols), symbolNames))
		}
	}

	// Write other file references
	if len(otherFileSymbols) == 0 {
		return hintBuilder.String()
	}
	if len(otherFileSymbols) > maxOtherFiles {
		hintBuilder.WriteString(fmt.Sprintf("%s is referenced in %d other files. Total referencing symbols: %d. Total references: %d\n", symbolNames, len(otherFileSymbols), totalOtherFileSymbols, totalOtherFileReferences))
		return hintBuilder.String()
	}

	hintBuilder.WriteString(fmt.Sprintf("%s is referenced in other files:\n", symbolNames))
	for filePath, symbols := range otherFileSymbols {
		if totalOtherFileSignatureLines <= maxOtherFileSignatureLines {
			hintBuilder.WriteString(fmt.Sprintf("\t%s:\n", filePath))
			for _, rs := range relatedSymbols {
				if rs.RelativeFilePath == filePath {
					signatureLines := strings.Split(rs.Signature.Content, "\n")
					for _, line := range signatureLines {
						hintBuilder.WriteString(fmt.Sprintf("\t\t%s\n", line))
					}
				}
			}
		} else if totalOtherFileSymbols <= maxOtherFilesRelatedSymbols {
			hintBuilder.WriteString(fmt.Sprintf("\t%s: %s\n", filePath, strings.Join(symbols, ", ")))
		} else {
			hintBuilder.WriteString(fmt.Sprintf("\t%s: %d symbols\n", filePath, len(symbols)))
		}
	}
	return hintBuilder.String()
}

func sitterToLspRange(r tree_sitter_lib.Range) lsp.Range {
	return lsp.Range{
		Start: lsp.Position{
			Line:      int(r.StartPoint.Row),
			Character: int(r.StartPoint.Column),
		},
		End: lsp.Position{
			Line:      int(r.EndPoint.Row),
			Character: int(r.EndPoint.Column),
		},
	}
}
