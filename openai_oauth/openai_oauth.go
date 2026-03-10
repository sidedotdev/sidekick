package openai_oauth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/zalando/go-keyring"
)

const (
	SecretName   = "OPENAI_OAUTH"
	ClientID     = "app_EMoamEEZ73f0CkXaXp7hrann"
	AuthorizeURL = "https://auth.openai.com/oauth/authorize"
	TokenURL     = "https://auth.openai.com/oauth/token"
	Scope        = "openid profile email offline_access"

	keyringService = "sidekick"
)

type Credentials struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresAt    int64  `json:"expires_at"`
	AccountID    string `json:"account_id"`
}

// StoreFn persists refreshed credentials. Override in tests to avoid OS keyring.
var StoreFn = func(creds *Credentials) error {
	data, err := json.Marshal(creds)
	if err != nil {
		return fmt.Errorf("failed to marshal credentials: %w", err)
	}
	return keyring.Set(keyringService, SecretName, string(data))
}

// TokenEndpoint and HTTPClient are overridable for testing.
var (
	TokenEndpoint = TokenURL
	HTTPClient    = &http.Client{Timeout: 30 * time.Second}
)

func ParseCredentials(raw string) (*Credentials, error) {
	var creds Credentials
	if err := json.Unmarshal([]byte(raw), &creds); err != nil {
		return nil, fmt.Errorf("failed to parse OpenAI OAuth credentials: %w", err)
	}
	if creds.AccessToken == "" {
		return nil, fmt.Errorf("OpenAI OAuth credentials missing access_token")
	}
	if creds.RefreshToken == "" {
		return nil, fmt.Errorf("OpenAI OAuth credentials missing refresh_token")
	}
	if creds.ExpiresAt == 0 {
		return nil, fmt.Errorf("OpenAI OAuth credentials missing expires_at")
	}
	if creds.AccountID == "" {
		return nil, fmt.Errorf("OpenAI OAuth credentials missing account_id")
	}
	return &creds, nil
}

// ExtractAccountIDFromAccessToken decodes the JWT payload and extracts
// chatgpt_account_id from the "https://api.openai.com/auth" claim. It also
// tolerates alternate claim shapes: a top-level "chatgpt_account_id" claim,
// or an "organizations" array with an "id" field under the auth claim.
func ExtractAccountIDFromAccessToken(accessToken string) (string, error) {
	parts := strings.Split(accessToken, ".")
	if len(parts) < 2 {
		return "", fmt.Errorf("invalid JWT: expected at least 2 parts, got %d", len(parts))
	}

	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", fmt.Errorf("failed to decode JWT payload: %w", err)
	}

	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		return "", fmt.Errorf("failed to parse JWT claims: %w", err)
	}

	// Primary path: https://api.openai.com/auth -> chatgpt_account_id
	if authClaim, ok := claims["https://api.openai.com/auth"]; ok {
		if authMap, ok := authClaim.(map[string]any); ok {
			if id, ok := extractStringField(authMap, "chatgpt_account_id"); ok {
				return id, nil
			}
		}
	}

	// Alternate: top-level chatgpt_account_id
	if id, ok := extractStringField(claims, "chatgpt_account_id"); ok {
		return id, nil
	}

	// Alternate: organizations array under the auth claim
	if authClaim, ok := claims["https://api.openai.com/auth"]; ok {
		if authMap, ok := authClaim.(map[string]any); ok {
			if orgs, ok := authMap["organizations"].([]any); ok && len(orgs) > 0 {
				if org, ok := orgs[0].(map[string]any); ok {
					if id, ok := extractStringField(org, "id"); ok {
						return id, nil
					}
				}
			}
		}
	}

	return "", fmt.Errorf("could not extract account_id from JWT: no recognized claim shape found")
}

func extractStringField(m map[string]any, key string) (string, bool) {
	v, ok := m[key]
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	if !ok || s == "" {
		return "", false
	}
	return s, true
}

// ExchangeRefreshToken exchanges a refresh token for new credentials.
func ExchangeRefreshToken(ctx context.Context, refreshToken string) (*Credentials, error) {
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", refreshToken)
	form.Set("client_id", ClientID)

	req, err := http.NewRequestWithContext(ctx, "POST", TokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("failed to create refresh request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send refresh request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read refresh response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token refresh failed with status %d: %s", resp.StatusCode, string(body))
	}

	var tokenResp struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("failed to parse refresh response: %w", err)
	}

	if tokenResp.AccessToken == "" {
		return nil, fmt.Errorf("refresh response missing access_token")
	}
	if tokenResp.RefreshToken == "" {
		return nil, fmt.Errorf("refresh response missing refresh_token")
	}
	if tokenResp.ExpiresIn == 0 {
		return nil, fmt.Errorf("refresh response missing expires_in")
	}

	accountID, err := ExtractAccountIDFromAccessToken(tokenResp.AccessToken)
	if err != nil {
		return nil, fmt.Errorf("failed to extract account_id from refreshed token: %w", err)
	}

	return &Credentials{
		AccessToken:  tokenResp.AccessToken,
		RefreshToken: tokenResp.RefreshToken,
		ExpiresAt:    time.Now().Unix() + tokenResp.ExpiresIn,
		AccountID:    accountID,
	}, nil
}

// GetAndMaybeRefresh loads OPENAI_OAUTH credentials and refreshes them if
// expired or expiring within 5 minutes. Returns (nil, false, nil) only when
// no OAuth credentials are configured (secret not found).
func GetAndMaybeRefresh(ctx context.Context, secretManager interface{ GetSecret(string) (string, error) }) (*Credentials, bool, error) {
	raw, err := secretManager.GetSecret(SecretName)
	if err != nil {
		if strings.Contains(err.Error(), "not found") || strings.Contains(err.Error(), "not exist") {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("failed to get OpenAI OAuth secret: %w", err)
	}
	if raw == "" {
		return nil, false, nil
	}

	creds, err := ParseCredentials(raw)
	if err != nil {
		return nil, false, err
	}

	// Refresh proactively if token expires within 5 minutes
	if time.Now().Unix() > creds.ExpiresAt-300 {
		log.Info().Msg("OpenAI OAuth token expiring soon, refreshing proactively")
		newCreds, refreshErr := ExchangeRefreshToken(ctx, creds.RefreshToken)
		if refreshErr != nil {
			return nil, false, fmt.Errorf("failed to refresh OpenAI OAuth token: %w", refreshErr)
		}
		if storeErr := StoreFn(newCreds); storeErr != nil {
			return nil, false, fmt.Errorf("failed to store refreshed OpenAI OAuth credentials: %w", storeErr)
		}
		return newCreds, true, nil
	}

	return creds, true, nil
}
