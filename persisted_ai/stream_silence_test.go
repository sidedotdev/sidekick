package persisted_ai

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sidekick/common"
	"sidekick/llm2"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestStreamCancelsAfterUpstreamSilence(t *testing.T) {
	t.Parallel()
	disconnected := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(disconnected)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"type\":\"keepalive\"}\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer server.Close()

	input := newTestStreamInput(llm2.Options{ModelConfig: common.ModelConfig{
		Provider: "silence-test",
		Model:    "test-model",
	}})
	input.Providers = []common.ModelProviderPublicConfig{{
		Name:     "silence-test",
		Type:     "openai_responses_compatible",
		BaseURL:  server.URL,
		AuthType: common.ProviderAuthTypeAPI,
	}}
	input.ChatHistory = &ChatHistoryContainer{History: &Llm2ChatHistory{hydrated: true}}

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	start := time.Now()
	response, err := (&Llm2Activities{}).Stream(ctx, input)
	require.ErrorContains(t, err, "no upstream events for 35s")
	require.Nil(t, response)
	require.NoError(t, ctx.Err(), "the silence watchdog must cancel before the caller deadline")
	require.GreaterOrEqual(t, time.Since(start), upstreamSilenceGrace)
	select {
	case <-disconnected:
	case <-ctx.Done():
		t.Fatal("provider request was not canceled")
	}
}
