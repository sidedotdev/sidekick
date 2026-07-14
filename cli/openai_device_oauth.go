package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"sidekick/openai_oauth"
)

const (
	openAIDeviceVerificationURI = "https://auth.openai.com/codex/device"
	openAIDeviceRedirectURI     = "https://auth.openai.com/deviceauth/callback"
	openAIDeviceCodeTimeout     = 15 * time.Minute
)

var (
	openAIDeviceUserCodeEndpoint = "https://auth.openai.com/api/accounts/deviceauth/usercode"
	openAIDeviceTokenEndpoint    = "https://auth.openai.com/api/accounts/deviceauth/token"
)

type openAIDeviceAuthInfo struct {
	DeviceAuthID    string
	UserCode        string
	IntervalSeconds float64
}

type openAIDeviceToken struct {
	AuthorizationCode string
	CodeVerifier      string
}

func startOpenAIDeviceAuth(ctx context.Context) (*openAIDeviceAuthInfo, error) {
	body, err := json.Marshal(map[string]string{"client_id": openai_oauth.ClientID})
	if err != nil {
		return nil, fmt.Errorf("failed to encode OpenAI device authorization request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, openAIDeviceUserCodeEndpoint, strings.NewReader(string(body)))
	if err != nil {
		return nil, fmt.Errorf("failed to create OpenAI device authorization request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := openai_oauth.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to request OpenAI device code: %w", err)
	}
	defer resp.Body.Close()

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read OpenAI device code response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("OpenAI device code request failed with status %d: %s", resp.StatusCode, responseBody)
	}

	var response struct {
		DeviceAuthID string          `json:"device_auth_id"`
		UserCode     string          `json:"user_code"`
		Interval     json.RawMessage `json:"interval"`
	}
	if err := json.Unmarshal(responseBody, &response); err != nil {
		return nil, fmt.Errorf("failed to parse OpenAI device code response: %w", err)
	}

	var interval float64
	if err := json.Unmarshal(response.Interval, &interval); err != nil {
		var intervalString string
		if stringErr := json.Unmarshal(response.Interval, &intervalString); stringErr != nil {
			return nil, fmt.Errorf("OpenAI device code response has invalid interval")
		}
		if _, scanErr := fmt.Sscan(intervalString, &interval); scanErr != nil {
			return nil, fmt.Errorf("OpenAI device code response has invalid interval")
		}
	}
	if response.DeviceAuthID == "" || response.UserCode == "" || interval < 0 {
		return nil, fmt.Errorf("OpenAI device code response missing required fields")
	}

	return &openAIDeviceAuthInfo{
		DeviceAuthID:    response.DeviceAuthID,
		UserCode:        response.UserCode,
		IntervalSeconds: interval,
	}, nil
}

func pollOpenAIDeviceAuth(ctx context.Context, device *openAIDeviceAuthInfo) (*openAIDeviceToken, error) {
	interval := time.Duration(device.IntervalSeconds * float64(time.Second))

	for {
		if interval > 0 {
			timer := time.NewTimer(interval)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, fmt.Errorf("OpenAI device authorization timed out: %w", ctx.Err())
			case <-timer.C:
			}
		}

		body, err := json.Marshal(map[string]string{
			"device_auth_id": device.DeviceAuthID,
			"user_code":      device.UserCode,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to encode OpenAI device token request: %w", err)
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, openAIDeviceTokenEndpoint, strings.NewReader(string(body)))
		if err != nil {
			return nil, fmt.Errorf("failed to create OpenAI device token request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := openai_oauth.HTTPClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("failed to poll OpenAI device authorization: %w", err)
		}
		responseBody, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("failed to read OpenAI device token response: %w", readErr)
		}

		if resp.StatusCode == http.StatusOK {
			var token struct {
				AuthorizationCode string `json:"authorization_code"`
				CodeVerifier      string `json:"code_verifier"`
			}
			if err := json.Unmarshal(responseBody, &token); err != nil {
				return nil, fmt.Errorf("failed to parse OpenAI device token response: %w", err)
			}
			if token.AuthorizationCode == "" || token.CodeVerifier == "" {
				return nil, fmt.Errorf("OpenAI device token response missing required fields")
			}
			return &openAIDeviceToken{
				AuthorizationCode: token.AuthorizationCode,
				CodeVerifier:      token.CodeVerifier,
			}, nil
		}

		var errorResponse struct {
			Error any `json:"error"`
		}
		_ = json.Unmarshal(responseBody, &errorResponse)
		errorCode := ""
		switch value := errorResponse.Error.(type) {
		case string:
			errorCode = value
		case map[string]any:
			errorCode, _ = value["code"].(string)
		}

		switch errorCode {
		case "deviceauth_authorization_pending", "authorization_pending":
			continue
		case "slow_down":
			interval += 5 * time.Second
			continue
		}

		return nil, fmt.Errorf("OpenAI device authorization failed with status %d: %s", resp.StatusCode, responseBody)
	}
}

func handleOpenAIDeviceCodeOAuth() (*openai_oauth.Credentials, error) {
	ctx, cancel := context.WithTimeout(context.Background(), openAIDeviceCodeTimeout)
	defer cancel()

	device, err := startOpenAIDeviceAuth(ctx)
	if err != nil {
		return nil, err
	}

	fmt.Printf("\nOpen %s and enter this code:\n\n%s\n\n", openAIDeviceVerificationURI, device.UserCode)

	token, err := pollOpenAIDeviceAuth(ctx, device)
	if err != nil {
		return nil, err
	}

	return exchangeOpenAICode(token.AuthorizationCode, token.CodeVerifier, openAIDeviceRedirectURI)
}
