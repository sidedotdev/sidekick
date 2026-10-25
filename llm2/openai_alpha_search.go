package llm2

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"sidekick/common"
	"sidekick/secret_manager"

	"github.com/segmentio/ksuid"
)

// OpenAIAlphaSearchRequest is a standalone web search executed against the
// ChatGPT codex backend's alpha/search endpoint, which Codex uses instead of
// the hosted web_search Responses tool.
type OpenAIAlphaSearchRequest struct {
	Provider        string
	Model           string
	Queries         []string
	AllowedDomains  []string
	MaxOutputTokens int
}

type OpenAIAlphaSearchResult struct {
	Type    string `json:"type"`
	Domain  string `json:"domain"`
	RefId   string `json:"ref_id"`
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
}

type OpenAIAlphaSearchResponse struct {
	Output  string                    `json:"output"`
	Results []OpenAIAlphaSearchResult `json:"results"`
}

const defaultOpenAIAlphaSearchMaxOutputTokens = 2000

// AlphaSearch is only available with subscription (ChatGPT OAuth) credentials,
// since alpha/search is a codex backend endpoint.
func (p OpenAIResponsesProvider) AlphaSearch(ctx context.Context, secretManager secret_manager.SecretManager, request OpenAIAlphaSearchRequest) (*OpenAIAlphaSearchResponse, error) {
	if request.Model == "" {
		return nil, fmt.Errorf("alpha search requires a model")
	}
	commands := make([]map[string]string, 0, len(request.Queries))
	for _, query := range request.Queries {
		query = strings.TrimSpace(query)
		if query == "" {
			return nil, fmt.Errorf("alpha search queries must not be empty")
		}
		commands = append(commands, map[string]string{"q": query})
	}
	if len(commands) == 0 {
		return nil, fmt.Errorf("alpha search requires at least one query")
	}

	credentials, err := openAICredentialsForRequest(secretManager, request.Provider, common.ProviderAuthTypeSubscription)
	if err != nil {
		return nil, err
	}

	maxOutputTokens := request.MaxOutputTokens
	if maxOutputTokens <= 0 {
		maxOutputTokens = defaultOpenAIAlphaSearchMaxOutputTokens
	}
	settings := map[string]any{"external_web_access": true}
	if len(request.AllowedDomains) > 0 {
		settings["filters"] = map[string]any{"allowed_domains": request.AllowedDomains}
	}
	body, err := json.Marshal(map[string]any{
		"id":                ksuid.New().String(),
		"model":             request.Model,
		"commands":          map[string]any{"search_query": commands},
		"settings":          settings,
		"max_output_tokens": maxOutputTokens,
	})
	if err != nil {
		return nil, fmt.Errorf("encode alpha search request: %w", err)
	}

	baseURL := p.BaseURL
	if baseURL == "" {
		baseURL = openAIChatGPTResponsesBaseURL
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(baseURL, "/")+"/alpha/search", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpRequest.Header.Set("Authorization", "Bearer "+credentials.token)
	httpRequest.Header.Set("ChatGPT-Account-Id", credentials.accountID)
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Accept", "application/json")
	for k, v := range p.CustomHeaders {
		httpRequest.Header.Set(k, v)
	}

	client := withResponseDump(&http.Client{Timeout: 2 * time.Minute})
	httpResponse, err := client.Do(httpRequest)
	if err != nil {
		return nil, fmt.Errorf("alpha search request failed: %w", err)
	}
	defer httpResponse.Body.Close()
	responseBody, err := io.ReadAll(httpResponse.Body)
	if err != nil {
		return nil, fmt.Errorf("read alpha search response: %w", err)
	}
	if httpResponse.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("alpha search returned HTTP %d: %s", httpResponse.StatusCode, truncateForError(responseBody, 500))
	}

	var response OpenAIAlphaSearchResponse
	if err := json.Unmarshal(responseBody, &response); err != nil {
		return nil, fmt.Errorf("decode alpha search response: %w", err)
	}
	if response.Output == "" && len(response.Results) == 0 {
		return nil, fmt.Errorf("alpha search returned neither output nor results")
	}
	return &response, nil
}

func truncateForError(body []byte, limit int) string {
	if len(body) <= limit {
		return string(body)
	}
	return string(body[:limit]) + "..."
}
