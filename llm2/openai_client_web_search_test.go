package llm2

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"sidekick/common"
	"sidekick/secret_manager"

	"github.com/invopop/jsonschema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func sseEvent(t *testing.T, payload map[string]any) string {
	t.Helper()
	data, err := json.Marshal(payload)
	require.NoError(t, err)
	return fmt.Sprintf("event: %s\ndata: %s\n\n", payload["type"], data)
}

func sseFunctionCall(t *testing.T, outputIndex int, callID, name, arguments string) string {
	t.Helper()
	itemID := "fc_" + callID
	return sseEvent(t, map[string]any{"type": "response.output_item.added", "output_index": outputIndex, "item": map[string]any{
		"type": "function_call", "id": itemID, "call_id": callID, "name": name, "arguments": "", "status": "in_progress",
	}}) +
		sseEvent(t, map[string]any{"type": "response.function_call_arguments.delta", "output_index": outputIndex, "item_id": itemID, "delta": arguments}) +
		sseEvent(t, map[string]any{"type": "response.output_item.done", "output_index": outputIndex, "item": map[string]any{
			"type": "function_call", "id": itemID, "call_id": callID, "name": name, "arguments": arguments, "status": "completed",
		}})
}

func sseResponse(t *testing.T, items ...string) string {
	t.Helper()
	return sseEvent(t, map[string]any{"type": "response.created", "response": map[string]any{"id": "resp_test", "status": "in_progress"}}) +
		strings.Join(items, "") +
		sseEvent(t, map[string]any{"type": "response.completed", "response": map[string]any{
			"id": "resp_test", "status": "completed", "output": []any{},
			"usage": map[string]any{"input_tokens": 10, "output_tokens": 5},
		}})
}

func sseText(t *testing.T, outputIndex int, text string) string {
	t.Helper()
	return sseEvent(t, map[string]any{"type": "response.content_part.added", "output_index": outputIndex, "item_id": "msg_test", "content_index": 0,
		"part": map[string]any{"type": "output_text", "text": "", "annotations": []any{}}}) +
		sseEvent(t, map[string]any{"type": "response.output_text.delta", "output_index": outputIndex, "item_id": "msg_test", "content_index": 0, "delta": text})
}

func searchCallArguments(query string) string {
	return fmt.Sprintf(`{"query":%q}`, query)
}

type fakeCodexBackend struct {
	t              *testing.T
	responseRounds func(round int) string
	alphaStatus    int
	alphaBody      string

	mu               sync.Mutex
	responseRequests []map[string]any
	alphaRequests    []map[string]any
}

