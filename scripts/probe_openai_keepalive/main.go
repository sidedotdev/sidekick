package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"sidekick/openai_oauth"
	"sidekick/secret_manager"
	"strings"
	"time"
)

type probeRoute struct {
	endpoint  string
	token     string
	accountID string
}

func resolveRoute(ctx context.Context, auth, provider string) (probeRoute, error) {
	manager := secret_manager.NewCompositeSecretManager([]secret_manager.SecretManager{
		&secret_manager.EnvSecretManager{},
		&secret_manager.KeyringSecretManager{},
		&secret_manager.LocalConfigSecretManager{},
	})
	switch auth {
	case "api":
		key, err := manager.GetSecret(strings.ToUpper(provider) + "_API_KEY")
		if err != nil {
			return probeRoute{}, fmt.Errorf("resolve provider API key: %w", err)
		}
		return probeRoute{endpoint: "https://api.openai.com/v1/responses", token: key}, nil
	case "subscription":
		credentials, found, err := openai_oauth.GetAndMaybeRefresh(ctx, manager)
		if err != nil {
			return probeRoute{}, fmt.Errorf("resolve OpenAI OAuth credentials: %w", err)
		}
		if !found {
			return probeRoute{}, fmt.Errorf("no %s credentials configured", openai_oauth.SecretName)
		}
		return probeRoute{
			endpoint:  "https://chatgpt.com/backend-api/codex/responses",
			token:     credentials.AccessToken,
			accountID: credentials.AccountID,
		}, nil
	default:
		return probeRoute{}, fmt.Errorf("-auth must be api or subscription")
	}
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	auth := flag.String("auth", "api", "Auth route: api (OpenAI API key) or subscription (ChatGPT OAuth via the codex backend)")
	endpoint := flag.String("endpoint", "", "Exact Responses endpoint (defaults to the -auth route's endpoint)")
	model := flag.String("model", "", "Model to probe (required)")
	provider := flag.String("provider", "openai", "Provider name used for API key lookup with -auth api")
	timeout := flag.Duration("timeout", 3*time.Minute, "Maximum observation time")
	maxTokens := flag.Int("max-output-tokens", 16384, "Output token budget, including reasoning (not sent with -auth subscription, which rejects it)")
	promptFile := flag.String("prompt-file", "", "Optional file containing the exact probe prompt")
	summary := flag.String("summary", "auto", "Reasoning summary mode: auto or none")
	effort := flag.String("effort", "xhigh", "Provider reasoning effort")
	stopAfter := flag.Int("stop-after", 2, "Stop once this many keepalives of either form are observed")
	flag.Parse()
	if *summary != "auto" && *summary != "none" {
		return fmt.Errorf("-summary must be auto or none")
	}
	if *model == "" {
		return fmt.Errorf("-model is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	route, err := resolveRoute(ctx, *auth, *provider)
	if err != nil {
		return err
	}
	if *endpoint == "" {
		*endpoint = route.endpoint
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
	requestBody := map[string]any{
		"model":     *model,
		"input":     []map[string]any{{"role": "user", "content": prompt}},
		"reasoning": reasoning,
		"stream":    true, "store": false,
	}
	if *auth == "api" {
		requestBody["max_output_tokens"] = *maxTokens
	}
	body, err := json.Marshal(requestBody)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, *endpoint, strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+route.token)
	if route.accountID != "" {
		req.Header.Set("ChatGPT-Account-Id", route.accountID)
	}
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
		errorBody, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
		if err != nil {
			errorBody = fmt.Appendf(nil, "(failed to read body: %v)", err)
		}
		return fmt.Errorf("endpoint returned %s (request ID %s): %s", resp.Status, resp.Header.Get("x-request-id"), errorBody)
	}
	encoder := json.NewEncoder(os.Stdout)
	keepalives := 0
	lastLine := start
	lastKeepalive := start
	counts := make(map[string]int)
	var maxLineGap time.Duration
	defer func() {
		fmt.Fprintf(os.Stderr, "Summary elapsed=%s counts=%v keepalives=%d maxLineGap=%s finalSilence=%s\n",
			time.Since(start), counts, keepalives, maxLineGap, time.Since(lastLine))
	}()
	recordKeepalive := func(form, detail string, now time.Time) error {
		keepalives++
		err := encoder.Encode(map[string]any{
			"endpoint": *endpoint, "model": *model, "authType": *auth,
			"requestId": resp.Header.Get("x-request-id"),
			"form":      form, "detail": detail,
			"observedAt": now.UTC(), "keepaliveCount": keepalives,
			"elapsedSeconds":      now.Sub(start).Seconds(),
			"keepaliveGapSeconds": now.Sub(lastKeepalive).Seconds(),
		})
		lastKeepalive = now
		return err
	}

	// The raw stream is scanned rather than SDK-decoded because SDK decoders
	// drop SSE comment lines, one of the two keepalive forms.
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(nil, bufio.MaxScanTokenSize<<9)
	var data []byte
	for scanner.Scan() {
		line := scanner.Bytes()
		now := time.Now()
		lineGap := now.Sub(lastLine)
		maxLineGap = max(maxLineGap, lineGap)
		lastLine = now

		switch {
		case len(line) == 0:
			if len(data) == 0 {
				continue
			}
			eventData := data
			data = nil
			if strings.TrimSpace(string(eventData)) == "[DONE]" {
				return fmt.Errorf("stream finished with only %d keepalives; capability not confirmed", keepalives)
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
			if err := json.Unmarshal(eventData, &envelope); err != nil {
				return fmt.Errorf("invalid SSE JSON %q: %w", eventData, err)
			}
			counts[envelope.Type]++
			if counts[envelope.Type] == 1 {
				fmt.Fprintf(os.Stderr, "First eventType=%s elapsed=%s lineGap=%s\n",
					envelope.Type, now.Sub(start), lineGap)
			}
			switch envelope.Type {
			case "keepalive":
				if err := recordKeepalive("data_event", string(bytes.TrimSpace(eventData)), now); err != nil {
					return err
				}
			case "response.completed":
				return fmt.Errorf("response completed with only %d keepalives; capability not confirmed", keepalives)
			case "error", "response.failed", "response.incomplete":
				if envelope.Type == "error" {
					fmt.Fprintf(os.Stderr, "Provider error event: %s\n", eventData)
				}
				return fmt.Errorf("provider returned %s after %s with %d keepalives (code: %q, message: %q, incomplete reason: %q, request ID: %s)",
					envelope.Type, time.Since(start), keepalives, envelope.Code, envelope.Message,
					envelope.Response.IncompleteDetails.Reason, resp.Header.Get("x-request-id"))
			}
		case line[0] == ':':
			comment := strings.TrimSpace(string(line[1:]))
			counts[":"+comment]++
			if err := recordKeepalive("sse_comment", comment, now); err != nil {
				return err
			}
		default:
			if value, ok := bytes.CutPrefix(line, []byte("data:")); ok {
				data = append(data, bytes.TrimPrefix(value, []byte(" "))...)
				data = append(data, '\n')
			}
		}
		if keepalives >= *stopAfter {
			return nil
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	return fmt.Errorf("stream ended with only %d keepalives; capability not confirmed", keepalives)
}
