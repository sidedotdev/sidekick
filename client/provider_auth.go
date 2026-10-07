package client

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

const (
	ProviderOAuthStatusPending   = "pending"
	ProviderOAuthStatusSucceeded = "succeeded"
	ProviderOAuthStatusFailed    = "failed"
)

type StartProviderOAuthRequest struct {
	Provider  string `json:"provider"`
	ProfileId string `json:"profileId"`
	Method    string `json:"method"`
}

type StartProviderOAuthResponse struct {
	SessionId string `json:"sessionId"`
	AuthUrl   string `json:"authUrl"`
}

type ProviderOAuthStatus struct {
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

type SaveProviderAPIKeyRequest struct {
	Provider  string `json:"provider"`
	ProfileId string `json:"profileId"`
	ApiKey    string `json:"apiKey"`
}

// StartProviderOAuth begins a server-run OAuth login and returns the URL the
// user must open to authorize it.
func (c *clientImpl) StartProviderOAuth(req StartProviderOAuthRequest) (StartProviderOAuthResponse, error) {
	var response StartProviderOAuthResponse
	err := c.doJSON(http.MethodPost, "/api/v1/provider_auth/oauth/start", req, &response)
	return response, err
}

func (c *clientImpl) GetProviderOAuthStatus(sessionID string) (ProviderOAuthStatus, error) {
	var status ProviderOAuthStatus
	err := c.doJSON(http.MethodGet, "/api/v1/provider_auth/oauth/"+url.PathEscape(sessionID), nil, &status)
	return status, err
}

// SubmitProviderOAuthCode hands a pasted authorization code or callback URL
// to an OAuth login whose browser redirect couldn't reach the server.
func (c *clientImpl) SubmitProviderOAuthCode(sessionID, input string) error {
	body := map[string]string{"input": input}
	return c.doJSON(http.MethodPost, "/api/v1/provider_auth/oauth/"+url.PathEscape(sessionID)+"/code", body, nil)
}

func (c *clientImpl) ProviderAPIKeyExists(provider, profileID string) (bool, error) {
	query := url.Values{"provider": {provider}, "profileId": {profileID}}
	var response struct {
		Exists bool `json:"exists"`
	}
	err := c.doJSON(http.MethodGet, "/api/v1/provider_auth/api_key?"+query.Encode(), nil, &response)
	return response.Exists, err
}

func (c *clientImpl) SaveProviderAPIKey(req SaveProviderAPIKeyRequest) error {
	return c.doJSON(http.MethodPost, "/api/v1/provider_auth/api_key", req, nil)
}

// doJSON surfaces the server's error message when present, since provider auth
// errors are shown to the user inline.
func (c *clientImpl) doJSON(method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		requestBody, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("failed to marshal request: %w", err)
		}
		reader = bytes.NewReader(requestBody)
	}

	httpReq, err := http.NewRequest(method, c.BaseURL+path, reader)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	if body != nil {
		httpReq.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("request to %s failed: %w", path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		var apiErr struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(bodyBytes, &apiErr) == nil && apiErr.Error != "" {
			return errors.New(apiErr.Error)
		}
		return fmt.Errorf("request to %s failed: status %d, body: %s", path, resp.StatusCode, string(bodyBytes))
	}

	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("failed to decode response from %s: %w", path, err)
	}
	return nil
}
