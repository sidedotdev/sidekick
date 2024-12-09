package coding

import (
	"context"
	"fmt"
	"path/filepath"
	"sidekick/coding/lsp"
	"sidekick/coding/tree_sitter"
	"sidekick/env"
	"slices"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
)

type RelatedSymbolsActivityInput struct {
	SymbolText       string
	EnvContainer     env.EnvContainer
	RelativeFilePath string
	SymbolRange      *lsp.Range
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
	lspInput := lsp.FindReferencesActivityInput{
		EnvContainer:     input.EnvContainer,
		RelativeFilePath: input.RelativeFilePath,
		SymbolText:       input.SymbolText,
		Range:            input.SymbolRange,
	}
	references, err := ca.LSPActivities.FindReferencesActivity(ctx, lspInput)
	if err != nil {
		return nil, fmt.Errorf("failed to find references: %w", err)
	}

	// Memoization map for file symbols and signatures
	memoMap := make(map[string]struct {
		symbols    []tree_sitter.Symbol
		signatures []tree_sitter.Signature
	})

	var relatedSymbols []RelatedSymbol
	rootUri := input.EnvContainer.Env.GetWorkingDirectory()

	for _, reference := range references {
		// Convert URI to absolute file path
		filePath := strings.Replace(reference.URI, "file://", "", 1)

		// Memoize file symbols and signatures
		if _, ok := memoMap[filePath]; !ok {
			symbols, err := tree_sitter.GetFileSymbols(filePath)
			if err != nil {
				return nil, fmt.Errorf("failed to get file symbols for %s: %w", filePath, err)
			}

			signatures, err := tree_sitter.GetFileSignatures(filePath)
			if err != nil {
				return nil, fmt.Errorf("failed to get file signatures for %s: %w", filePath, err)
			}

			memoMap[filePath] = struct {
				symbols    []tree_sitter.Symbol
				signatures []tree_sitter.Signature
			}{
				symbols:    symbols,
				signatures: signatures,
			}
		}
	}

	for _, reference := range references {
		filePath := strings.Replace(reference.URI, "file://", "", 1)
		symbols := memoMap[filePath].symbols
		for _, symbol := range symbols {
			symbolRange := sitter.Range{
				StartPoint: symbol.Declaration.StartPoint,
				EndPoint:   symbol.Declaration.EndPoint,
			}
			referenceRange := sitter.Range{
				StartPoint: sitter.Point{
					Row:    uint32(reference.Range.Start.Line),
					Column: uint32(reference.Range.Start.Character),
				},
				EndPoint: sitter.Point{
					Row:    uint32(reference.Range.End.Line),
					Column: uint32(reference.Range.End.Character),
				},
			}

			if RangesOverlap(symbolRange, referenceRange) {
				var signature tree_sitter.Signature
				var signatureRange sitter.Range
				for _, sig := range memoMap[filePath].signatures {
					sigRange := sitter.Range{
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

func RangesOverlap(r1, r2 sitter.Range) bool {
	return (r1.StartPoint.Row < r2.EndPoint.Row || (r1.StartPoint.Row == r2.EndPoint.Row && r1.StartPoint.Column <= r2.EndPoint.Column)) &&
		(r2.StartPoint.Row < r1.EndPoint.Row || (r2.StartPoint.Row == r1.EndPoint.Row && r2.StartPoint.Column <= r1.EndPoint.Column))
}
