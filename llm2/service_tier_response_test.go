package llm2

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sidekick/common"
	"sidekick/secret_manager"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const anthropicMinimalSSE = "event: message_start\n" +
	`data: {"type":"message_start","message":{"id":"msg_test","type":"message","role":"assistant","model":"claude-opus-5","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":1,"output_tokens":0}}}` + "\n\n" +
	"event: content_block_start\n" +
	`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}` + "\n\n" +
	"event: content_block_delta\n" +
	`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}` + "\n\n" +
	"event: content_block_stop\n" +
	`data: {"type":"content_block_stop","index":0}` + "\n\n" +
	"event: message_delta\n" +
	`data: {"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":1}}` + "\n\n" +
	"event: message_stop\n" +
	`data: {"type":"message_stop"}` + "\n\n"

const openaiResponsesMinimalSSE = "event: response.created\n" +
	`data: {"type":"response.created","response":{"id":"resp_test","status":"in_progress"}}` + "\n\n" +
	"event: response.completed\n" +
	`data: {"type":"response.completed","response":{"id":"resp_test","status":"completed","output":[]}}` + "\n\n"

// rawSSEServer replies to every request with rawSSE, returning a function
// that yields the JSON body of the request it received.
func rawSSEServer(t *testing.T, rawSSE string) (*httptest.Server, func() map[string]any) {
	t.Helper()
	bodies := make(chan map[string]any, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		bodies <- body
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, rawSSE)
	}))
	t.Cleanup(server.Close)

	return server, func() map[string]any {
		t.Helper()
		select {
		case body := <-bodies:
			return body
		default:
			t.Fatal("provider did not send a request")
			return nil
		}
	}
}

func sentString(body map[string]any, key string) string {
	value, _ := body[key].(string)
	return value
}

func TestAnthropicProviderResponseRecordsSentTier(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name            string
		model           string
		speed           string
		serviceTier     string
		wantSpeed       string
		wantServiceTier string
	}{
		{name: "standard", model: "claude-opus-5"},
		{name: "fast on opus 5", model: "claude-opus-5", speed: "fast", wantSpeed: "fast"},
		{name: "fast on opus 5.5", model: "claude-opus-5-5", speed: "fast", wantSpeed: "fast"},
		{name: "fast on opus 4.8", model: "claude-opus-4-8", speed: "fast", wantSpeed: "fast"},
		{name: "fast on model without fast mode", model: "claude-opus-4-7", speed: "fast", wantSpeed: ""},
		{name: "service tier", model: "claude-opus-5", serviceTier: "standard_only", wantServiceTier: "standard_only"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			server, sentBody := rawSSEServer(t, anthropicMinimalSSE)
			provider := AnthropicProvider{BaseURL: server.URL, AuthType: common.ProviderAuthTypeAPI}
			request := StreamRequest{
				Messages: []Message{{
					Role:    RoleUser,
					Content: []ContentBlock{{Type: ContentBlockTypeText, Text: "Hello"}},
				}},
				Options: Options{
					ModelConfig: common.ModelConfig{
						Provider:    "anthropic",
						Model:       tt.model,
						Speed:       tt.speed,
						ServiceTier: tt.serviceTier,
					},
					MaxTokens: 16,
				},
				SecretManager: anthropicAuthTestSecretManager{
					secrets: map[string]string{"ANTHROPIC_API_KEY": "test-api-key"},
				},
			}

			response, err := provider.Stream(context.Background(), request, make(chan Event, 100))
			require.NoError(t, err)

			body := sentBody()
			assert.Equal(t, tt.wantSpeed, response.Speed)
			assert.Equal(t, tt.wantServiceTier, response.ServiceTier)
			assert.Equal(t, sentString(body, "speed"), response.Speed)
			assert.Equal(t, sentString(body, "service_tier"), response.ServiceTier)
		})
	}
}

func TestOpenAIResponsesProviderResponseRecordsSentTier(t *testing.T) {
	t.Parallel()
	for _, serviceTier := range []string{"", "flex", "priority", "ultrafast"} {
		t.Run(fmt.Sprintf("tier %q", serviceTier), func(t *testing.T) {
			t.Parallel()
			server, sentBody := rawSSEServer(t, openaiResponsesMinimalSSE)
			provider := OpenAIResponsesProvider{BaseURL: server.URL, AuthType: common.ProviderAuthTypeAPI}
			request := StreamRequest{
				Options: Options{ModelConfig: common.ModelConfig{
					Provider:    "openai",
					Model:       "gpt-4.1-nano",
					ServiceTier: serviceTier,
				}},
				SecretManager: &secret_manager.MockSecretManager{},
			}

			response, err := provider.Stream(context.Background(), request, make(chan Event, 100))
			require.NoError(t, err)

			body := sentBody()
			assert.Equal(t, serviceTier, response.ServiceTier)
			assert.Equal(t, sentString(body, "service_tier"), response.ServiceTier)
			assert.Empty(t, response.Speed)
		})
	}
}
