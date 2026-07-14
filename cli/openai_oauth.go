package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"sidekick/openai_oauth"
	"strings"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/erikgeiser/promptkit/selection"
	"github.com/zalando/go-keyring"
	"golang.org/x/oauth2"
)

func generateRandomState() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("failed to generate random state: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func buildOpenAIAuthorizeURL(verifier, state, redirectURI string) string {
	challenge := oauth2.S256ChallengeFromVerifier(verifier)

	params := url.Values{}
	params.Set("response_type", "code")
	params.Set("client_id", openai_oauth.ClientID)
	params.Set("redirect_uri", redirectURI)
	params.Set("scope", openai_oauth.Scope)
	params.Set("code_challenge", challenge)
	params.Set("code_challenge_method", "S256")
	params.Set("state", state)
	params.Set("id_token_add_organizations", "true")
	params.Set("codex_cli_simplified_flow", "true")
	params.Set("originator", "codex_cli_rs")

	return openai_oauth.AuthorizeURL + "?" + params.Encode()
}

// parseOpenAIPastedInput extracts the authorization code and optional state
// from user-pasted input. Accepted formats:
//   - Full redirect URL with code & state query params
//   - "code#state" combined string
//   - Raw query string containing "code="
//   - Raw authorization code
func parseOpenAIPastedInput(input string) (code, state string, err error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return "", "", fmt.Errorf("empty input")
	}

	// Try as a full URL
	if u, parseErr := url.Parse(input); parseErr == nil && u.Scheme != "" && u.Host != "" {
		q := u.Query()
		if c := q.Get("code"); c != "" {
			return c, q.Get("state"), nil
		}
	}

	// Try as raw query string containing "code="
	if strings.Contains(input, "code=") {
		q, parseErr := url.ParseQuery(input)
		if parseErr == nil {
			if c := q.Get("code"); c != "" {
				return c, q.Get("state"), nil
			}
		}
	}

	// Try as "code#state"
	if parts := strings.SplitN(input, "#", 2); len(parts) == 2 && parts[0] != "" {
		return parts[0], parts[1], nil
	}

	// Raw code
	return input, "", nil
}

// exchangeOpenAICode exchanges an authorization code for tokens using the
// form-encoded POST required by OpenAI's token endpoint.
func exchangeOpenAICode(code, verifier, redirectURI string) (*openai_oauth.Credentials, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("client_id", openai_oauth.ClientID)
	form.Set("code", code)
	form.Set("code_verifier", verifier)
	form.Set("redirect_uri", redirectURI)

	req, err := http.NewRequest("POST", openai_oauth.TokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("failed to create token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := openai_oauth.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send token request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read token response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token exchange failed with status %d: %s", resp.StatusCode, string(body))
	}

	var tokenResp struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("failed to parse token response: %w", err)
	}

	if tokenResp.AccessToken == "" {
		return nil, fmt.Errorf("token response missing access_token")
	}
	if tokenResp.RefreshToken == "" {
		return nil, fmt.Errorf("token response missing refresh_token")
	}
	if tokenResp.ExpiresIn == 0 {
		return nil, fmt.Errorf("token response missing expires_in")
	}

	accountID, err := openai_oauth.ExtractAccountIDFromAccessToken(tokenResp.AccessToken)
	if err != nil {
		return nil, fmt.Errorf("failed to extract account_id from access token: %w", err)
	}

	return &openai_oauth.Credentials{
		AccessToken:  tokenResp.AccessToken,
		RefreshToken: tokenResp.RefreshToken,
		ExpiresAt:    time.Now().Unix() + tokenResp.ExpiresIn,
		AccountID:    accountID,
	}, nil
}