func (b *fakeCodexBackend) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(r.Body)
	if !assert.NoError(b.t, err) {
		return
	}
	var body map[string]any
	if !assert.NoError(b.t, json.Unmarshal(raw, &body)) {
		return
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	switch r.URL.Path {
	case "/responses":
		b.responseRequests = append(b.responseRequests, body)
		w.Header().Set("Openai-Organization", "org-test")
		w.Header().Set("Openai-Project", "project-test")
		w.Header()["Content-Type"] = nil
		fmt.Fprint(w, b.responseRounds(len(b.responseRequests)))
	case "/alpha/search":
		b.alphaRequests = append(b.alphaRequests, body)
		w.WriteHeader(b.alphaStatus)
		fmt.Fprint(w, b.alphaBody)
	default:
		b.t.Errorf("unexpected request path %s", r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
	}
}

func requestToolSummaries(t *testing.T, body map[string]any) []string {
	t.Helper()
	rawTools, _ := body["tools"].([]any)
	summaries := make([]string, 0, len(rawTools))
	for _, rawTool := range rawTools {
		tool, ok := rawTool.(map[string]any)
		require.True(t, ok, "tool must be an object: %v", rawTool)
		if name, ok := tool["name"].(string); ok {
			summaries = append(summaries, fmt.Sprintf("%s:%s", tool["type"], name))
		} else {
			summaries = append(summaries, fmt.Sprint(tool["type"]))
		}
	}
	return summaries
}

func requestInputItems(t *testing.T, body map[string]any, itemType string) []map[string]any {
	t.Helper()
	rawItems, ok := body["input"].([]any)
	require.True(t, ok, "input must be an item list: %v", body["input"])
	var items []map[string]any
	for _, rawItem := range rawItems {
		item, ok := rawItem.(map[string]any)
		require.True(t, ok)
		if item["type"] == itemType {
			items = append(items, item)
		}
	}
	return items
}

func jsonText(t *testing.T, value any) string {
	t.Helper()
	raw, err := json.Marshal(value)
	require.NoError(t, err)
	return string(raw)
}

func webSearchTestRequest(secretManager secret_manager.SecretManager) StreamRequest {
	return StreamRequest{
		Messages: []Message{{Role: RoleUser, Content: TextContentBlocks("What is the latest Go release?")}},
		Options: Options{
			ModelConfig: common.ModelConfig{Provider: "openai", Model: "gpt-6-astra"},
			Tools: []*common.Tool{
				{Type: common.ToolTypeWebSearch, WebSearch: &common.WebSearchToolConfig{AllowedDomains: []string{"go.dev"}}},
				{Name: "advisor_guide", Description: "Ask the advisor.", Parameters: &jsonschema.Schema{Type: "object"}},
			},
		},
		SecretManager: secretManager,
	}
}

func streamAgainstFakeCodexBackend(t *testing.T, backend *fakeCodexBackend, authType common.ProviderAuthType, secretManager secret_manager.SecretManager) (*MessageResponse, error) {
	t.Helper()
	server := httptest.NewServer(backend)
	t.Cleanup(server.Close)
	provider := OpenAIResponsesProvider{
		BaseURL:      server.URL,
		AuthType:     authType,
		BuiltinTools: []string{webSearchToolName},
	}
	return provider.Stream(context.Background(), webSearchTestRequest(secretManager), make(chan Event, 1000))
}

const alphaSearchSuccessBody = `{"output":"Go 1.27 was released in August.","encrypted_output":"opaque","results":[{"type":"text_result","domain":"go.dev","ref_id":"turn0search0","title":"Go 1.27 Release Notes","url":"https://go.dev/doc/go1.27","snippet":"notes"}]}`

func TestOpenAIResponsesProvider_SubscriptionWebSearchRunsClientSide(t *testing.T) {
	t.Parallel()

	backend := &fakeCodexBackend{
		t: t,
		responseRounds: func(round int) string {
			if round == 1 {
				return sseResponse(t, sseFunctionCall(t, 0, "call_search", clientWebSearchFunctionName, searchCallArguments("latest go release")))
			}
			return sseResponse(t, sseText(t, 0, "Go 1.27 is the latest release."))
		},
		alphaStatus: http.StatusOK,
		alphaBody:   alphaSearchSuccessBody,
	}

	server := httptest.NewServer(backend)
	t.Cleanup(server.Close)
	provider := OpenAIResponsesProvider{
		BaseURL:      server.URL,
		AuthType:     common.ProviderAuthTypeSubscription,
		BuiltinTools: []string{webSearchToolName},
	}
	request := webSearchTestRequest(alphaSearchTestSecrets(t))
	response, err := provider.Stream(context.Background(), request, make(chan Event, 1000))
	require.NoError(t, err)

	require.Len(t, backend.responseRequests, 2)
	for _, request := range backend.responseRequests {
		assert.ElementsMatch(t, []string{"function:advisor_guide", "function:" + clientWebSearchFunctionName}, requestToolSummaries(t, request),
			"subscription requests must declare a client search function instead of the hosted web_search tool")
	}

	require.Len(t, backend.alphaRequests, 1)
	assert.Equal(t, "gpt-6-astra", backend.alphaRequests[0]["model"])
	assert.JSONEq(t, `{"search_query":[{"q":"latest go release"}]}`, jsonText(t, backend.alphaRequests[0]["commands"]))
	assert.JSONEq(t, `{"external_web_access":true,"filters":{"allowed_domains":["go.dev"]}}`, jsonText(t, backend.alphaRequests[0]["settings"]))

	continuation := backend.responseRequests[1]
	calls := requestInputItems(t, continuation, "function_call")
	require.Len(t, calls, 1)
	assert.Equal(t, "call_search", calls[0]["call_id"])
	assert.Equal(t, clientWebSearchFunctionName, calls[0]["name"])
	assert.JSONEq(t, searchCallArguments("latest go release"), calls[0]["arguments"].(string))
	outputs := requestInputItems(t, continuation, "function_call_output")
	require.Len(t, outputs, 1)
	assert.Equal(t, "call_search", outputs[0]["call_id"])
	assert.Contains(t, jsonText(t, outputs[0]["output"]), "Go 1.27 was released in August.")
	assert.Contains(t, jsonText(t, outputs[0]["output"]), "https://go.dev/doc/go1.27")

	expectedContent := []ContentBlock{
		{
			Id:   "fc_call_search",
			Type: ContentBlockTypeBuiltinToolUse,
			BuiltinToolUse: &BuiltinToolUseBlock{
				Id: "call_search", Name: webSearchToolName, Arguments: searchCallArguments("latest go release"),
			},
		},
		{
			Type: ContentBlockTypeBuiltinToolResult,
			BuiltinToolResult: &BuiltinToolResultBlock{
				ToolCallId:    "call_search",
				Name:          webSearchToolName,
				Content:       "Go 1.27 was released in August.",
				SearchResults: []WebSearchResult{{URL: "https://go.dev/doc/go1.27", Title: "Go 1.27 Release Notes"}},
			},
		},
		{Id: "msg_test", Type: ContentBlockTypeText, Text: "Go 1.27 is the latest release."},
	}
	for i := range expectedContent {
		expectedContent[i].Metadata = map[string]any{MetadataNamespaceCore: &CoreBlockMetadata{
			Provider:     "openai",
			ProviderType: string(common.OpenaiResponsesCompatibleChatProvider),
			Headers:      map[string]string{"Openai-Organization": "org-test", "Openai-Project": "project-test"},
		}}
	}
	assert.Equal(t, expectedContent, response.Output.Content)
	assert.Equal(t, "stop", response.StopReason)
	assert.Equal(t, Usage{InputTokens: 20, OutputTokens: 10}, response.Usage)

	rawHistory, err := json.Marshal(response.Output)
	require.NoError(t, err)
	var history Message
	require.NoError(t, json.Unmarshal(rawHistory, &history))
	request.Messages = append(request.Messages, history, Message{Role: RoleUser, Content: TextContentBlocks("Summarize that.")})
	_, err = provider.Stream(context.Background(), request, make(chan Event, 1000))
	require.NoError(t, err)
	require.Len(t, backend.responseRequests, 3)
	assert.Len(t, backend.alphaRequests, 1)

	replay := backend.responseRequests[2]
	assert.Empty(t, requestInputItems(t, replay, "web_search_call"))
	replayCalls := requestInputItems(t, replay, "function_call")
	require.Len(t, replayCalls, 1)
	assert.Equal(t, "call_search", replayCalls[0]["call_id"])
	assert.Equal(t, clientWebSearchFunctionName, replayCalls[0]["name"])
	replayOutputs := requestInputItems(t, replay, "function_call_output")
	require.Len(t, replayOutputs, 1)
	assert.Equal(t, "call_search", replayOutputs[0]["call_id"])
	assert.Contains(t, jsonText(t, replayOutputs[0]["output"]), "Go 1.27 was released in August.")
	assert.Contains(t, jsonText(t, replayOutputs[0]["output"]), "https://go.dev/doc/go1.27")
}

func TestOpenAIResponsesProvider_SubscriptionWebSearchEdgeCases(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		responseRounds func(t *testing.T, round int) string
		alphaStatus    int
		alphaBody      string
		check          func(t *testing.T, backend *fakeCodexBackend, response *MessageResponse, err error)
	}{
		{
			name: "search failure is reported to the model and the response continues",
			responseRounds: func(t *testing.T, round int) string {
				if round == 1 {
					return sseResponse(t, sseFunctionCall(t, 0, "call_search", clientWebSearchFunctionName, searchCallArguments("q")))
				}
				return sseResponse(t, sseText(t, 0, "Search was unavailable."))
			},
			alphaStatus: http.StatusBadGateway,
			alphaBody:   `{"error":"upstream"}`,
			check: func(t *testing.T, backend *fakeCodexBackend, response *MessageResponse, err error) {
				require.NoError(t, err)
				require.Len(t, backend.responseRequests, 2)
				outputs := requestInputItems(t, backend.responseRequests[1], "function_call_output")
				require.Len(t, outputs, 1)
				assert.Contains(t, jsonText(t, outputs[0]["output"]), "Web search failed")

				require.Len(t, response.Output.Content, 3)
				result := response.Output.Content[1].BuiltinToolResult
				require.NotNil(t, result)
				assert.True(t, result.IsError)
				assert.Equal(t, "Search was unavailable.", response.Output.Content[2].Text)
			},
		},
		{
			name: "workflow tool call alongside a search returns for workflow execution",
			responseRounds: func(t *testing.T, round int) string {
				return sseResponse(t,
					sseFunctionCall(t, 0, "call_search", clientWebSearchFunctionName, searchCallArguments("q")),
					sseFunctionCall(t, 1, "call_advisor", "advisor_guide", `{}`),
				)
			},
			alphaStatus: http.StatusOK,
			alphaBody:   alphaSearchSuccessBody,
			check: func(t *testing.T, backend *fakeCodexBackend, response *MessageResponse, err error) {
				require.NoError(t, err)
				assert.Len(t, backend.responseRequests, 1)
				assert.Len(t, backend.alphaRequests, 1)

				var types []ContentBlockType
				for _, block := range response.Output.Content {
					types = append(types, block.Type)
				}
				assert.Equal(t, []ContentBlockType{ContentBlockTypeBuiltinToolUse, ContentBlockTypeToolUse, ContentBlockTypeBuiltinToolResult}, types)
				assert.Equal(t, "advisor_guide", response.Output.Content[1].ToolUse.Name)
				assert.Equal(t, "call_search", response.Output.Content[2].BuiltinToolResult.ToolCallId)
			},
		},
		{
			name: "search tool is withdrawn on the final round",
			responseRounds: func(t *testing.T, round int) string {
				return sseResponse(t, sseFunctionCall(t, 0, fmt.Sprintf("call_%d", round), clientWebSearchFunctionName, searchCallArguments("q")))
			},
			alphaStatus: http.StatusOK,
			alphaBody:   alphaSearchSuccessBody,
			check: func(t *testing.T, backend *fakeCodexBackend, response *MessageResponse, err error) {
				require.ErrorContains(t, err, "after the search tool was withdrawn")
				require.Len(t, backend.responseRequests, maxClientWebSearchRounds)
				for i, request := range backend.responseRequests[:maxClientWebSearchRounds-1] {
					assert.Contains(t, requestToolSummaries(t, request), "function:"+clientWebSearchFunctionName, "round %d", i+1)
				}
				assert.Equal(t, []string{"function:advisor_guide"}, requestToolSummaries(t, backend.responseRequests[maxClientWebSearchRounds-1]))
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			backend := &fakeCodexBackend{
				t:              t,
				responseRounds: func(round int) string { return tc.responseRounds(t, round) },
				alphaStatus:    tc.alphaStatus,
				alphaBody:      tc.alphaBody,
			}
			response, err := streamAgainstFakeCodexBackend(t, backend, common.ProviderAuthTypeSubscription, alphaSearchTestSecrets(t))
			tc.check(t, backend, response, err)
		})
	}
}

