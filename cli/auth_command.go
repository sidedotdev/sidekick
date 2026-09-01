package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sidekick/llm"
	"strings"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/urfave/cli/v3"
	"golang.org/x/oauth2"
)

const (
	AnthropicOAuthSecretName = "ANTHROPIC_OAUTH"
	keyringService           = "sidekick"

	anthropicClientID       = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"
	anthropicRedirectURI    = "https://console.anthropic.com/oauth/code/callback"
	anthropicTokenEndpoint  = "https://console.anthropic.com/v1/oauth/token"
	anthropicCreateKeyURL   = "https://api.anthropic.com/api/oauth/claude_cli/create_api_key"
	anthropicConsoleScopes  = "org:create_api_key user:profile user:inference"
	anthropicClaudeAIScopes = "user:profile user:inference"
	claudeProMaxAuthURL     = "https://claude.ai/oauth/authorize"
	consoleAuthURL          = "https://console.anthropic.com/oauth/authorize"
)

type oauthTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
}

type createAPIKeyResponse struct {
	APIKey string `json:"api_key"`
}

type OAuthCredentials struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresAt    int64  `json:"expires_at"`
}

func NewAuthCommand() *cli.Command {
	return &cli.Command{
		Name:  "auth",
		Usage: "Manage LLM provider authentication",
		Action: func(ctx context.Context, cmd *cli.Command) error {
			return handleAuthCommand()
		},
	}
}

func handleAuthCommand() error {
	provider, err := selectOption("Select your LLM API provider", []string{"OpenAI", "Google", "Anthropic"})
	if err != nil {
		return fmt.Errorf("provider selection failed: %w", err)
	}

	switch provider {
	case "OpenAI":
		return handleOpenAIAuth()
	case "Google":
		return handleGoogleAuth()
	case "Anthropic":
		return handleAnthropicAuth()
	default:
		return fmt.Errorf("unknown provider: %s", provider)
	}
}

func handleOpenAIAuth() error {
	method, err := selectOption("Select authentication method", []string{
		"ChatGPT/Codex subscription (OAuth)",
		"Manually enter API Key",
	})
	if err != nil {
		return fmt.Errorf("authentication method selection failed: %w", err)
	}

	switch method {
	case "ChatGPT/Codex subscription (OAuth)":
		return handleOpenAIOAuthSubscription()
	case "Manually enter API Key":
		return handleManualAPIKeyAuth("OpenAI", llm.OpenaiApiKeySecretName)
	default:
		return fmt.Errorf("unknown authentication method: %s", method)
	}
}

func handleGoogleAuth() error {
	return handleManualAPIKeyAuth("Google", llm.GoogleApiKeySecretName)
}

func handleAnthropicAuth() error {
	method, err := selectOption("Select authentication method", []string{
		"Claude Pro/Max (OAuth subscription)",
		"Create an API Key (OAuth)",
		"Manually enter API Key",
	})
	if err != nil {
		return fmt.Errorf("authentication method selection failed: %w", err)
	}

	switch method {
	case "Claude Pro/Max (OAuth subscription)":
		return handleAnthropicOAuthSubscription()
	case "Create an API Key (OAuth)":
		return handleAnthropicOAuthCreateKey()
	case "Manually enter API Key":
		return handleManualAPIKeyAuth("Anthropic", llm.AnthropicApiKeySecretName)
	default:
		return fmt.Errorf("unknown authentication method: %s", method)
	}
}

func handleAnthropicOAuthSubscription() error {
	profileIds, err := selectCredentialProfiles("Anthropic")
	if err != nil {
		return err
	}

	targetProfileIds, err := resolveTargetProfiles(profileIds, AnthropicOAuthSecretName, "Anthropic OAuth credentials")
	if err != nil {
		return err
	}
	if len(targetProfileIds) == 0 {
		fmt.Println("✔ Keeping existing Anthropic OAuth credentials.")
		return nil
	}

	tokens, err := performOAuthFlow(claudeProMaxAuthURL)
	if err != nil {
		return err
	}

	var expiresAt int64
	if tokens.ExpiresIn > 0 {
		expiresAt = time.Now().Unix() + int64(tokens.ExpiresIn)
	}

	creds := OAuthCredentials{
		AccessToken:  tokens.AccessToken,
		RefreshToken: tokens.RefreshToken,
		ExpiresAt:    expiresAt,
	}

	return saveAnthropicOAuthCredentials(creds, targetProfileIds)
}

func saveAnthropicOAuthCredentials(creds OAuthCredentials, profileIds []string) error {
	credsJSON, err := json.Marshal(creds)
	if err != nil {
		return fmt.Errorf("failed to marshal OAuth credentials: %w", err)
	}

	if err := storeSecretForProfiles(profileIds, AnthropicOAuthSecretName, string(credsJSON)); err != nil {
		return fmt.Errorf("error storing OAuth credentials in keyring: %w", err)
	}

	fmt.Println("✔ Anthropic OAuth credentials saved.")
	return nil
}