func handleOpenAIOAuthSubscription() error {
	existingCreds, err := keyring.Get(keyringService, openai_oauth.SecretName)
	if err != nil && err != keyring.ErrNotFound {
		return fmt.Errorf("error checking existing OAuth credentials: %w", err)
	}

	if existingCreds != "" {
		overwriteSelection := selection.New(
			"Existing OpenAI OAuth credentials found. What would you like to do?",
			[]string{"Keep existing credentials", "Overwrite with new credentials"},
		)
		choice, err := overwriteSelection.RunPrompt()
		if err != nil {
			return fmt.Errorf("selection failed: %w", err)
		}
		if choice == "Keep existing credentials" {
			fmt.Println("✔ Keeping existing OpenAI OAuth credentials.")
			return nil
		}
	}

	if isOpenAIOAuthHeadlessEnvironment() {
		fmt.Println("Headless environment detected; using OpenAI device authentication.")
		creds, err := handleOpenAIDeviceCodeOAuth()
		if err != nil {
			return err
		}
		return saveOpenAIOAuthCredentials(creds)
	}

	verifier := oauth2.GenerateVerifier()
	state, err := generateRandomState()
	if err != nil {
		return err
	}

	callbackServer, redirectURI, callbackResults, callbackServerErr := startOpenAIOAuthCallbackServer()
	if callbackServerErr != nil {
		fmt.Printf("Could not start the OpenAI OAuth callback server: %v\n", callbackServerErr)
		fmt.Println("Falling back to OpenAI device authentication.")
		creds, deviceErr := handleOpenAIDeviceCodeOAuth()
		if deviceErr != nil {
			return deviceErr
		}
		return saveOpenAIOAuthCredentials(creds)
	}
	defer callbackServer.Close()

	authURL := buildOpenAIAuthorizeURL(verifier, state, redirectURI)

	fmt.Println("\nOpening browser for OpenAI authentication...")
	fmt.Println("If the browser doesn't open, please visit this URL manually:")
	fmt.Println(authURL)
	fmt.Println()

	if err := openURL(authURL); err != nil {
		fmt.Printf("Could not open a browser automatically: %v\n", err)
		fmt.Println("Falling back to OpenAI device authentication.")
		if callbackServer != nil {
			callbackServer.Close()
		}
		creds, deviceErr := handleOpenAIDeviceCodeOAuth()
		if deviceErr != nil {
			return deviceErr
		}
		return saveOpenAIOAuthCredentials(creds)
	}

	callback, err := waitForOpenAIOAuthCallback(callbackResults)
	if err != nil {
		return err
	}
	if callback.err != nil {
		return callback.err
	}
	if callback.state == "" {
		return fmt.Errorf("OpenAI OAuth callback missing state; paste the full callback URL or code#state value")
	}
	if callback.state != state {
		return fmt.Errorf("state mismatch: received %q, expected %q", callback.state, state)
	}

	creds, err := exchangeOpenAICode(callback.code, verifier, redirectURI)
	if err != nil {
		return fmt.Errorf("failed to exchange authorization code: %w", err)
	}

	return saveOpenAIOAuthCredentials(creds)
}

func saveOpenAIOAuthCredentials(creds *openai_oauth.Credentials) error {
	credsJSON, err := json.Marshal(creds)
	if err != nil {
		return fmt.Errorf("failed to marshal OAuth credentials: %w", err)
	}

	if err := keyring.Set(keyringService, openai_oauth.SecretName, string(credsJSON)); err != nil {
		return fmt.Errorf("error storing OAuth credentials in keyring: %w", err)
	}

	fmt.Println("✔ OpenAI OAuth credentials saved.")
	return nil
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
func waitForOpenAIOAuthCallback(callbackResults <-chan openAIOAuthCallbackResult) (openAIOAuthCallbackResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
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
					Title("Complete login in your browser, or paste the callback URL or code#state value here").
					Value(&input),
			),
		).RunWithContext(ctx)
		manualResults <- manualInputResult{value: input, err: err}
	}()

	if callbackResults == nil {
		manual := <-manualResults
		if manual.err != nil {
			return openAIOAuthCallbackResult{}, fmt.Errorf("failed to get authorization input: %w", manual.err)
		}
		return openAIOAuthCallbackFromInput(manual.value)
	}

	select {
	case callback := <-callbackResults:
		cancel()
		<-manualResults
		return callback, nil
	case manual := <-manualResults:
		if manual.err != nil {
			return openAIOAuthCallbackResult{}, fmt.Errorf("failed to get authorization input: %w", manual.err)
		}
		return openAIOAuthCallbackFromInput(manual.value)
	case <-ctx.Done():
		return openAIOAuthCallbackResult{}, fmt.Errorf("timed out waiting for OpenAI OAuth callback")
	}
}
func openAIOAuthCallbackFromInput(input string) (openAIOAuthCallbackResult, error) {
	code, state, err := parseOpenAIPastedInput(input)
	if err != nil {
		return openAIOAuthCallbackResult{}, fmt.Errorf("failed to parse authorization input: %w", err)
	}
	return openAIOAuthCallbackResult{
		code:  code,
		state: strings.TrimSpace(state),
	}, nil
}
