package repotools

import (
	"fmt"
	"strings"

	"sidekick/coding/tree_sitter/parser"

	"github.com/rs/zerolog/log"
)

func BulkSearchRepository(runner SearchRunner, params BulkSearchRepositoryParams) (string, error) {
	results := []string{}
	for _, search := range params.Searches {
		result, err := SearchRepository(runner, SearchRepositoryInput{
			PathGlob: search.PathGlob, SearchTerm: search.SearchTerm, ContextLines: params.ContextLines,
		})
		if err != nil {
			return "", err
		}
		if strings.Contains(result, "No results found") && isExistentFilePath(runner, search.PathGlob) {
			message, err := getSymbolsMessage(runner, search.PathGlob)
			if err != nil {
				return "", err
			}
			if message != "" {
				result = fmt.Sprintf("No results found for search term '%s' in file '%s'.%s",
					search.SearchTerm, search.PathGlob, message)
			}
		}
		results = append(results, result)
	}
	return strings.Join(results, "\n"), nil
}

func isExistentFilePath(runner SearchRunner, path string) bool {
	if !strings.ContainsAny(path, "*?[]{}") && path != "" {
		output, err := runner.RunCommand("cat", path)
		if err != nil {
			log.Error().Err(err).Msgf("failed to cat file %s", path)
		}
		if output.ExitStatus == 0 {
			return true
		}
	}
	return false
}

func getSymbolsMessage(runner SearchRunner, path string) (string, error) {
	symbols, err := runner.FileSymbols(path)
	if err != nil && !strings.Contains(err.Error(), parser.ErrFailedInferLanguage.Error()) {
		return "", err
	}
	if len(symbols) == 0 {
		return "\nNote: The file exists and can be read in full using the get_symbol_definitions tool.", nil
	}
	names := make([]string, len(symbols))
	for i, symbol := range symbols {
		names[i] = symbol.Content
	}
	return fmt.Sprintf("\nNote: The file exists and the full file or specific symbols in it can be read using the get_symbol_definitions tool. It contains the following symbols: %s", strings.Join(names, ", ")), nil
}
