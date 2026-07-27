package dev

import (
	"context"
	"fmt"
	"sidekick/coding/tree_sitter"
	"sidekick/common"
	"sidekick/env"
	"sidekick/flow_action"
	"sidekick/llm"
	"sidekick/persisted_ai"
	"sidekick/utils"
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

type BulkSearchRepositoryActivityInput struct {
	EnvContainer env.EnvContainer           `json:"envContainer"`
	Params       BulkSearchRepositoryParams `json:"params"`
}

type BulkSearchRepositoryActivityOutput struct {
	Result string `json:"result"`
}

// BulkSearchRepositoryActivity performs all the searches within a single
// activity, so that the many intermediate command inputs and outputs never
// cross the activity boundary.
func BulkSearchRepositoryActivity(ctx context.Context, input BulkSearchRepositoryActivityInput) (BulkSearchRepositoryActivityOutput, error) {
	result, err := bulkSearchRepository(activitySearchRunner{ctx: ctx}, input.EnvContainer, input.Params)
	if err != nil {
		return BulkSearchRepositoryActivityOutput{}, err
	}
	return BulkSearchRepositoryActivityOutput{Result: result}, nil
}

func BulkSearchRepository(ctx workflow.Context, envContainer env.EnvContainer, bulkSearchRepositoryParams BulkSearchRepositoryParams) (string, error) {
	if len(bulkSearchRepositoryParams.Searches) == 0 {
		return "", llm.ErrToolCallUnmarshal
	}

	v := workflow.GetVersion(ctx, "bulk-search-single-activity", workflow.DefaultVersion, 1)
	if v >= 1 {
		var output BulkSearchRepositoryActivityOutput
		err := workflow.ExecuteActivity(ctx, BulkSearchRepositoryActivity, BulkSearchRepositoryActivityInput{
			EnvContainer: envContainer,
			Params:       bulkSearchRepositoryParams,
		}).Get(ctx, &output)
		if err != nil {
			return "", err
		}
		return output.Result, nil
	}

	return bulkSearchRepository(workflowSearchRunner{ctx: ctx}, envContainer, bulkSearchRepositoryParams)
}

func bulkSearchRepository(runner searchRunner, envContainer env.EnvContainer, bulkSearchRepositoryParams BulkSearchRepositoryParams) (string, error) {
	results := []string{}
	for _, searchParams := range bulkSearchRepositoryParams.Searches {
		result, err := searchRepository(runner, envContainer, SearchRepositoryInput{
			PathGlob:     searchParams.PathGlob,
			SearchTerm:   searchParams.SearchTerm,
			ContextLines: bulkSearchRepositoryParams.ContextLines,
		})
		if err != nil {
			return "", err
		}

		// If no results were found and the glob is just a file path, add information about available symbols
		if strings.Contains(result, "No results found") && isExistentFilePath(runner, envContainer, searchParams.PathGlob) {
			// File exists, get symbols
			filePath := searchParams.PathGlob
			symbolsMsg, err := getSymbolsMessage(runner, envContainer, filePath)
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

func ForceToolBulkSearchRepository(dCtx DevContext, chatHistory *persisted_ai.ChatHistoryContainer) ([]llm.ToolCall, error) {
	actionCtx := dCtx.ExecContext.NewActionContext("generate.repo_search_query")
	modelConfig := dCtx.GetModelConfig(common.CodeLocalizationKey, 0, "default")
	toolNameMapping, err := resolveStreamToolNameMapping(actionCtx.ExecContext, modelConfig, *actionCtx.Secrets)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve tool name mapping: %v", err)
	}
	response, err := persisted_ai.ForceToolCallWithTrackOptionsV2(
		actionCtx,
		flow_action.TrackOptions{},
		modelConfig,
		chatHistory,
		toolNameMapping,
		&bulkSearchRepositoryTool,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to force tool call: %v", err)
	}
	return response.GetMessage().GetToolCalls(), nil
}

// isExistentFilePath returns true if the given path is a specific file path rather than a glob pattern
func isExistentFilePath(runner searchRunner, envContainer env.EnvContainer, path string) bool {
	// Glob patterns contain special characters: *, ?, [, ], {, }
	if !strings.ContainsAny(path, "*?[]{}") && path != "" {
		// TODO /gen replace with a new env.FileExistsActivity - we need to implement that.
		catOutput, err := runner.runCommand(env.EnvRunCommandActivityInput{
			EnvContainer:       envContainer,
			RelativeWorkingDir: "./",
			Command:            "cat",
			Args:               []string{path},
		})
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
	return getFileSymbols(context.Background(), envContainer, filePath)
}

func getFileSymbols(ctx context.Context, envContainer env.EnvContainer, filePath string) ([]tree_sitter.Symbol, error) {
	fileBytes, err := envContainer.Env.ReadFile(ctx, filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read file %s: %w", filePath, err)
	}
	langName := utils.InferLanguageNameFromFilePath(filePath)
	return tree_sitter.GetFileSymbolsFromBytes(filePath, langName, fileBytes)
}

func getSymbolsMessage(runner searchRunner, envContainer env.EnvContainer, filePath string) (string, error) {
	symbols, err := runner.getSymbols(envContainer, filePath)
	//if err != nil && !errors.Is(err, tree_sitter.ErrFailedInferLanguage) {
	if err != nil && !strings.Contains(err.Error(), tree_sitter.ErrFailedInferLanguage.Error()) {
		return "", err
	}

	if len(symbols) == 0 {
		return fmt.Sprintf("\nNote: The file exists and can be read in full using the get_symbol_definitions tool."), nil
	}

	symbolNames := make([]string, len(symbols))
	for i, symbol := range symbols {
		symbolNames[i] = symbol.Content
	}
	return fmt.Sprintf("\nNote: The file exists and the full file or specific symbols in it can be read using the get_symbol_definitions tool. It contains the following symbols: %s", strings.Join(symbolNames, ", ")), nil
}
