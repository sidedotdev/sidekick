package llm2

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sidekick/common"
	"sidekick/secret_manager"
	"testing"

	"github.com/openai/openai-go/v3/packages/ssestream"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func streamOpenAIResponsesFromRawSSE(t *testing.T, rawSSE string) (*MessageResponse, error) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Deliberately no Content-Type, matching the ChatGPT codex backend.
		w.Header()["Content-Type"] = nil
		fmt.Fprint(w, rawSSE)
	}))
	t.Cleanup(server.Close)

	request := StreamRequest{
		Options: Options{ModelConfig: common.ModelConfig{
			Provider: "openai",
			Model:    "gpt-4.1-nano",
		}},
		SecretManager: &secret_manager.MockSecretManager{},
	}
	provider := OpenAIResponsesProvider{BaseURL: server.URL, AuthType: common.ProviderAuthTypeAPI}
	events := make(chan Event, 100)
	return provider.Stream(context.Background(), request, events)
}

// The ChatGPT codex backend sends ": keep-alive" SSE comments during long
// reasoning, which older openai-go decoders dispatched as empty events and
// failed to JSON-decode.
func TestOpenAIResponsesStreamToleratesCommentOnlyKeepalives(t *testing.T) {
	t.Parallel()
	rawSSE := "event: response.created\n" +
		`data: {"type":"response.created","response":{"id":"resp_test","status":"in_progress"}}` + "\n\n" +
		": keep-alive\n\n" +
		"event: response.output_text.delta\n" +
		`data: {"type":"response.output_text.delta","output_index":0,"delta":"hello"}` + "\n\n" +
		": keep-alive\r\n\r\n" +
		"event: response.completed\n" +
		`data: {"type":"response.completed","response":{"id":"resp_test","status":"completed","output":[]}}` + "\n\n"

	response, err := streamOpenAIResponsesFromRawSSE(t, rawSSE)
	require.NoError(t, err)
	require.NotNil(t, response)
	assert.Equal(t, "stop", response.StopReason)
}

func TestOpenAIResponsesStreamDecodeErrorIncludesContext(t *testing.T) {
	t.Parallel()
	rawSSE := "event: response.created\n" +
		`data: {"type":"response.created","response":{"id":"resp_test","status":"in_progress"}}` + "\n\n" +
		"event: response.output_item.added\n" +
		`data: {"type":"response.output_item.added","sequence_number":2` + "\n\n"

	_, err := streamOpenAIResponsesFromRawSSE(t, rawSSE)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "after 1 events")
	assert.Contains(t, err.Error(), `"response.created"`)
	assert.NotContains(t, err.Error(), ResponseDumpDirEnvVar)
}

func TestOpenAIResponsesStreamServerErrorIncludesContext(t *testing.T) {
	t.Parallel()

	rawError := `{"type":"server_error","code":"server_error","message":"An error occurred while processing your request. Request ID req_test_failure.","param":null}`
	rawSSE := "event: response.created\n" +
		`data: {"type":"response.created","response":{"id":"resp_test","status":"in_progress"}}` + "\n\n" +
		"event: response.output_item.added\n" +
		`data: {"type":"response.output_item.added","output_index":0,"item":{"id":"msg_test","type":"message","role":"assistant","status":"in_progress","content":[]}}` + "\n\n" +
		"event: error\n" +
		`data: {"type":"error","error":` + rawError + "}\n\n"

	response, err := streamOpenAIResponsesFromRawSSE(t, rawSSE)
	require.Error(t, err)
	assert.Nil(t, response)
	assert.Contains(t, err.Error(), "model gpt-4.1-nano")
	assert.Contains(t, err.Error(), "after 2 events")
	assert.Contains(t, err.Error(), `last event type "response.output_item.added"`)
	assert.Contains(t, err.Error(), rawError)
	assert.NotContains(t, err.Error(), ResponseDumpDirEnvVar)

	var streamErr *ssestream.StreamError
	require.ErrorAs(t, err, &streamErr)
	assert.Contains(t, streamErr.Error(), rawError)
}
