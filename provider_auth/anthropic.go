package provider_auth

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/oauth2"
)

const (
	anthropicClientID = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"
	// AnthropicManualRedirectURI is Anthropic's hosted callback page, which
	// displays the authorization code for the user to paste back.
	AnthropicManualRedirectURI = "https://console.anthropic.com/oauth/code/callback"
	anthropicConsoleScopes     = "org:create_api_key user:profile user:inference"
	// Matches current Claude Code scopes; user:sessions:claude_code entitles
	// subscription tokens to newer models.
	anthropicClaudeAIScopes = "user:profile user:inference user:sessions:claude_code user:mcp_servers user:file_upload"
	ClaudeProMaxAuthURL     = "https://claude.ai/oauth/authorize"
	ConsoleAuthURL          = "https://console.anthropic.com/oauth/authorize"
)

// Overridable in tests.
var (
	anthropicTokenEndpoint         = "https://console.anthropic.com/v1/oauth/token"
	anthropicClaudeAITokenEndpoint = "https://platform.claude.com/v1/oauth/token"
	anthropicCreateKeyURL          = "https://api.anthropic.com/api/oauth/claude_cli/create_api_key"
	anthropicHTTPClient            = &http.Client{}
)

type TokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
}

type createAPIKeyResponse struct {
	APIKey string `json:"api_key"`
}

// AnthropicOAuthCredentials is the keyring representation of ANTHROPIC_OAUTH.
type AnthropicOAuthCredentials struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresAt    int64  `json:"expires_at"`
}

func AnthropicCredentialsFromTokens(tokens *TokenResponse, now time.Time) AnthropicOAuthCredentials {
	var expiresAt int64
	if tokens.ExpiresIn > 0 {
		expiresAt = now.Unix() + int64(tokens.ExpiresIn)
	}
	return AnthropicOAuthCredentials{
		AccessToken:  tokens.AccessToken,
		RefreshToken: tokens.RefreshToken,
		ExpiresAt:    expiresAt,
	}
}

// ParseAnthropicAuthorizationInput extracts the authorization code from
// user-pasted input, validating state against the PKCE verifier when present.
// Accepted formats: a full callback URL with code & state query params, a
// "code#state" combined string, or a raw authorization code.
func ParseAnthropicAuthorizationInput(input, verifier string) (string, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return "", fmt.Errorf("authorization code not provided")
	}

	if u, parseErr := url.Parse(input); parseErr == nil && u.Scheme != "" && u.Host != "" {
		q := u.Query()
		if c := q.Get("code"); c != "" {
			if state := q.Get("state"); state != "" && state != verifier {
				return "", fmt.Errorf("state mismatch: expected verifier but got different state")
			}
			return c, nil
		}
		return "", fmt.Errorf("callback URL missing authorization code")
	}

	parts := strings.Split(input, "#")
	code := parts[0]
	if len(parts) > 1 {
		state := parts[1]
		if state != verifier {
			return "", fmt.Errorf("state mismatch: expected verifier but got different state")
		}
	}
	return code, nil
}

func BuildAuthorizationURL(baseURL, redirectURI, verifier string) string {
	challenge := oauth2.S256ChallengeFromVerifier(verifier)

	params := url.Values{}
	params.Set("client_id", anthropicClientID)
	params.Set("redirect_uri", redirectURI)
	params.Set("response_type", "code")
	params.Set("code_challenge", challenge)
	params.Set("code_challenge_method", "S256")
	params.Set("state", verifier)

	params.Set("code", "true")

	isClaudeAI := baseURL == ClaudeProMaxAuthURL
	if isClaudeAI {
		params.Set("scope", anthropicClaudeAIScopes)
	} else {
		params.Set("scope", anthropicConsoleScopes)
	}

	return baseURL + "?" + params.Encode()
}

func buildTokenExchangeRequest(authBaseURL, redirectURI, code, verifier string) (*http.Request, error) {
	requestBody := map[string]string{
		"code":          code,
		"state":         verifier,
		"grant_type":    "authorization_code",
		"client_id":     anthropicClientID,
		"redirect_uri":  redirectURI,
		"code_verifier": verifier,
	}

	jsonBody, err := json.Marshal(requestBody)
	if err != nil {
		return nil, err
	}

	tokenEndpoint := anthropicTokenEndpoint
	if authBaseURL == ClaudeProMaxAuthURL {
		tokenEndpoint = anthropicClaudeAITokenEndpoint
	}
	req, err := http.NewRequest("POST", tokenEndpoint, strings.NewReader(string(jsonBody)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return req, nil
}

func ExchangeCodeForTokens(authBaseURL, redirectURI, code, verifier string) (*TokenResponse, error) {
	req, err := buildTokenExchangeRequest(authBaseURL, redirectURI, code, verifier)
	if err != nil {
		return nil, err
	}

	resp, err := anthropicHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token exchange failed with status %d: %s", resp.StatusCode, string(body))
	}

	var tokens TokenResponse
	if err := json.Unmarshal(body, &tokens); err != nil {
		return nil, fmt.Errorf("failed to parse token response: %w", err)
	}

	return &tokens, nil
}

func CreateAPIKeyWithOAuth(accessToken string) (string, error) {
	req, err := http.NewRequest("POST", anthropicCreateKeyURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := anthropicHTTPClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("API key creation failed with status %d: %s", resp.StatusCode, string(body))
	}

	var keyResp createAPIKeyResponse
	if err := json.Unmarshal(body, &keyResp); err != nil {
		return "", fmt.Errorf("failed to parse API key response: %w", err)
	}

	if keyResp.APIKey == "" {
		return "", fmt.Errorf("received empty API key from server")
	}

	return keyResp.APIKey, nil
}
