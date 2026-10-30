package llm2

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"sidekick/common"

	"github.com/stretchr/testify/require"
)

func TestAnthropicCacheControlLimit(t *testing.T) {
	t.Parallel()

	for _, count := range []int{0, 1, 4, 6, 12} {
		t.Run(fmt.Sprintf("%d markers", count), func(t *testing.T) {
			t.Parallel()

			var messages []Message
			for i := 0; i < count; i++ {
				messages = append(messages, Message{
					Role: RoleUser,
					Content: []ContentBlock{{
						Type: ContentBlockTypeText, Text: fmt.Sprintf("block %d", i),
						CacheControl: "ephemeral",
					}},
				})
			}
			before, err := json.Marshal(messages)
			require.NoError(t, err)

			params, err := messagesToAnthropicParams(messages)
			require.NoError(t, err)
			encoded, err := json.Marshal(params)
			require.NoError(t, err)
			var wire []struct {
				Content []struct {
					Text         string         `json:"text"`
					CacheControl map[string]any `json:"cache_control"`
				} `json:"content"`
			}
			require.NoError(t, json.Unmarshal(encoded, &wire))

			index, markers := 0, 0
			for _, message := range wire {
				for _, block := range message.Content {
					require.Equal(t, fmt.Sprintf("block %d", index), block.Text)
					// Preserve the stable prefix and the most recent breakpoints.
					wantMarker := count <= 4 || index == 0 || index >= count-3
					require.Equal(t, wantMarker, block.CacheControl != nil)
					if block.CacheControl != nil {
						markers++
					}
					index++
				}
			}
			require.Equal(t, count, index)
			require.Equal(t, min(count, 4), markers)
			after, err := json.Marshal(messages)
			require.NoError(t, err)
			require.JSONEq(t, string(before), string(after))
		})
	}
}

func TestAnthropicStreamLimitsPersistedCacheMarkers(t *testing.T) {
	t.Parallel()

	messages := []Message{
		{Role: RoleUser, Content: []ContentBlock{
			{Type: ContentBlockTypeText, Text: "instructions", CacheControl: "ephemeral"},
			{Type: ContentBlockTypeImage, Image: &ImageRef{Url: "https://example.com/image.png"}, CacheControl: "ephemeral"},
		}},
		{Role: RoleAssistant, Content: []ContentBlock{
			{Type: ContentBlockTypeToolUse, ToolUse: &ToolUseBlock{Id: "call-1", Name: "lookup", Arguments: "{}"}, CacheControl: "ephemeral"},
		}},
		{Role: RoleUser, Content: []ContentBlock{
			{Type: ContentBlockTypeToolResult, ToolResult: &ToolResultBlock{ToolCallId: "call-1", Content: TextContentBlocks("result")}, CacheControl: "ephemeral"},
		}},
		{Role: RoleAssistant, Content: []ContentBlock{
			{Type: ContentBlockTypeRefusal, Refusal: &RefusalBlock{Reason: "unavailable"}, CacheControl: "ephemeral"},
		}},
		{Role: RoleUser, Content: []ContentBlock{
			{Type: ContentBlockTypeText, Text: "try again", CacheControl: "ephemeral"},
		}},
	}
	before, err := json.Marshal(messages)
	require.NoError(t, err)
	requests := make(chan map[string]any, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		requests <- body
		writeAnthropicTextStream(w, "org-test")
	}))
	defer server.Close()

	request := providerAuthTestRequest("anthropic", "claude-opus-4-5", map[string]string{"ANTHROPIC_API_KEY": "test-key"})
	request.Messages = messages
	response, err := (AnthropicProvider{BaseURL: server.URL, AuthType: common.ProviderAuthTypeAPI}).Stream(
		context.Background(), request, make(chan Event, 100),
	)
	require.NoError(t, err)
	require.NotNil(t, response)

	body := <-requests
	var countMarkers func(any) int
	countMarkers = func(value any) int {
		count := 0
		switch value := value.(type) {
		case map[string]any:
			for key, child := range value {
				if key == "cache_control" {
					count++
				}
				count += countMarkers(child)
			}
		case []any:
			for _, child := range value {
				count += countMarkers(child)
			}
		}
		return count
	}
	require.Equal(t, 4, countMarkers(body))
	wireMessages := body["messages"].([]any)
	require.Len(t, wireMessages, len(messages))
	for i, message := range wireMessages {
		blocks := message.(map[string]any)["content"].([]any)
		require.Len(t, blocks, len(messages[i].Content))
	}
	after, err := json.Marshal(messages)
	require.NoError(t, err)
	require.JSONEq(t, string(before), string(after))
}
