package coding

import (
	"context"
	"fmt"
	"net/url"
	"sidekick/coding/lsp"
	"sidekick/coding/tree_sitter"
	"sidekick/env"
	"sidekick/utils"
	"slices"
	"sync"

	tree_sitter_lib "github.com/tree-sitter/go-tree-sitter"
	"golang.org/x/sync/errgroup"
)

type RelatedSymbolsActivityInput struct {
	SymbolText       string
	EnvContainer     env.EnvContainer
	RelativeFilePath string
	SymbolRange      *lsp.Range

	// PreloadedFileContent, when non-nil, is used as RelativeFilePath's
	// content instead of reading it from the environment. It is intentionally
	// not serialized: activity invocations always read from the env.
	PreloadedFileContent []byte `json:"-"`

	// ReadFile, when non-nil, replaces EnvContainer.Env.ReadFile for loading
	// files that reference the symbol, so callers can coalesce duplicate reads
	// across lookups. It is intentionally not serialized: activity invocations
	// always read from the env.
	ReadFile func(ctx context.Context, path string) ([]byte, error) `json:"-"`
}

type RelatedSymbol struct {
	Symbol           tree_sitter.Symbol
	Locations        []lsp.Location
	RelativeFilePath string
	InSignature      bool
	Signature        tree_sitter.Signature
}

// Could be called EnrichedReferencesActivity or something similar too
func (ca *CodingActivities) RelatedSymbolsActivity(ctx context.Context, input RelatedSymbolsActivityInput) ([]RelatedSymbol, error) {
	lang := utils.InferLanguageNameFromFilePath(input.RelativeFilePath)
	if !lsp.IsSupportedLanguage(lang) {
		return nil, nil
	}

	lspInput := lsp.FindReferencesActivityInput{
		EnvContainer:         input.EnvContainer,
		RelativeFilePath:     input.RelativeFilePath,
		SymbolText:           input.SymbolText,
		Range:                input.SymbolRange,
		PreloadedFileContent: input.PreloadedFileContent,
	}
	references, err := ca.LSPActivities.FindReferencesActivity(ctx, lspInput)
	if err != nil {
		return nil, fmt.Errorf("failed to find references: %w", err)
	}

	var relatedSymbols []RelatedSymbol
	rootUri := input.EnvContainer.Env.GetWorkingDirectory()

	parsedFiles, err := loadReferencingFiles(ctx, input, references)
	if err != nil {
		return nil, err
	}

	for _, reference := range references {
		parsedUrl, err := url.Parse(reference.URI)
		if err != nil {
			return nil, fmt.Errorf("failed to parse reference URI %s: %w", reference.URI, err)
		}
		filePath := parsedUrl.Path

		symbols := parsedFiles[filePath].symbols
		for _, symbol := range symbols {
			symbolRange := tree_sitter_lib.Range{
				StartPoint: symbol.Declaration.StartPoint,
				EndPoint:   symbol.Declaration.EndPoint,
			}
			referenceRange := tree_sitter_lib.Range{
				StartPoint: tree_sitter_lib.Point{
					Row:    uint(reference.Range.Start.Line),
					Column: uint(reference.Range.Start.Character),
				},
				EndPoint: tree_sitter_lib.Point{
					Row:    uint(reference.Range.End.Line),
					Column: uint(reference.Range.End.Character),
				},
			}

			if RangesOverlap(symbolRange, referenceRange) {
				var signature tree_sitter.Signature
				var signatureRange tree_sitter_lib.Range
				for _, sig := range parsedFiles[filePath].signatures {
					sigRange := tree_sitter_lib.Range{
						StartPoint: sig.StartPoint,
						EndPoint:   sig.EndPoint,
					}
					if RangesOverlap(sigRange, symbolRange) {
						signature = sig
						signatureRange = sigRange
						break
					}
				}

				inSignature := RangesOverlap(signatureRange, referenceRange)
				relFilePath, err := env.EnvRel(input.EnvContainer.Env, rootUri, filePath)
				if err != nil {
					relFilePath = "" // File is outside working directory
				}

				index := slices.IndexFunc(relatedSymbols, func(rs RelatedSymbol) bool {
					return rs.Symbol.Content == symbol.Content && rs.Signature == signature && rs.RelativeFilePath == relFilePath
				})
				if index > -1 {
					relatedSymbols[index].Locations = append(relatedSymbols[index].Locations, reference)
					continue
				}
				relatedSymbols = append(relatedSymbols, RelatedSymbol{
					Symbol:           symbol,
					Locations:        []lsp.Location{reference},
					RelativeFilePath: relFilePath,
					InSignature:      inSignature,
					Signature:        signature,
				})
			}
		}
	}

	return relatedSymbols, nil
}

