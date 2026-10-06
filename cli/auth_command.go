package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sidekick/common"
	"sidekick/llm"
	"sidekick/provider_auth"
	"strings"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/urfave/cli/v3"
)

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

	targetProfileIds, err := resolveTargetProfiles(profileIds, llm.AnthropicOAuthSecretName, "Anthropic OAuth credentials")
	if err != nil {
		return err
	}
	if len(targetProfileIds) == 0 {
		fmt.Println("✔ Keeping existing Anthropic OAuth credentials.")
		return nil
	}

	if err := performAnthropicOAuthFlow(targetProfileIds, provider_auth.MethodOAuthSubscription); err != nil {
		return err
	}

	fmt.Println("✔ Anthropic OAuth credentials saved.")
	notifyCredentialsUpdated(provider_auth.ProviderAnthropic, targetProfileIds, provider_auth.MethodOAuthSubscription)
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

	if err := performAnthropicOAuthFlow(targetProfileIds, provider_auth.MethodOAuthCreateKey); err != nil {
		return err
	}

	fmt.Println("✔ Anthropic API Key created and saved.")
	notifyCredentialsUpdated(provider_auth.ProviderAnthropic, targetProfileIds, provider_auth.MethodOAuthCreateKey)
	return nil
}

func performAnthropicOAuthFlow(profileIds []string, method provider_auth.Method) error {
	session, err := provider_auth.StartOAuthSessionForProfiles(provider_auth.ProviderAnthropic, profileIds, method)
	if err != nil {
		return err
	}
	defer session.Cancel()

	if callbackErr := session.CallbackError(); callbackErr != nil {
		fmt.Printf("Could not start the OAuth callback server, falling back to manual code entry: %v\n", callbackErr)
	}

	fmt.Println("\nOpening browser for authentication...")
	fmt.Println("If the browser doesn't open, please visit this URL manually:")
	fmt.Println(session.AuthURL)
	fmt.Println()

	if err := openURL(session.AuthURL); err != nil {
		fmt.Printf("Warning: Could not open browser automatically: %v\n", err)
	}

	return awaitOAuthSession(session, "Complete login in your browser, or paste the authorization code or callback URL here")
}

// awaitOAuthSession races the session's loopback callback against manual
// paste of the code or callback URL.
func awaitOAuthSession(session *provider_auth.OAuthSession, manualInputTitle string) error {
	ctx, cancel := context.WithTimeout(context.Background(), provider_auth.OAuthSessionTimeout)
	defer cancel()

	type manualInputResult struct {
		value string
		err   error
	}

	manualResults := make(chan manualInputResult, 1)
	go func() {
		var input string
		err := huh.NewForm(
			huh.NewGroup(
				huh.NewInput().
					Title(manualInputTitle).
					Value(&input),
			),
		).RunWithContext(ctx)
		manualResults <- manualInputResult{value: input, err: err}
	}()

	select {
	case <-session.Done():
		cancel()
		<-manualResults
	case manual := <-manualResults:
		if manual.err != nil {
			return fmt.Errorf("failed to get authorization input: %w", manual.err)
		}
		if err := session.SubmitManualCode(manual.value); err != nil {
			return err
		}
		<-session.Done()
	}

	_, err := session.Status()
	return err
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

	if err := provider_auth.StoreSecretForProfiles(targetProfileIds, secretName, apiKey); err != nil {
		return fmt.Errorf("error storing API key in keyring: %w", err)
	}

	fmt.Printf("✔ %s API Key saved.\n", providerName)
	notifyCredentialsUpdated(strings.ToLower(providerName), targetProfileIds, provider_auth.MethodAPIKey)
	return nil
}

// notifyCredentialsUpdated lets a running Sidekick server resume flows that
// are blocked on the saved credentials. Overridable in tests.
var notifyCredentialsUpdated = func(provider string, profileIds []string, method provider_auth.Method) {
	postCredentialUpdatedEvents(fmt.Sprintf("http://localhost:%d", common.GetServerPort()), provider, profileIds, method)
}

// postCredentialUpdatedEvents is best effort: the server often isn't running
// while credentials are configured, so failures are ignored.
func postCredentialUpdatedEvents(serverBaseURL, provider string, profileIds []string, method provider_auth.Method) {
	client := &http.Client{Timeout: 2 * time.Second}
	endpoint := serverBaseURL + "/api/v1/provider_auth/credential_updated"
	for _, profileId := range profileIds {
		body, err := json.Marshal(provider_auth.NewCredentialUpdatedEvent(provider, profileId, method))
		if err != nil {
			continue
		}
		resp, err := client.Post(endpoint, "application/json", bytes.NewReader(body))
		if err != nil {
			return
		}
		resp.Body.Close()
	}
}
