package llm2

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sidekick/common"
	"sidekick/llm"
	"sidekick/secret_manager"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/zalando/go-keyring"
)

// GetAnthropicOAuthCredentials loads credentials for modern streaming and
// tool-name mapping. Refresh failures require reauthentication only when the
// token endpoint rejects the credentials.
func GetAnthropicOAuthCredentials(secretManager interface{ GetSecret(string) (string, error) }) (*llm.OAuthCredentials, bool, error) {
	oauthJSON, err := secretManager.GetSecret(llm.AnthropicOAuthSecretName)
	if err != nil {
		return nil, false, nil
	}

	profileId := secret_manager.ProfileIdOf(secretManager)
	invalidCredentials := func(cause error) error {
		return common.NewProviderAuthError(common.ProviderAuthErrorDetails{
			Provider:  "anthropic",
			ProfileId: profileId,
			AuthType:  common.ProviderAuthTypeSubscription,
			Reason:    common.ProviderAuthErrorReasonInvalidOAuth,
		}, cause)
	}

	var creds llm.OAuthCredentials
	if err := json.Unmarshal([]byte(oauthJSON), &creds); err != nil {
		return nil, false, invalidCredentials(fmt.Errorf("failed to parse OAuth credentials: %w", err))
	}
	if creds.AccessToken == "" {
		return nil, false, invalidCredentials(fmt.Errorf("OAuth credentials missing access token"))
	}

	if creds.ExpiresAt > 0 && time.Now().Unix() > creds.ExpiresAt-300 {
		log.Info().Msg("OAuth token expiring soon, refreshing proactively")
		newCreds, err := refreshAnthropicOAuthToken(creds.RefreshToken)
		if err != nil {
			err = fmt.Errorf("failed to refresh OAuth token: %w", err)
			var refreshErr *common.OAuthRefreshError
			if errors.As(err, &refreshErr) && refreshErr.InvalidCredentials() {
				return nil, false, invalidCredentials(err)
			}
			return nil, false, err
		}
		if storeErr := storeAnthropicOAuthCredentialsForProfile(profileId, newCreds); storeErr != nil {
			log.Warn().Err(storeErr).Msg("Failed to store refreshed OAuth credentials")
		}
		return newCreds, true, nil
	}

	return &creds, true, nil
}

func refreshAnthropicOAuthToken(refreshToken string) (*llm.OAuthCredentials, error) {
	reqBody := map[string]string{
		"grant_type":    "refresh_token",
		"refresh_token": refreshToken,
		"client_id":     "9d1c250a-e61b-44d9-88ed-5944d1962f5e",
	}
	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal refresh request: %w", err)
	}
	req, err := http.NewRequest(http.MethodPost, "https://console.anthropic.com/v1/oauth/token", bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create refresh request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send refresh request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read refresh response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, &common.OAuthRefreshError{StatusCode: resp.StatusCode, Body: string(body)}
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

	var expiresAt int64
	if tokenResp.ExpiresIn > 0 {
		expiresAt = time.Now().Unix() + tokenResp.ExpiresIn
	}
	return &llm.OAuthCredentials{
		AccessToken:  tokenResp.AccessToken,
		RefreshToken: tokenResp.RefreshToken,
		ExpiresAt:    expiresAt,
	}, nil
}

func storeAnthropicOAuthCredentialsForProfile(profileId string, creds *llm.OAuthCredentials) error {
	credsJSON, err := json.Marshal(creds)
	if err != nil {
		return fmt.Errorf("failed to marshal credentials: %w", err)
	}
	secretName := secret_manager.ProfileSecretName(profileId, llm.AnthropicOAuthSecretName)
	return keyring.Set("sidekick", secretName, string(credsJSON))
}