// parsedReferenceFile holds the tree-sitter view of one file that references
// the subject symbol.
type parsedReferenceFile struct {
	symbols    []tree_sitter.Symbol
	signatures []tree_sitter.Signature
}

// maxConcurrentReferenceFileLoads bounds parallel reads of files referencing a
// symbol: widely used symbols are referenced from dozens of files, and remote
// envs pay several network round trips per read, so loading serially is
// prohibitively slow.
const maxConcurrentReferenceFileLoads = 15

// loadReferencingFiles reads and parses each distinct file the references
// point into, concurrently up to maxConcurrentReferenceFileLoads, keyed by
// absolute file path. The first failure cancels the remaining loads.
func loadReferencingFiles(ctx context.Context, input RelatedSymbolsActivityInput, references []lsp.Location) (map[string]parsedReferenceFile, error) {
	readFile := input.ReadFile
	if readFile == nil {
		readFile = input.EnvContainer.Env.ReadFile
	}
	rootUri := input.EnvContainer.Env.GetWorkingDirectory()
	subjectAbsPath := env.EnvClean(input.EnvContainer.Env, rootUri+env.EnvSeparator(input.EnvContainer.Env)+input.RelativeFilePath)

	seen := make(map[string]bool, len(references))
	var uniquePaths []string
	for _, reference := range references {
		parsedUrl, err := url.Parse(reference.URI)
		if err != nil {
			return nil, fmt.Errorf("failed to parse reference URI %s: %w", reference.URI, err)
		}
		if filePath := parsedUrl.Path; !seen[filePath] {
			seen[filePath] = true
			uniquePaths = append(uniquePaths, filePath)
		}
	}

	parsedFiles := make(map[string]parsedReferenceFile, len(uniquePaths))
	var mu sync.Mutex
	g, groupCtx := errgroup.WithContext(ctx)
	g.SetLimit(maxConcurrentReferenceFileLoads)
	for _, filePath := range uniquePaths {
		g.Go(func() error {
			if err := groupCtx.Err(); err != nil {
				return err
			}
			fileBytes := input.PreloadedFileContent
			if fileBytes == nil || filePath != subjectAbsPath {
				var readErr error
				fileBytes, readErr = readFile(groupCtx, filePath)
				if readErr != nil {
					return fmt.Errorf("failed to read file %s: %w", filePath, readErr)
				}
			}
			langName := utils.InferLanguageNameFromFilePath(filePath)

			symbols, err := tree_sitter.GetFileSymbolsFromBytes(filePath, langName, fileBytes)
			if err != nil {
				return fmt.Errorf("failed to get file symbols for %s: %w", filePath, err)
			}

			signatures, err := tree_sitter.GetFileSignaturesFromBytes(langName, fileBytes)
			if err != nil {
				return fmt.Errorf("failed to get file signatures for %s: %w", filePath, err)
			}

			mu.Lock()
			parsedFiles[filePath] = parsedReferenceFile{symbols: symbols, signatures: signatures}
			mu.Unlock()
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	return parsedFiles, nil
}

func RangesOverlap(r1, r2 tree_sitter_lib.Range) bool {
	return (r1.StartPoint.Row < r2.EndPoint.Row || (r1.StartPoint.Row == r2.EndPoint.Row && r1.StartPoint.Column <= r2.EndPoint.Column)) &&
		(r2.StartPoint.Row < r1.EndPoint.Row || (r2.StartPoint.Row == r1.EndPoint.Row && r2.StartPoint.Column <= r1.EndPoint.Column))
}
