package llm2

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sidekick/common"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeAnthropicTextStream(w http.ResponseWriter, organizationId string) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set(anthropicOrganizationIdHeader, organizationId)
	events := [][2]string{
		{"message_start", `{"type":"message_start","message":{"id":"msg_test","type":"message","role":"assistant","content":[],"model":"claude-test","stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":1,"output_tokens":1}}}`},
		{"content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`},
		{"content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"ok"}}`},
		{"content_block_stop", `{"type":"content_block_stop","index":0}`},
		{"message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":1}}`},
		{"message_stop", `{"type":"message_stop"}`},
	}
	for _, event := range events {
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event[0], event[1])
	}
}

func anthropicCoreMetadataTestRequest(history []Message) StreamRequest {
	return StreamRequest{
		Messages: history,
		Options: Options{ModelConfig: common.ModelConfig{
			Provider: "anthropic",
			Model:    "claude-test",
		}},
		SecretManager: anthropicAuthTestSecretManager{
			secrets: map[string]string{"ANTHROPIC_API_KEY": "sk-test"},
		},
	}
}

func drainEvents() chan Event {
	events := make(chan Event, 100)
	go func() {
		for range events {
		}
	}()
	return events
}

func TestAnthropicProvider_StampsCoreMetadataOnOutputBlocks(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeAnthropicTextStream(w, "org-current")
	}))
	defer server.Close()

	events := drainEvents()
	defer close(events)
	response, err := AnthropicProvider{BaseURL: server.URL, AuthType: common.ProviderAuthTypeAPI}.Stream(
		context.Background(),
		anthropicCoreMetadataTestRequest([]Message{{Role: RoleUser, Content: TextContentBlocks("hi")}}),
		events,
	)
	require.NoError(t, err)
	require.NotEmpty(t, response.Output.Content)

	for _, block := range response.Output.Content {
		roundTripped := roundTripContentBlock(t, block)
		for _, b := range []ContentBlock{block, roundTripped} {
			assert.Equal(t, &CoreBlockMetadata{
				Provider:     "anthropic",
				ProviderType: "anthropic",
				Headers:      map[string]string{anthropicOrganizationIdHeader: "org-current"},
			}, GetCoreBlockMetadata(b))
		}
	}
}

func roundTripContentBlock(t *testing.T, block ContentBlock) ContentBlock {
	t.Helper()
	data, err := json.Marshal(block)
	require.NoError(t, err)
	var out ContentBlock
	require.NoError(t, json.Unmarshal(data, &out))
	return out
}

func anthropicWebSearchBlocks(id, organizationId string) []ContentBlock {
	var metadata map[string]any
	if organizationId != "" {
		metadata = map[string]any{MetadataNamespaceCore: map[string]any{
			"provider":     "anthropic",
			"providerType": "anthropic",
			"headers":      map[string]any{anthropicOrganizationIdHeader: organizationId},
		}}
	}
	return []ContentBlock{
		{
			Type:           ContentBlockTypeBuiltinToolUse,
			BuiltinToolUse: &BuiltinToolUseBlock{Id: id, Name: webSearchToolName, Arguments: `{"query":"q"}`},
			Metadata:       metadata,
		},
		{
			Type: ContentBlockTypeBuiltinToolResult,
			BuiltinToolResult: &BuiltinToolResultBlock{
				ToolCallId:    id,
				Name:          webSearchToolName,
				SearchResults: []WebSearchResult{{URL: "https://example.com", Title: "Example", EncryptedContent: "enc-" + id}},
			},
			Metadata: metadata,
		},
	}
}

// anthropicRequestBlockTypesById maps each tool call id in an Anthropic
// request body to the wire block types that reference it.
func anthropicRequestBlockTypesById(t *testing.T, body []byte) map[string][]string {
	t.Helper()
	var request struct {
		Messages []struct {
			Content []struct {
				Type      string `json:"type"`
				Id        string `json:"id"`
				ToolUseId string `json:"tool_use_id"`
			} `json:"content"`
		} `json:"messages"`
	}
	require.NoError(t, json.Unmarshal(body, &request))
	typesById := make(map[string][]string)
	for _, msg := range request.Messages {
		for _, block := range msg.Content {
			id := block.Id
			if id == "" {
				id = block.ToolUseId
			}
			if id != "" {
				typesById[id] = append(typesById[id], block.Type)
			}
		}
	}
	return typesById
}

func TestAnthropicProvider_RetriesRejectedSearchResultsFromOtherOrganizationsAsText(t *testing.T) {
	t.Parallel()

	const rejectedSearchResultBody = "{\"type\":\"error\",\"error\":{\"type\":\"invalid_request_error\",\"message\":\"messages.1.content.0: Invalid `encrypted_content` in `search_result` block\"}}"

	tests := []struct {
		name                string
		errorOrganization   string
		errorBody           string
		wantRequests        int
		wantErr             bool
		wantRetryBlockTypes map[string][]string
	}{
		{
			name:              "search results from other organizations are converted",
			errorOrganization: "org-current",
			errorBody:         rejectedSearchResultBody,
			wantRequests:      2,
			wantRetryBlockTypes: map[string][]string{
				"srvtoolu_old":      {"tool_use", "tool_result"},
				"srvtoolu_untagged": {"tool_use", "tool_result"},
				"srvtoolu_current":  {"server_tool_use", "web_search_tool_result"},
			},
		},
		{
			name:         "rejection without an organization header is not retried",
			errorBody:    rejectedSearchResultBody,
			wantRequests: 1,
			wantErr:      true,
		},
		{
			name:              "unrelated bad requests are not retried",
			errorOrganization: "org-current",
			errorBody:         `{"type":"error","error":{"type":"invalid_request_error","message":"something else"}}`,
			wantRequests:      1,
			wantErr:           true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var mu sync.Mutex
			var bodies [][]byte
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				mu.Lock()
				bodies = append(bodies, body)
				attempt := len(bodies)
				mu.Unlock()
				if attempt == 1 {
					if tt.errorOrganization != "" {
						w.Header().Set(anthropicOrganizationIdHeader, tt.errorOrganization)
					}
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusBadRequest)
					fmt.Fprint(w, tt.errorBody)
					return
				}
				writeAnthropicTextStream(w, "org-current")
			}))
			defer server.Close()

			var assistantContent []ContentBlock
			assistantContent = append(assistantContent, anthropicWebSearchBlocks("srvtoolu_old", "org-old")...)
			assistantContent = append(assistantContent, anthropicWebSearchBlocks("srvtoolu_untagged", "")...)
			assistantContent = append(assistantContent, anthropicWebSearchBlocks("srvtoolu_current", "org-current")...)
			assistantContent = append(assistantContent, ContentBlock{Type: ContentBlockTypeText, Text: "found it"})
			history := []Message{
				{Role: RoleUser, Content: TextContentBlocks("search")},
				{Role: RoleAssistant, Content: assistantContent},
				{Role: RoleUser, Content: TextContentBlocks("thanks")},
			}

			events := drainEvents()
			defer close(events)
			_, err := AnthropicProvider{BaseURL: server.URL, AuthType: common.ProviderAuthTypeAPI}.Stream(
				context.Background(), anthropicCoreMetadataTestRequest(history), events,
			)

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			require.Len(t, bodies, tt.wantRequests)
			if tt.wantRetryBlockTypes != nil {
				assert.Equal(t, tt.wantRetryBlockTypes, anthropicRequestBlockTypesById(t, bodies[1]))
			}
		})
	}
}
