package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"sidekick/secret_manager"
	"strings"
	"time"

	"github.com/openai/openai-go/v3/packages/ssestream"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	endpoint := flag.String("endpoint", "https://api.openai.com/v1/responses", "Exact Responses endpoint")
	model := flag.String("model", "", "Model to probe (required)")
	provider := flag.String("provider", "openai", "Provider name used for secret lookup")
	timeout := flag.Duration("timeout", 3*time.Minute, "Maximum observation time")
	maxTokens := flag.Int("max-output-tokens", 16384, "Output token budget, including reasoning")
	promptFile := flag.String("prompt-file", "", "Optional file containing the exact probe prompt")
	summary := flag.String("summary", "auto", "Reasoning summary mode: auto or none")
	effort := flag.String("effort", "xhigh", "Provider reasoning effort")
	flag.Parse()
	if *summary != "auto" && *summary != "none" {
		return fmt.Errorf("-summary must be auto or none")
	}
	if *model == "" {
		return fmt.Errorf("-model is required")
	}
	manager := secret_manager.NewCompositeSecretManager([]secret_manager.SecretManager{
		&secret_manager.EnvSecretManager{},
		&secret_manager.KeyringSecretManager{},
		&secret_manager.LocalConfigSecretManager{},
	})
	key, err := manager.GetSecret(strings.ToUpper(*provider) + "_API_KEY")
	if err != nil {
		return fmt.Errorf("resolve provider API key: %w", err)
	}
	prompt := "Design a small Go HTTP client that retries transient failures safely. Explain how you would handle deadlines, cancellation, idempotency, Retry-After, and jitter. Include a concise implementation and a few focused test cases."
	if *promptFile != "" {
		data, err := os.ReadFile(*promptFile)
		if err != nil {
			return fmt.Errorf("read prompt: %w", err)
		}
		prompt = string(data)
		if strings.TrimSpace(prompt) == "" {
			return fmt.Errorf("prompt file is empty")
		}
	}
	reasoning := map[string]any{"effort": *effort}
	if *summary != "none" {
		reasoning["summary"] = *summary
	}
	body, err := json.Marshal(map[string]any{
		"model":     *model,
		"input":     prompt,
		"reasoning": reasoning,
		"stream":    true, "store": false, "max_output_tokens": *maxTokens,
	})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, *endpoint, strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	start := time.Now()
	fmt.Fprintf(os.Stderr, "Starting endpoint=%s model=%s timeout=%s maxOutputTokens=%d\n", *endpoint, *model, *timeout, *maxTokens)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	fmt.Fprintf(os.Stderr, "Headers after=%s status=%s requestId=%s contentType=%s\n",
		time.Since(start), resp.Status, resp.Header.Get("x-request-id"), resp.Header.Get("Content-Type"))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("endpoint returned %s (request ID %s)", resp.Status, resp.Header.Get("x-request-id"))
	}
	encoder := json.NewEncoder(os.Stdout)
	decoder := ssestream.NewDecoder(resp)
	keepalives := 0
	lastEvent := start
	lastKeepalive := start
	counts := make(map[string]int)
	var maxEventGap time.Duration
	defer func() {
		fmt.Fprintf(os.Stderr, "Summary elapsed=%s counts=%v maxEventGap=%s finalSilence=%s\n",
			time.Since(start), counts, maxEventGap, time.Since(lastEvent))
	}()
	for decoder.Next() {
		data := decoder.Event().Data
		if strings.TrimSpace(string(data)) == "[DONE]" {
			break
		}
		var envelope struct {
			Type     string `json:"type"`
			Code     string `json:"code"`
			Message  string `json:"message"`
			Response struct {
				IncompleteDetails struct {
					Reason string `json:"reason"`
				} `json:"incomplete_details"`
			} `json:"response"`
		}
		if err := json.Unmarshal(data, &envelope); err != nil {
			return fmt.Errorf("invalid SSE JSON: %w", err)
		}
		now := time.Now()
		counts[envelope.Type]++
		maxEventGap = max(maxEventGap, now.Sub(lastEvent))
		if counts[envelope.Type] == 1 {
			fmt.Fprintf(os.Stderr, "First eventType=%s elapsed=%s gap=%s\n",
				envelope.Type, now.Sub(start), now.Sub(lastEvent))
		}
		if envelope.Type == "keepalive" {
			keepalives++
			if err := encoder.Encode(map[string]any{
				"endpoint": *endpoint, "model": *model, "authType": "api",
				"requestId":  resp.Header.Get("x-request-id"),
				"observedAt": now.UTC(), "keepaliveCount": keepalives,
				"elapsedSeconds":      now.Sub(start).Seconds(),
				"eventGapSeconds":     now.Sub(lastEvent).Seconds(),
				"keepaliveGapSeconds": now.Sub(lastKeepalive).Seconds(),
			}); err != nil {
				return err
			}
			lastKeepalive = now
			if keepalives >= 2 {
				return nil
			}
		}
		lastEvent = now
		switch envelope.Type {
		case "response.completed":
			return fmt.Errorf("response completed with only %d keepalives; capability not confirmed", keepalives)
		case "error", "response.failed", "response.incomplete":
			if envelope.Type == "error" {
				fmt.Fprintf(os.Stderr, "Provider error event: %s\n", data)
			}
			return fmt.Errorf("provider returned %s after %s with %d keepalives (code: %q, message: %q, incomplete reason: %q, request ID: %s)",
				envelope.Type, time.Since(start), keepalives, envelope.Code, envelope.Message,
				envelope.Response.IncompleteDetails.Reason, resp.Header.Get("x-request-id"))
		}
	}
	if err := decoder.Err(); err != nil {
		return err
	}
	return fmt.Errorf("stream ended with only %d keepalives; capability not confirmed", keepalives)
}
