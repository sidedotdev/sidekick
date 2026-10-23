package llm2

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"sidekick/common"
	"sidekick/secret_manager"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/responses"
	"github.com/rs/zerolog/log"
)

// On the ChatGPT codex backend (subscription auth), the hosted web_search tool
// fails with server_error after the search completes in most requests. Codex
// itself declares a client-executed search function backed by alpha/search for
// such models, so subscription requests do the same: the model calls this
// function, the provider runs alpha/search and continues the response. The
// name matches clientNameForBuiltinTool so that recorded searches replay as
// the same function call pairs in later requests.
const clientWebSearchFunctionName = builtinToolClientNamePrefix + webSearchToolName

// maxClientWebSearchRounds bounds the responses requested within one Stream
// call. The search function is withheld from the final round so the model
// must answer or call a workflow tool.
const maxClientWebSearchRounds = 8

const clientWebSearchDescription = "Search the web for current information. Returns relevant page text along with source titles and URLs."

type clientWebSearch struct {
	allowedDomains []string
}

type clientWebSearchCall struct {
	callID    string
	arguments string
}

// extractClientWebSearch removes the web search tool from tools, returning the
// remaining tools and the search configuration, or nil if none was requested.
func extractClientWebSearch(tools []*common.Tool) ([]*common.Tool, *clientWebSearch) {
	var search *clientWebSearch
	remaining := make([]*common.Tool, 0, len(tools))
	for _, tool := range tools {
		if tool == nil || tool.Type != common.ToolTypeWebSearch {
			remaining = append(remaining, tool)
			continue
		}
		search = &clientWebSearch{}
		if tool.WebSearch != nil {
			search.allowedDomains = tool.WebSearch.AllowedDomains
		}
	}
	return remaining, search
}

func openaiClientWebSearchTool() responses.ToolUnionParam {
	return responses.ToolUnionParam{OfFunction: &responses.FunctionToolParam{
		Name:        clientWebSearchFunctionName,
		Description: openai.String(clientWebSearchDescription),
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{"type": "string", "description": "The search query."},
			},
			"required":             []string{"query"},
			"additionalProperties": false,
		},
	}}
}

func withoutClientWebSearchTool(params *responses.ResponseNewParams) {
	tools := make([]responses.ToolUnionParam, 0, len(params.Tools))
	for _, tool := range params.Tools {
		if tool.OfFunction != nil && tool.OfFunction.Name == clientWebSearchFunctionName {
			continue
		}
		tools = append(tools, tool)
	}
	params.Tools = tools
	if len(tools) == 0 {
		params.Tools = nil
		params.ToolChoice = responses.ResponseNewParamsToolChoiceUnion{}
	}
}

// runClientWebSearch reports search failures to the model as error results so
// it can rephrase or proceed; only context cancellation is returned as an
// error.
func (p OpenAIResponsesProvider) runClientWebSearch(
	ctx context.Context,
	eventChan chan<- Event,
	activityInterval time.Duration,
	secretManager secret_manager.SecretManager,
	provider, model string,
	search *clientWebSearch,
	call clientWebSearchCall,
) (BuiltinToolResultBlock, error) {
	result := BuiltinToolResultBlock{ToolCallId: call.callID, Name: webSearchToolName}

	var arguments struct {
		Query string `json:"query"`
	}
	if err := json.Unmarshal([]byte(call.arguments), &arguments); err != nil || strings.TrimSpace(arguments.Query) == "" {
		result.IsError = true
		result.Content = fmt.Sprintf("Invalid web search arguments %q: a non-empty query is required.", call.arguments)
		return result, nil
	}

	type searchOutcome struct {
		response *OpenAIAlphaSearchResponse
		err      error
	}
	done := make(chan searchOutcome, 1)
	go func() {
		response, err := p.AlphaSearch(ctx, secretManager, OpenAIAlphaSearchRequest{
			Provider:       provider,
			Model:          model,
			Queries:        []string{arguments.Query},
			AllowedDomains: search.allowedDomains,
		})
		done <- searchOutcome{response: response, err: err}
	}()

	ticker := time.NewTicker(activityInterval)
	defer ticker.Stop()
	var outcome searchOutcome
wait:
	for {
		select {
		case outcome = <-done:
			break wait
		case <-ticker.C:
			select {
			case eventChan <- Event{Type: EventUpstreamActivity}:
			case <-ctx.Done():
				return result, ctx.Err()
			}
		case <-ctx.Done():
			return result, ctx.Err()
		}
	}

	if outcome.err != nil {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		log.Warn().Err(outcome.err).Str("model", model).Msg("client web search failed")
		result.IsError = true
		result.Content = fmt.Sprintf("Web search failed: %v", outcome.err)
		return result, nil
	}

	result.Content = outcome.response.Output
	for _, item := range outcome.response.Results {
		if item.URL == "" {
			continue
		}
		result.SearchResults = append(result.SearchResults, WebSearchResult{URL: item.URL, Title: item.Title})
	}
	return result, nil
}

func nextEventIndex(events []Event) int {
	next := 0
	for _, event := range events {
		if event.Index >= next {
			next = event.Index + 1
		}
	}
	return next
}

func hasClientToolUseBlock(events []Event) bool {
	for _, event := range events {
		if event.Type == EventBlockStarted && event.ContentBlock != nil && event.ContentBlock.Type == ContentBlockTypeToolUse {
			return true
		}
	}
	return false
}

func addUsage(total, usage Usage) Usage {
	total.InputTokens += usage.InputTokens
	total.OutputTokens += usage.OutputTokens
	total.CacheReadInputTokens += usage.CacheReadInputTokens
	total.CacheWriteInputTokens += usage.CacheWriteInputTokens
	return total
}
