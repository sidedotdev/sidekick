package repotools

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"sidekick/coding/lsp/core"
	"sidekick/coding/tree_sitter/parser"
	"sidekick/utils"
	"slices"
	"sync"

	tree_sitter_lib "github.com/tree-sitter/go-tree-sitter"
	"golang.org/x/sync/errgroup"
)

type RelatedSymbolsRequest struct {
	SymbolText       string
	RelativeFilePath string
	SymbolRange      *core.Range

	// PreloadedFileContent, when non-nil, is used as RelativeFilePath's
	// content instead of reading it from the repository.
	PreloadedFileContent []byte `json:"-"`
}

type RelatedSymbol struct {
	Symbol           parser.Symbol
	Locations        []core.Location
	RelativeFilePath string
	InSignature      bool
	Signature        parser.Signature
}

// RelatedSymbols finds the symbols whose definitions reference the requested
// symbol, with the signature each reference appears in.
func (d *SymbolDefinitions) RelatedSymbols(ctx context.Context, repo Repository, input RelatedSymbolsRequest) ([]RelatedSymbol, error) {
	lang := utils.InferLanguageNameFromFilePath(input.RelativeFilePath)
	if !core.IsSupportedLanguage(lang) {
		return nil, nil
	}
	rootUri := repo.GetWorkingDirectory()

	references, err := d.findReferences(ctx, repo, rootUri, lang, input)
	if err != nil {
		return nil, fmt.Errorf("failed to find references: %w", err)
	}

	var relatedSymbols []RelatedSymbol

	parsedFiles, err := loadReferencingFiles(ctx, repo, input, references)
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
				var signature parser.Signature
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
				relFilePath, err := filepath.Rel(rootUri, filePath)
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
					Locations:        []core.Location{reference},
					RelativeFilePath: relFilePath,
					InSignature:      inSignature,
					Signature:        signature,
				})
			}
		}
	}

	return relatedSymbols, nil
}

func (d *SymbolDefinitions) findReferences(ctx context.Context, repo Repository, rootUri, lang string, input RelatedSymbolsRequest) ([]core.Location, error) {
	ctx, client, release, err := d.acquireLanguageServer(ctx, lang)
	if err != nil {
		return nil, err
	}
	defer release()
	fileBytes := input.PreloadedFileContent
	if fileBytes == nil {
		fileBytes, err = repo.ReadFile(ctx, input.RelativeFilePath)
		if err != nil {
			return nil, fmt.Errorf("failed to read file: %w", err)
		}
	}
	return core.References(ctx, client, rootUri, input.RelativeFilePath, fileBytes, input.SymbolText, input.SymbolRange)
}

// acquireLanguageServer returns ErrNoLanguageServer when LSP resolution is
// disabled.
func (d *SymbolDefinitions) acquireLanguageServer(ctx context.Context, lang string) (context.Context, core.LSPClient, func(), error) {
	if d.Sessions == nil || d.Sessions.LSPClientProvider == nil {
		return ctx, nil, nil, ErrNoLanguageServer
	}
	return d.Sessions.Acquire(ctx, d.Scope, lang)
}

var ErrNoLanguageServer = errors.New("no language server available")

// parsedReferenceFile holds the tree-sitter view of one file that references
// the subject symbol.
type parsedReferenceFile struct {
	symbols    []parser.Symbol
	signatures []parser.Signature
}

// maxConcurrentReferenceFileLoads bounds parallel reads of files referencing a
// symbol: widely used symbols are referenced from dozens of files, and remote
// envs pay several network round trips per read, so loading serially is
// prohibitively slow.
const maxConcurrentReferenceFileLoads = 15

// loadReferencingFiles reads and parses each distinct file the references
// point into, concurrently up to maxConcurrentReferenceFileLoads, keyed by
// absolute file path. The first failure cancels the remaining loads.
func loadReferencingFiles(ctx context.Context, repo Repository, input RelatedSymbolsRequest, references []core.Location) (map[string]parsedReferenceFile, error) {
	subjectAbsPath := filepath.Join(repo.GetWorkingDirectory(), input.RelativeFilePath)

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
				fileBytes, readErr = repo.ReadFile(groupCtx, filePath)
				if readErr != nil {
					return fmt.Errorf("failed to read file %s: %w", filePath, readErr)
				}
			}
			langName := utils.InferLanguageNameFromFilePath(filePath)

			symbols, err := parser.GetFileSymbolsFromBytes(filePath, langName, fileBytes)
			if err != nil {
				return fmt.Errorf("failed to get file symbols for %s: %w", filePath, err)
			}

			signatures, err := parser.GetFileSignaturesFromBytes(langName, fileBytes)
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