func handleAnthropicOAuthCreateKey() error {
	profileIds, err := selectCredentialProfiles("Anthropic")
	if err != nil {
		return err
	}

	targetProfileIds, err := resolveTargetProfiles(profileIds, llm.AnthropicApiKeySecretName, "Anthropic API key")
	if err != nil {
		return err
	}
	if len(targetProfileIds) == 0 {
		fmt.Println("✔ Keeping existing Anthropic API key.")
		return nil
	}

	tokens, err := performOAuthFlow(consoleAuthURL)
	if err != nil {
		return err
	}

	apiKey, err := createAPIKeyWithOAuth(tokens.AccessToken)
	if err != nil {
		return err
	}

	if err := storeSecretForProfiles(targetProfileIds, llm.AnthropicApiKeySecretName, apiKey); err != nil {
		return fmt.Errorf("error storing API key in keyring: %w", err)
	}

	fmt.Println("✔ Anthropic API Key created and saved.")
	return nil
}

func performOAuthFlow(authBaseURL string) (*oauthTokenResponse, error) {
	verifier := oauth2.GenerateVerifier()

	authURL := buildAuthorizationURL(authBaseURL, verifier)

	fmt.Println("\nOpening browser for authentication...")
	fmt.Println("If the browser doesn't open, please visit this URL manually:")
	fmt.Println(authURL)
	fmt.Println()

	if err := openURL(authURL); err != nil {
		fmt.Printf("Warning: Could not open browser automatically: %v\n", err)
	}

	var codeWithState string
	if err := runPrompt(huh.NewInput().
		Title("Paste the authorization code from the callback page").
		Value(&codeWithState)); err != nil {
		return nil, fmt.Errorf("failed to get authorization code: %w", err)
	}

	if codeWithState == "" {
		return nil, fmt.Errorf("authorization code not provided")
	}

	// Parse the code which contains state appended after #
	parts := strings.Split(codeWithState, "#")
	code := parts[0]
	if len(parts) > 1 {
		state := parts[1]
		if state != verifier {
			return nil, fmt.Errorf("state mismatch: expected verifier but got different state")
		}
	}

	tokens, err := exchangeCodeForTokens(code, verifier)
	if err != nil {
		return nil, fmt.Errorf("failed to exchange code for tokens: %w", err)
	}

	return tokens, nil
}

func buildAuthorizationURL(baseURL, verifier string) string {
	challenge := oauth2.S256ChallengeFromVerifier(verifier)

	params := url.Values{}
	params.Set("client_id", anthropicClientID)
	params.Set("redirect_uri", anthropicRedirectURI)
	params.Set("response_type", "code")
	params.Set("code_challenge", challenge)
	params.Set("code_challenge_method", "S256")
	params.Set("state", verifier)

	params.Set("code", "true")

	isClaudeAI := baseURL == claudeProMaxAuthURL
	if isClaudeAI {
		params.Set("scope", anthropicClaudeAIScopes)
	} else {
		params.Set("scope", anthropicConsoleScopes)
	}

	return baseURL + "?" + params.Encode()
}

func exchangeCodeForTokens(code, verifier string) (*oauthTokenResponse, error) {
	requestBody := map[string]string{
		"code":          code,
		"state":         verifier,
		"grant_type":    "authorization_code",
		"client_id":     anthropicClientID,
		"redirect_uri":  anthropicRedirectURI,
		"code_verifier": verifier,
	}

	jsonBody, err := json.Marshal(requestBody)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("POST", anthropicTokenEndpoint, strings.NewReader(string(jsonBody)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
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

	var tokens oauthTokenResponse
	if err := json.Unmarshal(body, &tokens); err != nil {
		return nil, fmt.Errorf("failed to parse token response: %w", err)
	}

	return &tokens, nil
}

func createAPIKeyWithOAuth(accessToken string) (string, error) {
	req, err := http.NewRequest("POST", anthropicCreateKeyURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
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

func handleManualAPIKeyAuth(providerName, secretName string) error {
	profileIds, err := selectCredentialProfiles(providerName)
	if err != nil {
		return err
	}

	targetProfileIds, err := resolveTargetProfiles(profileIds, secretName, fmt.Sprintf("%s API key", providerName))
	if err != nil {
		return err
	}
	if len(targetProfileIds) == 0 {
		fmt.Printf("✔ Keeping existing %s API key.\n", providerName)
		return nil
	}

	apiKey, err := promptAPIKey(providerName)
	if err != nil {
		return fmt.Errorf("failed to get %s API Key: %w", providerName, err)
	}

	if apiKey == "" {
		return fmt.Errorf("%s API Key not provided", providerName)
	}

	if err := storeSecretForProfiles(targetProfileIds, secretName, apiKey); err != nil {
		return fmt.Errorf("error storing API key in keyring: %w", err)
	}

	fmt.Printf("✔ %s API Key saved.\n", providerName)
	return nil
}
