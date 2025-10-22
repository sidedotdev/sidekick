package dev

import (
	"fmt"
	"path/filepath"
	"sidekick/coding/tree_sitter"
	"sidekick/env"
	"sidekick/llm"
	"sidekick/persisted_ai"
	"strings"

	"github.com/invopop/jsonschema"
	"github.com/rs/zerolog/log"
	"go.temporal.io/sdk/workflow"
)

var bulkSearchRepositoryTool = llm.Tool{
	Name:        "bulk_search_repository",
	Description: "Used to perform multiple searches within the repository, each for files matching a given glob pattern and containing a search term.",
	Parameters:  (&jsonschema.Reflector{DoNotReference: true}).Reflect(&BulkSearchRepositoryParams{}),
}

type BulkSearchRepositoryParams struct {
	ContextLines int                  `json:"context_lines" jsonschema:"description=The number of lines of context to include around the search term."`
	Searches     []SingleSearchParams `json:"searches" jsonschema:"description=The list of searches to perform."`
}

func BulkSearchRepository(ctx workflow.Context, envContainer env.EnvContainer, bulkSearchRepositoryParams BulkSearchRepositoryParams) (string, error) {
	if len(bulkSearchRepositoryParams.Searches) == 0 {
		return "", llm.ErrToolCallUnmarshal
	}
	results := []string{}
	for _, searchParams := range bulkSearchRepositoryParams.Searches {
		result, err := SearchRepository(ctx, envContainer, SearchRepositoryInput{
			PathGlob:     searchParams.PathGlob,
			SearchTerm:   searchParams.SearchTerm,
			ContextLines: bulkSearchRepositoryParams.ContextLines,
		})
		if err != nil {
			return "", err
		}

		// If no results were found and the glob is just a file path, add information about available symbols
		if strings.Contains(result, "No results found") && isExistentFilePath(ctx, envContainer, searchParams.PathGlob) {
			// File exists, get symbols
			filePath := searchParams.PathGlob
			symbolsMsg, err := getSymbolsMessage(ctx, envContainer, filePath)
			if err != nil {
				return "", err
			}
			if symbolsMsg != "" {
				result = fmt.Sprintf("No results found for search term '%s' in file '%s'.%s",
					searchParams.SearchTerm, searchParams.PathGlob, symbolsMsg)
			}
		}

		results = append(results, result)
	}
	return strings.Join(results, "\n"), nil
}

func ForceToolBulkSearchRepository(dCtx DevContext, chatHistory *[]llm.ChatMessage) (llm.ToolCall, error) {
	actionCtx := dCtx.ExecContext.NewActionContext("generate.repo_search_query")
	params := llm.ToolChatParams{Messages: *chatHistory}
	chatResponse, err := persisted_ai.ForceToolCall(actionCtx, dCtx.LLMConfig, &params, &bulkSearchRepositoryTool)
	*chatHistory = params.Messages // update chat history with the new messages
	if err != nil {
		return llm.ToolCall{}, fmt.Errorf("failed to force tool call: %v", err)
	}
	toolCall := chatResponse.ToolCalls[0]
	return toolCall, err
}

// isExistentFilePath returns true if the given path is a specific file path rather than a glob pattern
func isExistentFilePath(ctx workflow.Context, envContainer env.EnvContainer, path string) bool {
	// Glob patterns contain special characters: *, ?, [, ], {, }
	if !strings.ContainsAny(path, "*?[]{}") && path != "" {
		// TODO /gen replace with a new env.FileExistsActivity - we need to implement that.
		var catOutput env.EnvRunCommandOutput
		err := workflow.ExecuteActivity(ctx, env.EnvRunCommandActivity, env.EnvRunCommandActivityInput{
			EnvContainer:       envContainer,
			RelativeWorkingDir: "./",
			Command:            "cat",
			Args:               []string{path},
		}).Get(ctx, &catOutput)
		if err != nil {
			log.Error().Err(err).Msgf("failed to cat file %s", path)
		}
		if catOutput.ExitStatus == 0 {
			return true
		}
	}

	return false
}

// getSymbolsMessage returns a message about available symbols in a file if it exists
func GetSymbolsActivity(envContainer env.EnvContainer, filePath string) ([]tree_sitter.Symbol, error) {
	absolutePath := filepath.Join(envContainer.Env.GetWorkingDirectory(), filePath)
	return tree_sitter.GetFileSymbols(absolutePath)
}

func getSymbolsMessage(ctx workflow.Context, envContainer env.EnvContainer, filePath string) (string, error) {
	var symbols []tree_sitter.Symbol
	err := workflow.ExecuteActivity(ctx, GetSymbolsActivity, envContainer, filePath).Get(ctx, &symbols)
	//if err != nil && !errors.Is(err, tree_sitter.ErrFailedInferLanguage) {
	if err != nil && !strings.Contains(err.Error(), tree_sitter.ErrFailedInferLanguage.Error()) {
		return "", err
	}

	if len(symbols) == 0 {
		return fmt.Sprintf("\nNote: The file exists and can be read in full using the retrieve_code_context tool."), nil
	}

	symbolNames := make([]string, len(symbols))
	for i, symbol := range symbols {
		symbolNames[i] = symbol.Content
	}
	return fmt.Sprintf("\nNote: The file exists and the full file or specific symbols in it can be read using the retrieve_code_context tool. It contains the following symbols: %s", strings.Join(symbolNames, ", ")), nil
}
