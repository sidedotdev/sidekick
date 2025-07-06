package coding

import (
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

type DirectorySymDefRequest struct {
	EnvContainer          env.EnvContainer
	Requests              []FileSymDefRequest
	NumContextLines       *int
	IncludeRelatedSymbols bool
}

// Given a list of symbol definition requests for a directory, this method
// outputs symbol definitions formatted per file. Any symbols that were not
// found are included in the failures
func (ca *CodingActivities) BulkGetSymbolDefinitions(dirSymDefRequest DirectorySymDefRequest) (SymDefResults, error) {
	var relativeFilePathsBySymbolName *map[string][]string
	allSymbolDefs := strings.Builder{}
	allFailures := strings.Builder{}

	var extractions []*SymbolDefinitionExtraction
	var wg sync.WaitGroup
	for _, request := range dirSymDefRequest.Requests {
		sde := NewSymbolDefinitionExtraction(dirSymDefRequest, request, ca)
		extractions = append(extractions, sde)
		wg.Add(1)
		go func() {
			defer wg.Done()
			wroteWildcard := sde.WriteWildcard()
			if !wroteWildcard {
				symbolDefinitions, symbolErrors, allSymbolsFailed, relatedSymbols := sde.RetrieveSymbolDefinitions(dirSymDefRequest.EnvContainer)
				if !allSymbolsFailed {
					sde.WriteHeaders()
				}
				sde.WriteSymbolDefinitions(symbolDefinitions, symbolErrors, &relativeFilePathsBySymbolName, relatedSymbols)
			}
		}()
	}

	wg.Wait()
	for _, sde := range extractions {
		allSymbolDefs.WriteString(sde.SymbolDefinitionsString())
		allFailures.WriteString(sde.FailuresString())
	}

	return SymDefResults{
		SymbolDefinitions: allSymbolDefs.String(),
		Failures:          allFailures.String(),
	}, nil
}

const codeFenceEnd = "```\n\n"

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

type SymbolDefinitionExtraction struct {
	codingActivities      *CodingActivities
	symbolDefBuilder      strings.Builder
	failureBuilder        strings.Builder
	numContextLines       int
	blockHeader           string
	langName              string
	absolutePath          string
	directoryPath         string
	filePath              string
	symbols               []string
	includeRelatedSymbols bool
}

const defaultNumContextLines = 5

func NewSymbolDefinitionExtraction(dirSymDefRequest DirectorySymDefRequest, request FileSymDefRequest, codingActivities *CodingActivities) *SymbolDefinitionExtraction {
	workingDir := dirSymDefRequest.EnvContainer.Env.GetWorkingDirectory()
	blockHeader := fmt.Sprintf("File: %s\n", request.FilePath)
	absolutePath := filepath.Join(workingDir, request.FilePath)
	langName := utils.InferLanguageNameFromFilePath(absolutePath)
	numContextLines := defaultNumContextLines
	if dirSymDefRequest.NumContextLines != nil {
		numContextLines = *dirSymDefRequest.NumContextLines
	}
	sde := &SymbolDefinitionExtraction{
		codingActivities:      codingActivities,
		symbolDefBuilder:      strings.Builder{},
		failureBuilder:        strings.Builder{},
		blockHeader:           blockHeader,
		langName:              langName,
		numContextLines:       numContextLines,
		absolutePath:          absolutePath,
		directoryPath:         workingDir,
		filePath:              request.FilePath,
		symbols:               request.SymbolNames,
		includeRelatedSymbols: dirSymDefRequest.IncludeRelatedSymbols,
	}
	return sde
}

// success writes to both failure and symbol def
func (sde *SymbolDefinitionExtraction) WriteFailure(str string) {
	sde.symbolDefBuilder.WriteString(str)
	sde.failureBuilder.WriteString(str)
}

func (sde *SymbolDefinitionExtraction) WriteSymbolDef(str string) {
	sde.symbolDefBuilder.WriteString(str)
}

func (sde *SymbolDefinitionExtraction) SymbolDefinitionsString() string {
	return sde.symbolDefBuilder.String()
}

func (sde *SymbolDefinitionExtraction) FailuresString() string {
	return sde.failureBuilder.String()
}

func (sde *SymbolDefinitionExtraction) WriteHeaders() {
	headers, err := tree_sitter.GetFileHeaders(sde.absolutePath, sde.numContextLines)
	if err != nil {
		if !errors.Is(err, tree_sitter.ErrNoHeadersFound) {
			message := fmt.Sprintf("error getting file headers: %v\n\n", err)
			// write to both sd and failure: failure is for business logic, sd is what the LLM/user sees
			sde.WriteFailure(sde.blockHeader)
			sde.WriteFailure(message)
		}
	} else {
		for _, header := range headers {
			headerContent := header.String()
			sde.WriteSymbolDef(sde.blockHeader)
			sde.WriteSymbolDef(fmt.Sprintf("Lines: %d-%d\n", header.Range.StartPoint.Row+1, header.Range.EndPoint.Row+1))
			sde.WriteSymbolDef(CodeFenceStartForLanguage(sde.langName))
			sde.WriteSymbolDef(headerContent)
			if !strings.HasSuffix(headerContent, "\n") {
				sde.WriteSymbolDef("\n")
			}
			sde.WriteSymbolDef(codeFenceEnd)
		}
	}
}

// Handles a wildcard, which means the entire file is being retrieved instead of
// a specific symbol definition. After writing, returns whether a wildcard was
// encountered and written
func (sde *SymbolDefinitionExtraction) WriteWildcard() bool {
	// If no symbol names were provided or the special wildcard "*" or "" values
	// were provided, output the entire file

	if shouldRetrieveFullFile(sde.symbols, sde.absolutePath) {
		sde.WriteSymbolDef(sde.blockHeader)

		// output the file in its entirety in this case
		fileBytes, err := os.ReadFile(sde.absolutePath)
		if err != nil {
			var message string
			if os.IsNotExist(err) {
				message = getHintForNonExistentFile(sde.directoryPath, sde.absolutePath) + "\n"
			} else {
				relativeErr := errors.New(strings.ReplaceAll(err.Error(), sde.directoryPath, ""))
				message = fmt.Sprintf("error reading file %s: %v\n", sde.filePath, relativeErr)
			}
			sde.WriteFailure(sde.blockHeader)
			sde.WriteFailure(message)
			return true
		}

		fileContent := string(fileBytes)
		sde.WriteSymbolDef(fmt.Sprintf("Lines: %d-%d (full file)\n", 1, strings.Count(fileContent, "\n")+1))

		sde.WriteSymbolDef(CodeFenceStartForLanguage(sde.langName))
		sde.WriteSymbolDef(fileContent)
		if !strings.HasSuffix(fileContent, "\n") {
			sde.WriteSymbolDef("\n")
		}
		sde.WriteSymbolDef(codeFenceEnd)
		return true
	}

	return false
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

func (sde *SymbolDefinitionExtraction) RetrieveSymbolDefinitions(envContainer env.EnvContainer) ([][]tree_sitter.SourceBlock, []error, bool, map[string][]RelatedSymbol) {
	// Attempt to retrieve all symbol definitions before writing the file header block
	symbolDefinitions := make([][]tree_sitter.SourceBlock, len(sde.symbols))
	symbolErrors := make([]error, len(sde.symbols))
	allSymbolsFailed := true
	relatedSymbols := sync.Map{}

	var wg sync.WaitGroup
	for i, symbol := range sde.symbols {
		if symbol == "" || symbol == "*" {
			continue
		}
		i, symbol := i, symbol // avoid loop variable capture
		wg.Add(1)
		go func() {
			defer wg.Done()

			// TODO optimize: don't re-parse the file for each symbol
			symbolDefinitions[i], symbolErrors[i] = tree_sitter.GetSymbolDefinitions(sde.absolutePath, symbol, sde.numContextLines)
			if symbolErrors[i] != nil && strings.Contains(symbol, ".") {
				// If the retrieval failed and the symbol name contains a ".",
				// retry with only the part after the "."
				// TODO make this language-specific and try several different alternative forms
				lastDotIndex := strings.LastIndex(symbol, ".")
				if lastDotIndex != -1 {
					symbolDefinitions[i], symbolErrors[i] = tree_sitter.GetSymbolDefinitions(sde.absolutePath, symbol[lastDotIndex+1:], sde.numContextLines)
				}
			}
			if symbolErrors[i] == nil {
				allSymbolsFailed = false

				if sde.includeRelatedSymbols {
					symbolNameRange := sitterToLspRange(*symbolDefinitions[i][0].NameRange)
					related, err := sde.codingActivities.RelatedSymbolsActivity(context.Background(), RelatedSymbolsActivityInput{
						RelativeFilePath: sde.filePath,
						SymbolText:       symbol,
						EnvContainer:     envContainer,
						SymbolRange:      &symbolNameRange,
					})
					if err == nil {
						relatedSymbols.Store(symbol, related)
					} else {
						// hack to make the related symbol errors appear in the UI
						relatedSymbols.Store(symbol, []RelatedSymbol{
							{
								Symbol:    tree_sitter.Symbol{Content: fmt.Sprintf("error getting related symbols: %v", err)},
								Signature: tree_sitter.Signature{Content: fmt.Sprintf("error getting related symbols: %v", err)},
							},
						})
					}
				}
			}
		}()
	}
	wg.Wait()

	relatedSymbolsMap := make(map[string][]RelatedSymbol)
	relatedSymbols.Range(func(key, value interface{}) bool {
		relatedSymbolsMap[key.(string)] = value.([]RelatedSymbol)
		return true
	})

	return symbolDefinitions, symbolErrors, allSymbolsFailed, relatedSymbolsMap
}

func (sde *SymbolDefinitionExtraction) WriteSymbolDefinitions(
	symbolDefinitions [][]tree_sitter.SourceBlock,
	symbolErrors []error,
	relativeFilePathsBySymbolName **map[string][]string,
	relatedSymbols map[string][]RelatedSymbol,
) {
	// Write the symbol definition blocks
	for i, symbol := range sde.symbols {
		if symbolErrors[i] != nil {
			if *relativeFilePathsBySymbolName == nil {
				filePaths, err := getRelativeFilePathsBySymbolName(sde.directoryPath)
				if err != nil {
					sde.WriteFailure(fmt.Sprintf("error getting file paths by symbol name: %v\n", err))
				}
				*relativeFilePathsBySymbolName = &filePaths
			}

			message := getHintForSymbolDefResultFailure(symbolErrors[i], sde.directoryPath, sde.filePath, symbol, *relativeFilePathsBySymbolName)
			// write to both sd and failure: failure is for business logic, sd is what the LLM/user sees
			sde.WriteFailure(sde.blockHeader)
			sde.WriteFailure(message)
		} else {
			if len(symbolDefinitions[i]) > 1 {
				sde.WriteSymbolDef(fmt.Sprintf("NOTE: Multiple definitions were found for symbol %s:\n\n", symbol))

				// Merge adjacent or overlapping source blocks when they are duplicates beside each other
				// TODO /gen remove in favor of merging in the BulkGetSymbolDefinitions method
				sourceCodeLines := strings.Split(string(*symbolDefinitions[i][0].Source), "\n")
				symbolDefinitions[i] = tree_sitter.MergeAdjacentOrOverlappingSourceBlocks(symbolDefinitions[i], sourceCodeLines)
			}
			for _, symbolDefinition := range symbolDefinitions[i] {
				sde.WriteSymbolDef(sde.blockHeader)
				sde.WriteSymbolDef(fmt.Sprintf("Symbol: %s\n", symbol))
				sde.WriteSymbolDef(fmt.Sprintf("Lines: %d-%d\n", symbolDefinition.Range.StartPoint.Row+1, symbolDefinition.Range.EndPoint.Row+1))
				sde.WriteSymbolDef(CodeFenceStartForLanguage(sde.langName))
				defString := symbolDefinition.String()
				sde.WriteSymbolDef(defString)
				if !strings.HasSuffix(defString, "\n") {
					sde.WriteSymbolDef("\n")
				}
				sde.WriteSymbolDef(codeFenceEnd)

				// Write related symbols information
				if related, ok := relatedSymbols[symbol]; ok && len(related) > 0 {
					sde.writeRelatedSymbols(symbol, related)
				}
			}
		}
	}
}

// TODO: make this configurable, and/or more dynamic depending on the codebase's symbol graph structure
var (
	maxSameFileRelatedSymbols   = 25
	maxOtherFilesRelatedSymbols = 50
	maxOtherFiles               = 20
	maxSameFileSignatureLines   = 10
	maxOtherFileSignatureLines  = 10
)

func (sde *SymbolDefinitionExtraction) writeRelatedSymbols(symbol string, related []RelatedSymbol) {
	sameFileSymbols := make([]string, 0)
	otherFileSymbols := make(map[string][]string)
	numSameFileSignatureLines := 0
	totalOtherFileSignatureLines := 0
	numSameFileReferences := 0
	totalOtherFileReferences := 0
	totalOtherFileSymbols := 0

	for _, r := range related {
		if r.RelativeFilePath == sde.filePath {
			sameFileSymbols = append(sameFileSymbols, r.Symbol.Content)
			numSameFileReferences += len(r.Locations)
			numSameFileSignatureLines += strings.Count(r.Signature.Content, "\n") + 1
		} else {
			otherFileSymbols[r.RelativeFilePath] = append(otherFileSymbols[r.RelativeFilePath], r.Symbol.Content)
			totalOtherFileReferences += len(r.Locations)
			totalOtherFileSignatureLines += strings.Count(r.Signature.Content, "\n") + 1
			totalOtherFileSymbols += 1
		}
	}

	// Write same-file references
	if len(sameFileSymbols) > 0 {
		if numSameFileSignatureLines <= maxSameFileSignatureLines {
			sde.WriteSymbolDef(fmt.Sprintf("%s is referenced in the same file by:\n", symbol))
			for _, r := range related {
				if r.RelativeFilePath == sde.filePath {
					sde.WriteSymbolDef(fmt.Sprintf("\t%s\n", r.Signature.Content))
				}
			}
		} else if len(sameFileSymbols) <= maxSameFileRelatedSymbols {
			sde.WriteSymbolDef(fmt.Sprintf("%s is referenced in the same file by: %s\n", symbol, strings.Join(sameFileSymbols, ", ")))
		} else {
			sde.WriteSymbolDef(fmt.Sprintf("%s is referenced in the same file by %d other symbols %d times\n", symbol, len(sameFileSymbols), numSameFileReferences))
			sde.WriteSymbolDef(fmt.Sprintf("There are %d other symbols that reference %s in the same file.\n", len(sameFileSymbols), symbol))
		}
	}

	// Write other file references
	if len(otherFileSymbols) == 0 {
		return
	}
	if len(otherFileSymbols) > maxOtherFiles {
		sde.WriteSymbolDef(fmt.Sprintf("%s is referenced in %d other files. Total referencing symbols: %d. Total references: %d\n", symbol, len(otherFileSymbols), totalOtherFileSymbols, totalOtherFileReferences))
		return
	}

	sde.WriteSymbolDef(fmt.Sprintf("%s is referenced in other files:\n", symbol))
	for filePath, symbols := range otherFileSymbols {
		if totalOtherFileSignatureLines <= maxOtherFileSignatureLines {
			sde.WriteSymbolDef(fmt.Sprintf("\t%s:\n", filePath))
			for _, r := range related {
				if r.RelativeFilePath == filePath {
					signatureLines := strings.Split(r.Signature.Content, "\n")
					for _, line := range signatureLines {
						sde.WriteSymbolDef(fmt.Sprintf("\t\t%s\n", line))
					}
				}
			}
		} else if totalOtherFileSymbols <= maxOtherFilesRelatedSymbols {
			sde.WriteSymbolDef(fmt.Sprintf("\t%s: %s\n", filePath, strings.Join(symbols, ", ")))
		} else {
			sde.WriteSymbolDef(fmt.Sprintf("\t%s: %d symbols\n", filePath, len(symbols)))
		}
	}
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
