package llm2

import (
	"sidekick/common"
	"testing"
)

func TestAnthropicRequestHeadersAPIKey(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                      string
		model                     string
		tools                     []*common.Tool
		assumeAnthropicModelNames bool
		wantBeta                  string
	}{
		{
			name:                      "adaptive anthropic model omits interleaved thinking beta",
			model:                     "claude-sonnet-4-6",
			assumeAnthropicModelNames: true,
			wantBeta:                  "fine-grained-tool-streaming-2025-05-14",
		},
		{
			name:                      "non adaptive anthropic model includes interleaved thinking beta",
			model:                     "claude-opus-4-5",
			assumeAnthropicModelNames: true,
			wantBeta:                  "fine-grained-tool-streaming-2025-05-14,interleaved-thinking-2025-05-14",
		},
		{
			name:                      "api key ignores claude native tools for claude code beta",
			model:                     "claude-sonnet-4-5",
			assumeAnthropicModelNames: true,
			tools: []*common.Tool{
				{Name: "Read"},
			},
			wantBeta: "fine-grained-tool-streaming-2025-05-14,interleaved-thinking-2025-05-14",
		},
		{
			name:                      "compatible providers keep interleaved thinking even for adaptive looking model names",
			model:                     "claude-sonnet-4-6",
			assumeAnthropicModelNames: false,
			wantBeta:                  "fine-grained-tool-streaming-2025-05-14,interleaved-thinking-2025-05-14",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			headers := anthropicRequestHeaders(tt.model, false, "", tt.tools, tt.assumeAnthropicModelNames)

			if got := headers["Accept"]; got != "application/json" {
				t.Fatalf("Accept = %q, want %q", got, "application/json")
			}
			if got := headers["anthropic-dangerous-direct-browser-access"]; got != "true" {
				t.Fatalf("anthropic-dangerous-direct-browser-access = %q, want %q", got, "true")
			}
			if got := headers["anthropic-beta"]; got != tt.wantBeta {
				t.Fatalf("anthropic-beta = %q, want %q", got, tt.wantBeta)
			}
			if _, ok := headers["Authorization"]; ok {
				t.Fatal("Authorization header should not be set for API-key requests")
			}
			if _, ok := headers["User-Agent"]; ok {
				t.Fatal("User-Agent header should not be set for API-key requests")
			}
			if _, ok := headers["x-app"]; ok {
				t.Fatal("x-app header should not be set for API-key requests")
			}
		})
	}
}

func TestAnthropicRequestHeadersOAuth(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                      string
		model                     string
		tools                     []*common.Tool
		assumeAnthropicModelNames bool
		wantBeta                  string
	}{
		{
			name:                      "adaptive anthropic model with native claude tools omits interleaved thinking beta",
			model:                     "claude-opus-4.6",
			assumeAnthropicModelNames: true,
			tools: []*common.Tool{
				{Name: "Read"},
			},
			wantBeta: "claude-code-20250219,oauth-2025-04-20,fine-grained-tool-streaming-2025-05-14",
		},
		{
			name:                      "non adaptive anthropic model with native claude tools includes interleaved thinking beta",
			model:                     "claude-sonnet-4-5",
			assumeAnthropicModelNames: true,
			tools: []*common.Tool{
				{Name: "Bash"},
			},
			wantBeta: "claude-code-20250219,oauth-2025-04-20,fine-grained-tool-streaming-2025-05-14,interleaved-thinking-2025-05-14",
		},
		{
			name:                      "oauth without native mapped tools omits claude code beta",
			model:                     "claude-sonnet-4-5",
			assumeAnthropicModelNames: true,
			tools: []*common.Tool{
				{Name: "get_current_weather"},
			},
			wantBeta: "oauth-2025-04-20,fine-grained-tool-streaming-2025-05-14,interleaved-thinking-2025-05-14",
		},
		{
			name:                      "oauth without tools omits claude code beta",
			model:                     "claude-opus-4.6",
			assumeAnthropicModelNames: true,
			wantBeta:                  "oauth-2025-04-20,fine-grained-tool-streaming-2025-05-14",
		},
		{
			name:                      "compatible providers keep interleaved thinking for oauth headers",
			model:                     "claude-opus-4.6",
			assumeAnthropicModelNames: false,
			tools: []*common.Tool{
				{Name: "Read"},
			},
			wantBeta: "claude-code-20250219,oauth-2025-04-20,fine-grained-tool-streaming-2025-05-14,interleaved-thinking-2025-05-14",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			headers := anthropicRequestHeaders(tt.model, true, "oauth-token", tt.tools, tt.assumeAnthropicModelNames)

			if got := headers["Authorization"]; got != "Bearer oauth-token" {
				t.Fatalf("Authorization = %q, want %q", got, "Bearer oauth-token")
			}
			if got := headers["Accept"]; got != "application/json" {
				t.Fatalf("Accept = %q, want %q", got, "application/json")
			}
			if got := headers["User-Agent"]; got != "claude-cli/2.0.65 (external, cli)" {
				t.Fatalf("User-Agent = %q, want %q", got, "claude-cli/2.0.65 (external, cli)")
			}
			if got := headers["x-app"]; got != "cli" {
				t.Fatalf("x-app = %q, want %q", got, "cli")
			}
			if got := headers["anthropic-dangerous-direct-browser-access"]; got != "true" {
				t.Fatalf("anthropic-dangerous-direct-browser-access = %q, want %q", got, "true")
			}
			if got := headers["anthropic-beta"]; got != tt.wantBeta {
				t.Fatalf("anthropic-beta = %q, want %q", got, tt.wantBeta)
			}
		})
	}
}
