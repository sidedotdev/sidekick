package llm2

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sidekick/common"
	"sidekick/secret_manager"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestOpenAIResponsesReportsUpstreamActivityForCommentKeepalives(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Matches the ChatGPT codex backend: no Content-Type, and only SSE
		// comment keepalives while the model reasons.
		w.Header()["Content-Type"] = nil
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		for {
			fmt.Fprint(w, ": keep-alive\n\n")
			w.(http.Flusher).Flush()
			select {
			case <-ticker.C:
			case <-release:
				return
			case <-r.Context().Done():
				return
			}
		}
	}))
	t.Cleanup(server.Close)
	t.Cleanup(func() { close(release) })

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	request := StreamRequest{
		Options: Options{ModelConfig: common.ModelConfig{
			Provider: "openai",
			Model:    "gpt-4.1-nano",
		}},
		SecretManager: &secret_manager.MockSecretManager{},
	}
	provider := OpenAIResponsesProvider{
		BaseURL:                  server.URL,
		AuthType:                 common.ProviderAuthTypeAPI,
		upstreamActivityInterval: 10 * time.Millisecond,
	}
	events := make(chan Event, 100)
	streamErr := make(chan error, 1)
	go func() {
		_, err := provider.Stream(ctx, request, events)
		streamErr <- err
	}()

	select {
	case event := <-events:
		require.Equal(t, EventUpstreamActivity, event.Type)
	case err := <-streamErr:
		t.Fatalf("stream ended before reporting upstream activity: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("comment-only keepalives produced no upstream activity")
	}
}
