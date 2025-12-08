package main

import (
	"context"
	"fmt"
	"sidekick/llm"

	"github.com/erikgeiser/promptkit/selection"
	"github.com/erikgeiser/promptkit/textinput"
	"github.com/urfave/cli/v3"
	"github.com/zalando/go-keyring"
)

const (
	AnthropicOAuthSecretName = "ANTHROPIC_OAUTH"
	keyringService           = "sidekick"
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
	providerSelection := selection.New("Select your LLM API provider", []string{"OpenAI", "Google", "Anthropic"})
	provider, err := providerSelection.RunPrompt()
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
	return handleManualAPIKeyAuth("OpenAI", llm.OpenaiApiKeySecretName)
}

func handleGoogleAuth() error {
	return handleManualAPIKeyAuth("Google", llm.GoogleApiKeySecretName)
}

func handleAnthropicAuth() error {
	methodSelection := selection.New("Select authentication method", []string{
		"Claude Pro/Max (OAuth subscription)",
		"Create an API Key (OAuth)",
		"Manually enter API Key",
	})
	method, err := methodSelection.RunPrompt()
	if err != nil {
		return fmt.Errorf("authentication method selection failed: %w", err)
	}

	switch method {
	case "Claude Pro/Max (OAuth subscription)":
		// OAuth flow will be implemented in step 2
		fmt.Println("OAuth subscription flow not yet implemented.")
		return nil
	case "Create an API Key (OAuth)":
		// OAuth flow will be implemented in step 2
		fmt.Println("OAuth API key creation flow not yet implemented.")
		return nil
	case "Manually enter API Key":
		return handleManualAPIKeyAuth("Anthropic", llm.AnthropicApiKeySecretName)
	default:
		return fmt.Errorf("unknown authentication method: %s", method)
	}
}

func handleManualAPIKeyAuth(providerName, secretName string) error {
	existingKey, err := keyring.Get(keyringService, secretName)
	if err != nil && err != keyring.ErrNotFound {
		return fmt.Errorf("error checking existing API key: %w", err)
	}

	if existingKey != "" {
		overwriteSelection := selection.New(
			fmt.Sprintf("An existing %s API key was found. What would you like to do?", providerName),
			[]string{"Keep existing key", "Overwrite with new key"},
		)
		choice, err := overwriteSelection.RunPrompt()
		if err != nil {
			return fmt.Errorf("selection failed: %w", err)
		}
		if choice == "Keep existing key" {
			fmt.Printf("✔ Keeping existing %s API key.\n", providerName)
			return nil
		}
	}

	apiKeyInput := textinput.New(fmt.Sprintf("Enter your %s API Key: ", providerName))
	apiKeyInput.Hidden = true

	apiKey, err := apiKeyInput.RunPrompt()
	if err != nil {
		return fmt.Errorf("failed to get %s API Key: %w", providerName, err)
	}

	if apiKey == "" {
		return fmt.Errorf("%s API Key not provided", providerName)
	}

	err = keyring.Set(keyringService, secretName, apiKey)
	if err != nil {
		return fmt.Errorf("error storing API key in keyring: %w", err)
	}

	fmt.Printf("✔ %s API Key saved to keyring.\n", providerName)
	return nil
}