func TestOpenAIResponsesProvider_APIKeyKeepsHostedWebSearch(t *testing.T) {
	t.Parallel()

	backend := &fakeCodexBackend{
		t:              t,
		responseRounds: func(int) string { return sseResponse(t, sseText(t, 0, "answer")) },
	}
	_, err := streamAgainstFakeCodexBackend(t, backend, common.ProviderAuthTypeAPI, &secret_manager.MockSecretManager{})
	require.NoError(t, err)

	require.Len(t, backend.responseRequests, 1)
	assert.ElementsMatch(t, []string{"web_search", "function:advisor_guide"}, requestToolSummaries(t, backend.responseRequests[0]))
	assert.Empty(t, backend.alphaRequests)
}

func TestOpenAIResponsesProvider_CompletesFunctionArguments(t *testing.T) {
	t.Parallel()

	const arguments = `{"answer":"complete"}`
	tests := []struct {
		name    string
		initial string
		delta   string
	}{
		{name: "completion only"},
		{name: "partial delta", delta: `{"answer":`},
		{name: "complete delta", delta: arguments},
		{name: "complete initial item", initial: arguments},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			added := sseEvent(t, map[string]any{
				"type": "response.output_item.added", "output_index": 0,
				"item": map[string]any{
					"type": "function_call", "id": "fc_answer", "call_id": "call_answer",
					"name": "advisor_guide", "arguments": tc.initial, "status": "in_progress",
				},
			})
			delta := sseEvent(t, map[string]any{
				"type": "response.function_call_arguments.delta", "output_index": 0,
				"item_id": "fc_answer", "delta": tc.delta,
			})
			done := sseEvent(t, map[string]any{
				"type": "response.output_item.done", "output_index": 0,
				"item": map[string]any{
					"type": "function_call", "id": "fc_answer", "call_id": "call_answer",
					"name": "advisor_guide", "arguments": arguments, "status": "completed",
				},
			})
			backend := &fakeCodexBackend{
				t:              t,
				responseRounds: func(int) string { return sseResponse(t, added, delta, done) },
			}
			server := httptest.NewServer(backend)
			t.Cleanup(server.Close)

			provider := OpenAIResponsesProvider{BaseURL: server.URL, AuthType: common.ProviderAuthTypeAPI, BuiltinTools: []string{webSearchToolName}}
			events := make(chan Event, 100)
			response, err := provider.Stream(context.Background(), webSearchTestRequest(&secret_manager.MockSecretManager{}), events)
			close(events)
			require.NoError(t, err)
			require.Len(t, response.Output.Content, 1)
			require.NotNil(t, response.Output.Content[0].ToolUse)
			assert.JSONEq(t, arguments, response.Output.Content[0].ToolUse.Arguments)

			var streamed []Event
			for event := range events {
				streamed = append(streamed, event)
			}
			expectedOutput := AccumulateEventsToMessage(streamed)
			require.Len(t, expectedOutput.Content, 1)
			expectedOutput.Content[0].Metadata = map[string]any{MetadataNamespaceCore: &CoreBlockMetadata{
				Provider:     "openai",
				ProviderType: string(common.OpenaiResponsesCompatibleChatProvider),
				Headers:      map[string]string{"Openai-Organization": "org-test", "Openai-Project": "project-test"},
			}}
			assert.Equal(t, expectedOutput, response.Output)
		})
	}
}

