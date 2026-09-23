package llm2

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/openai/openai-go/v3/packages/ssestream"
)

func TestOpenAIResponsesProvider_DirectKeepaliveIntegration(t *testing.T) {
	t.Parallel()
	if os.Getenv("SIDE_OPENAI_KEEPALIVE_TEST") != "true" {
		t.Skip("Set SIDE_OPENAI_KEEPALIVE_TEST=true to run the direct OpenAI heartbeat probe")
	}
	manager := requireIntegrationAPIKey(t, "OPENAI_API_KEY")
	key, err := manager.GetSecret("OPENAI_API_KEY")
	if err != nil {
		t.Fatal(err)
	}

	model := os.Getenv("SIDE_OPENAI_KEEPALIVE_MODEL")
	if model == "" {
		model = "gpt-5.2"
	}
	body, err := json.Marshal(map[string]any{
		"model": model,
		"input": "Find all positive integer triples a <= b <= c satisfying 1/a + 1/b + 1/c = 1/12. Give a complete proof that your enumeration is exhaustive, and independently verify the enumeration.",
		"reasoning": map[string]any{
			"effort":  "high",
			"summary": "auto",
		},
		"stream":            true,
		"store":             false,
		"max_output_tokens": 8192,
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://api.openai.com/v1/responses", strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Direct OpenAI request returned %s (request ID %s)",
			resp.Status, resp.Header.Get("x-request-id"))
	}
	t.Logf("Direct OpenAI model=%s headersAfter=%s requestId=%s",
		model, time.Since(start), resp.Header.Get("x-request-id"))

	decoder := ssestream.NewDecoder(resp)
	counts := make(map[string]int)
	lastEventAt := start
	defer func() {
		t.Logf("Elapsed=%s eventCounts=%v", time.Since(start), counts)
		if counts["keepalive"] == 0 {
			t.Log("No keepalive observed; this does not establish that the endpoint lacks heartbeat support")
		}
	}()
	for decoder.Next() {
		event := decoder.Event()
		if strings.TrimSpace(string(event.Data)) == "[DONE]" {
			return
		}
		var envelope struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(event.Data, &envelope); err != nil {
			t.Fatalf("Invalid SSE JSON: %v", err)
		}
		now := time.Now()
		counts[envelope.Type]++
		if counts[envelope.Type] == 1 || envelope.Type == "keepalive" {
			t.Logf("elapsed=%s gap=%s eventType=%s",
				now.Sub(start), now.Sub(lastEventAt), envelope.Type)
		}
		lastEventAt = now
		switch envelope.Type {
		case "keepalive":
			t.Logf("Direct OpenAI keepalive payload: %s", event.Data)
		case "response.completed":
			return
		case "error", "response.failed", "response.incomplete":
			t.Fatalf("Direct OpenAI terminal event: %s", event.Data)
		}
	}
	if ctx.Err() != nil {
		t.Logf("Observation deadline reached: %v", ctx.Err())
		return
	}
	if err := decoder.Err(); err != nil {
		t.Fatal(err)
	}
	t.Fatal("Stream ended without a terminal event")
}
