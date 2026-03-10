package main

import (
	"net/url"
	"testing"
)

func TestParseOpenAIPastedInput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		input     string
		wantCode  string
		wantState string
		wantErr   bool
	}{
		{
			name:      "full redirect URL with code and state",
			input:     "http://localhost:8855/auth/openai/callback?code=abc123&state=xyz789",
			wantCode:  "abc123",
			wantState: "xyz789",
		},
		{
			name:      "full redirect URL with code only",
			input:     "http://localhost:8855/auth/openai/callback?code=abc123",
			wantCode:  "abc123",
			wantState: "",
		},
		{
			name:      "code#state format",
			input:     "abc123#xyz789",
			wantCode:  "abc123",
			wantState: "xyz789",
		},
		{
			name:      "raw query string",
			input:     "code=abc123&state=xyz789",
			wantCode:  "abc123",
			wantState: "xyz789",
		},
		{
			name:      "raw query string code only",
			input:     "code=abc123",
			wantCode:  "abc123",
			wantState: "",
		},
		{
			name:      "raw code",
			input:     "abc123def456",
			wantCode:  "abc123def456",
			wantState: "",
		},
		{
			name:    "empty input",
			input:   "",
			wantErr: true,
		},
		{
			name:    "whitespace only",
			input:   "   ",
			wantErr: true,
		},
		{
			name:      "input with leading/trailing whitespace",
			input:     "  abc123  ",
			wantCode:  "abc123",
			wantState: "",
		},
		{
			name:      "https redirect URL",
			input:     "https://example.com/callback?code=mycode&state=mystate&other=param",
			wantCode:  "mycode",
			wantState: "mystate",
		},
		{
			name:      "URL without code param falls through to raw code",
			input:     "http://localhost:8855/auth/openai/callback?error=access_denied",
			wantCode:  "http://localhost:8855/auth/openai/callback?error=access_denied",
			wantState: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			code, state, err := parseOpenAIPastedInput(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Errorf("expected error but got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if code != tt.wantCode {
				t.Errorf("code = %q, want %q", code, tt.wantCode)
			}
			if state != tt.wantState {
				t.Errorf("state = %q, want %q", state, tt.wantState)
			}
		})
	}
}

func TestParseOpenAIPastedInput_StateValidation(t *testing.T) {
	t.Parallel()

	t.Run("state mismatch is detected by caller", func(t *testing.T) {
		t.Parallel()
		// parseOpenAIPastedInput itself doesn't validate state; the caller does.
		// This test verifies state is correctly extracted for caller validation.
		code, state, err := parseOpenAIPastedInput("http://localhost:8855/auth/openai/callback?code=abc&state=wrong")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if code != "abc" {
			t.Errorf("code = %q, want %q", code, "abc")
		}
		expectedState := "expected_state"
		if state == expectedState {
			t.Error("state should not match expected_state")
		}
		if state != "wrong" {
			t.Errorf("state = %q, want %q", state, "wrong")
		}
	})

	t.Run("absent state is permissive", func(t *testing.T) {
		t.Parallel()
		_, state, err := parseOpenAIPastedInput("rawcode")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if state != "" {
			t.Errorf("state = %q, want empty", state)
		}
	})

	t.Run("code#state extracts both parts", func(t *testing.T) {
		t.Parallel()
		code, state, err := parseOpenAIPastedInput("thecode#thestate")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if code != "thecode" {
			t.Errorf("code = %q, want %q", code, "thecode")
		}
		if state != "thestate" {
			t.Errorf("state = %q, want %q", state, "thestate")
		}
	})
}

func TestBuildOpenAIAuthorizeURL(t *testing.T) {
	t.Parallel()

	verifier := "test_verifier_value"
	state := "test_state_value"
	rawURL := buildOpenAIAuthorizeURL(verifier, state)

	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("failed to parse authorize URL: %v", err)
	}

	if u.Scheme != "https" || u.Host != "auth.openai.com" || u.Path != "/oauth/authorize" {
		t.Errorf("unexpected base URL: %s", rawURL)
	}

	q := u.Query()
	expectations := map[string]string{
		"response_type":              "code",
		"client_id":                  "app_EMoamEEZ73f0CkXaXp7hrann",
		"scope":                      "openid profile email offline_access",
		"code_challenge_method":      "S256",
		"state":                      state,
		"id_token_add_organizations": "true",
		"codex_cli_simplified_flow":  "true",
		"originator":                 "pi",
	}

	for key, want := range expectations {
		if got := q.Get(key); got != want {
			t.Errorf("param %s = %q, want %q", key, got, want)
		}
	}

	if q.Get("code_challenge") == "" {
		t.Error("code_challenge should not be empty")
	}
	if q.Get("redirect_uri") == "" {
		t.Error("redirect_uri should not be empty")
	}
}
