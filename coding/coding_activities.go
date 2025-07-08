package coding

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io/fs"
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

	sitter "github.com/smacker/go-tree-sitter"
)

type CodingActivities struct {
	LSPActivities        *lsp.LSPActivities
	TreeSitterActivities *tree_sitter.TreeSitterActivities
}

type FileSymDefRequest struct {
	FilePath    string   `json:"file_path" jsonschema:"description=The name of the file\\, including relative path\\, eg: \"foo/bar/something.go\""`
	SymbolNames []string `json:"symbol_names,omitempty" jsonschema:"description=Each string in this array is a case-sensitive name of a code symbol (eg name of a function\\, type\\, alias\\, interface\\, class\\, method\\, enum/member\\, constant\\, etc\\, depending on the language) defined in the given file\\, eg: \"someFunction\"\\, or \"SomeType\"\\, or \"SOME_CONSTANT\" etc. These are the symbol names for which the full symbol definition will be returned. Eg for a function name\\, this would be the entire function declaration including the function body. If no symbol names are provided\\, the entire file will be returned."`
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

type DirectorySymDefRequest struct {
	EnvContainer          env.EnvContainer
	Requests              []FileSymDefRequest
	NumContextLines       *int
	IncludeRelatedSymbols bool
}

const DefaultNumContextLines = 5
const codeFenceEnd = "```\n\n"

// Given a list of symbol definition requests for a directory, this method
// outputs symbol definitions formatted per file. Any symbols that were not
// found are included in the failures
func (ca *CodingActivities) BulkGetSymbolDefinitions(dirSymDefRequest DirectorySymDefRequest) (SymDefResults, error) {
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

	var relativeFilePathsBySymbolName map[string][]string
	var symbolDefBuilder, failureBuilder strings.Builder
	for _, result := range results {
		if result.Error != nil {
			if relativeFilePathsBySymbolName == nil {
				filePaths, err := getRelativeFilePathsBySymbolName(baseDir)
				if err != nil {
					msg := fmt.Sprintf("error getting file paths by symbol name: %v\n", err)
					symbolDefBuilder.WriteString(msg)
					failureBuilder.WriteString(msg)
				}
				relativeFilePathsBySymbolName = filePaths
			}

			hint := getHintForSymbolDefResultFailure(result.Error, baseDir, result.RelativePath, result.SymbolName, &relativeFilePathsBySymbolName)

			symbolDefBuilder.WriteString(hint)
			symbolDefBuilder.WriteString("\n")
			failureBuilder.WriteString(hint)
			failureBuilder.WriteString("\n")
			continue
		}

		if len(result.SourceBlocks) == 0 {
			continue
		}

		if len(result.SourceBlocks) > 1 && result.SymbolName != "" {
			symbolDefBuilder.WriteString(fmt.Sprintf("NOTE: Multiple definitions were found for symbol %s:\n\n", result.SymbolName))
		}
		sourceCodeLines := strings.Split(string(*result.SourceBlocks[0].Source), "\n")
		mergedBlocks := tree_sitter.MergeAdjacentOrOverlappingSourceBlocks(result.SourceBlocks, sourceCodeLines)

		langName := utils.InferLanguageNameFromFilePath(result.RelativePath)
		for _, block := range mergedBlocks {
			// Write block header
			symbolDefBuilder.WriteString("File: ")
			symbolDefBuilder.WriteString(result.RelativePath)
			if result.SymbolName != "" && result.SymbolName != "*" {
				symbolDefBuilder.WriteString("\nSymbol: ")
				symbolDefBuilder.WriteString(result.SymbolName)
			}

			// Write line numbers
			symbolDefBuilder.WriteString("\nLines: ")
			symbolDefBuilder.WriteString(fmt.Sprintf("%d-%d",
				block.Range.StartPoint.Row+1,
				block.Range.EndPoint.Row+1))

			if result.SymbolName == "*" {
				symbolDefBuilder.WriteString(" (full file)")
			}
			symbolDefBuilder.WriteString("\n")

			// Write source block content
			symbolDefBuilder.WriteString(CodeFenceStartForLanguage(langName))
			content := block.String()
			symbolDefBuilder.WriteString(content)
			if !strings.HasSuffix(content, "\n") {
				symbolDefBuilder.WriteString("\n")
			}
			symbolDefBuilder.WriteString(codeFenceEnd)
		}

		// Write related symbols if any
		if len(result.RelatedSymbols) > 0 {
			symbolDefBuilder.WriteString(getRelatedSymbolsHint(result))
		}
	}

	return SymDefResults{
		SymbolDefinitions: symbolDefBuilder.String(),
		Failures:          failureBuilder.String(),
	}, nil
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
	fullRange := sitter.Range{
		StartPoint: sitter.Point{Row: 0, Column: 0},
		StartByte:  0,
		EndPoint:   sitter.Point{Row: uint32(lineCount) - 1, Column: 0},
		EndByte:    uint32(len(fileBytes)),
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
			if err != nil && strings.Contains(symbol, ".") {
				// If the retrieval failed and the symbol name contains a ".",
				// retry with only the part after the "."
				// TODO make this language-specific and try several different alternative forms
				lastDotIndex := strings.LastIndex(symbol, ".")
				if lastDotIndex != -1 {
					sourceBlocks, err = tree_sitter.GetSymbolDefinitions(absolutePath, symbol[lastDotIndex+1:], numContextLines)
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

func getRelatedSymbolsHint(result SymbolRetrievalResult) string {
	sameFileSymbols := make([]string, 0)
	otherFileSymbols := make(map[string][]string)
	numSameFileSignatureLines := 0
	totalOtherFileSignatureLines := 0
	numSameFileReferences := 0
	totalOtherFileReferences := 0
	totalOtherFileSymbols := 0
	hintBuilder := strings.Builder{}

	for _, rs := range result.RelatedSymbols {
		if rs.RelativeFilePath == result.RelativePath {
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
			hintBuilder.WriteString(fmt.Sprintf("%s is referenced in the same file by:\n", result.SymbolName))
			for _, rs := range result.RelatedSymbols {
				if rs.RelativeFilePath == result.RelativePath {
					hintBuilder.WriteString(fmt.Sprintf("\t%s\n", rs.Signature.Content))
				}
			}
		} else if len(sameFileSymbols) <= maxSameFileRelatedSymbols {
			hintBuilder.WriteString(fmt.Sprintf("%s is referenced in the same file by: %s\n", result.SymbolName, strings.Join(sameFileSymbols, ", ")))
		} else {
			hintBuilder.WriteString(fmt.Sprintf("%s is referenced in the same file by %d other symbols %d times\n", result.SymbolName, len(sameFileSymbols), numSameFileReferences))
			hintBuilder.WriteString(fmt.Sprintf("There are %d other symbols that reference %s in the same file.\n", len(sameFileSymbols), result.SymbolName))
		}
	}

	// Write other file references
	if len(otherFileSymbols) == 0 {
		return hintBuilder.String()
	}
	if len(otherFileSymbols) > maxOtherFiles {
		hintBuilder.WriteString(fmt.Sprintf("%s is referenced in %d other files. Total referencing symbols: %d. Total references: %d\n", result.SymbolName, len(otherFileSymbols), totalOtherFileSymbols, totalOtherFileReferences))
		return hintBuilder.String()
	}

	hintBuilder.WriteString(fmt.Sprintf("%s is referenced in other files:\n", result.SymbolName))
	for filePath, symbols := range otherFileSymbols {
		if totalOtherFileSignatureLines <= maxOtherFileSignatureLines {
			hintBuilder.WriteString(fmt.Sprintf("\t%s:\n", filePath))
			for _, rs := range result.RelatedSymbols {
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

func sitterToLspRange(r sitter.Range) lsp.Range {
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
