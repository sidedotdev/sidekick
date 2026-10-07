package main

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"sidekick/openai_oauth"
	"sidekick/provider_auth"
)

func handleOpenAIOAuthSubscription() error {
	profileIds, err := selectCredentialProfiles("OpenAI")
	if err != nil {
		return err
	}

	targetProfileIds, err := resolveTargetProfiles(profileIds, openai_oauth.SecretName, "OpenAI OAuth credentials")
	if err != nil {
		return err
	}
	if len(targetProfileIds) == 0 {
		fmt.Println("✔ Keeping existing OpenAI OAuth credentials.")
		return nil
	}

	if err := performOpenAIOAuthFlow(targetProfileIds); err != nil {
		return err
	}

	fmt.Println("✔ OpenAI OAuth credentials saved.")
	notifyCredentialsUpdated(provider_auth.ProviderOpenAI, targetProfileIds, provider_auth.MethodOAuthSubscription)
	return nil
}

func performOpenAIOAuthFlow(profileIds []string) error {
	if isOpenAIOAuthHeadlessEnvironment() {
		fmt.Println("Headless environment detected; using OpenAI device authentication.")
		return performOpenAIDeviceCodeOAuth(profileIds)
	}

	session, err := provider_auth.StartOAuthSessionForProfiles(provider_auth.ProviderOpenAI, profileIds, provider_auth.MethodOAuthSubscription)
	if err != nil {
		return err
	}
	defer session.Cancel()

	if callbackErr := session.CallbackError(); callbackErr != nil {
		session.Cancel()
		fmt.Printf("Could not start the OpenAI OAuth callback server: %v\n", callbackErr)
		fmt.Println("Falling back to OpenAI device authentication.")
		return performOpenAIDeviceCodeOAuth(profileIds)
	}

	fmt.Println("\nOpening browser for OpenAI authentication...")
	fmt.Println("If the browser doesn't open, please visit this URL manually:")
	fmt.Println(session.AuthURL)
	fmt.Println()

	if err := openURL(session.AuthURL); err != nil {
		fmt.Printf("Could not open a browser automatically: %v\n", err)
		fmt.Println("Falling back to OpenAI device authentication.")
		session.Cancel()
		return performOpenAIDeviceCodeOAuth(profileIds)
	}

	return awaitOAuthSession(session, "Complete login in your browser, or paste the callback URL or code#state value here")
}

func performOpenAIDeviceCodeOAuth(profileIds []string) error {
	ctx, cancel := context.WithTimeout(context.Background(), provider_auth.OpenAIDeviceCodeTimeout)
	defer cancel()

	device, err := provider_auth.StartOpenAIDeviceAuth(ctx)
	if err != nil {
		return err
	}

	fmt.Printf("\nOpen %s and enter this code:\n\n%s\n\n", provider_auth.OpenAIDeviceVerificationURI, device.UserCode)

	creds, err := provider_auth.CompleteOpenAIDeviceAuth(ctx, device)
	if err != nil {
		return err
	}
	return provider_auth.SaveOpenAIOAuthCredentials(creds, profileIds)
}

func isOpenAIOAuthHeadlessEnvironment() bool {
	if os.Getenv("SIDE_OPENAI_DEVICE_AUTH") != "" {
		return true
	}
	if os.Getenv("SSH_CONNECTION") != "" || os.Getenv("SSH_TTY") != "" {
		return true
	}
	return runtime.GOOS == "linux" &&
		!isWSL() &&
		os.Getenv("DISPLAY") == "" &&
		os.Getenv("WAYLAND_DISPLAY") == ""
}
