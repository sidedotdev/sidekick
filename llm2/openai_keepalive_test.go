package llm2

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sidekick/common"
	"sidekick/secret_manager"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOpenAIResponsesMapsKeepaliveToHeartbeat(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, data := range []string{
			`{"type":"response.created","response":{"id":"resp_test","status":"in_progress"}}`,
			`{"type":"keepalive","sequence_number":2}`,
			`{"type":"response.future_event"}`,
			`{"type":"response.output_text.delta","output_index":0,"delta":"hello"}`,
			`{"type":"response.completed","response":{"id":"resp_test","status":"completed","output":[]}}`,
		} {
			fmt.Fprintf(w, "data: %s\n\n", data)
		}
	}))
	defer server.Close()

	request := StreamRequest{
		Options: Options{ModelConfig: common.ModelConfig{
			Provider: "openai",
			Model:    "gpt-4.1-nano",
		}},
		SecretManager: &secret_manager.MockSecretManager{},
	}
	provider := OpenAIResponsesProvider{BaseURL: server.URL, AuthType: common.ProviderAuthTypeAPI}
	events := make(chan Event, 10)
	response, err := provider.Stream(context.Background(), request, events)
	require.NoError(t, err)
	require.NotNil(t, response)
	close(events)
	var observed []Event
	for event := range events {
		observed = append(observed, event)
	}
	require.Equal(t, []Event{
		{Type: EventHeartbeat},
		{Type: EventTextDelta, Delta: "hello"},
	}, observed)
}
