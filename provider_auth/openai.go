package provider_auth

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sidekick/openai_oauth"
	"strings"
	"time"

	"golang.org/x/oauth2"
)

func GenerateRandomState() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("failed to generate random state: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func BuildOpenAIAuthorizeURL(verifier, state, redirectURI string) string {
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

// ParseOpenAIPastedInput extracts the authorization code and optional state
// from user-pasted input. Accepted formats:
//   - Full redirect URL with code & state query params
//   - "code#state" combined string
//   - Raw query string containing "code="
//   - Raw authorization code
func ParseOpenAIPastedInput(input string) (code, state string, err error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return "", "", fmt.Errorf("empty input")
	}

	if u, parseErr := url.Parse(input); parseErr == nil && u.Scheme != "" && u.Host != "" {
		q := u.Query()
		if c := q.Get("code"); c != "" {
			return c, q.Get("state"), nil
		}
	}

	if strings.Contains(input, "code=") {
		q, parseErr := url.ParseQuery(input)
		if parseErr == nil {
			if c := q.Get("code"); c != "" {
				return c, q.Get("state"), nil
			}
		}
	}

	if parts := strings.SplitN(input, "#", 2); len(parts) == 2 && parts[0] != "" {
		return parts[0], parts[1], nil
	}

	return input, "", nil
}

func openAICallbackFromInput(input string) (callbackResult, error) {
	code, state, err := ParseOpenAIPastedInput(input)
	if err != nil {
		return callbackResult{}, fmt.Errorf("failed to parse authorization input: %w", err)
	}
	return callbackResult{
		code:  code,
		state: strings.TrimSpace(state),
	}, nil
}

func validateOpenAIState(received, expected string) error {
	if received == "" {
		return fmt.Errorf("OpenAI OAuth callback missing state; paste the full callback URL or code#state value")
	}
	if received != expected {
		return fmt.Errorf("state mismatch: received %q, expected %q", received, expected)
	}
	return nil
}

// ExchangeOpenAICode exchanges an authorization code for tokens using the
// form-encoded POST required by OpenAI's token endpoint.
func ExchangeOpenAICode(code, verifier, redirectURI string) (*openai_oauth.Credentials, error) {
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