func TestOpenAIResponsesProvider_ReplaysBuiltinSearchByOrigin(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		metadata   *CoreBlockMetadata
		wantNative bool
	}{
		{name: "untagged hosted search", wantNative: true},
		{
			name:       "same provider",
			metadata:   &CoreBlockMetadata{Provider: "openai", ProviderType: string(common.OpenaiResponsesCompatibleChatProvider)},
			wantNative: true,
		},
		{
			name:     "different provider name",
			metadata: &CoreBlockMetadata{Provider: "other-openai", ProviderType: string(common.OpenaiResponsesCompatibleChatProvider)},
		},
		{
			name:     "different provider type",
			metadata: &CoreBlockMetadata{Provider: "openai", ProviderType: string(common.OpenaiToolChatProviderType)},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			backend := &fakeCodexBackend{
				t:              t,
				responseRounds: func(int) string { return sseResponse(t, sseText(t, 0, "answer")) },
			}
			server := httptest.NewServer(backend)
			t.Cleanup(server.Close)
			provider := OpenAIResponsesProvider{
				BaseURL:      server.URL,
				AuthType:     common.ProviderAuthTypeAPI,
				BuiltinTools: []string{webSearchToolName},
			}
			block := ContentBlock{
				Type: ContentBlockTypeBuiltinToolUse,
				BuiltinToolUse: &BuiltinToolUseBlock{
					Id: "ws_history", Name: webSearchToolName, Arguments: `{"type":"search","query":"q"}`, Status: "completed",
				},
			}
			if tc.metadata != nil {
				block.Metadata = map[string]any{MetadataNamespaceCore: tc.metadata}
			}
			request := webSearchTestRequest(&secret_manager.MockSecretManager{})
			request.Messages = append([]Message{{Role: RoleAssistant, Content: []ContentBlock{block}}}, request.Messages...)

			_, err := provider.Stream(context.Background(), request, make(chan Event, 100))
			require.NoError(t, err)
			require.Len(t, backend.responseRequests, 1)
			body := backend.responseRequests[0]
			nativeCalls := requestInputItems(t, body, "web_search_call")
			calls := requestInputItems(t, body, "function_call")
			outputs := requestInputItems(t, body, "function_call_output")
			if tc.wantNative {
				require.Len(t, nativeCalls, 1)
				assert.Equal(t, "ws_history", nativeCalls[0]["id"])
				assert.Empty(t, calls)
				assert.Empty(t, outputs)
			} else {
				assert.Empty(t, nativeCalls)
				require.Len(t, calls, 1)
				require.Len(t, outputs, 1)
				assert.Equal(t, clientWebSearchFunctionName, calls[0]["name"])
				assert.JSONEq(t, block.BuiltinToolUse.Arguments, calls[0]["arguments"].(string))
				assert.Equal(t, calls[0]["call_id"], outputs[0]["call_id"])
				assert.Contains(t, jsonText(t, outputs[0]["output"]), "Web search was performed server-side")
			}
			assert.Empty(t, backend.alphaRequests)
		})
	}
}
