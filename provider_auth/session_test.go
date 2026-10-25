package provider_auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sidekick/llm"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeAnthropicOAuthServer struct {
	mu                 sync.Mutex
	tokenRequest       map[string]string
	createKeyAuthToken string
	tokenStatus        int
}

func (f *fakeAnthropicOAuthServer) lastTokenRequest() map[string]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.tokenRequest
}

// stubAnthropicOAuth points token exchange and key creation at a local fake
// and makes the loopback callback listen on an ephemeral port.
func stubAnthropicOAuth(t *testing.T, tokenStatus int) *fakeAnthropicOAuthServer {
	t.Helper()

	fake := &fakeAnthropicOAuthServer{tokenStatus: tokenStatus}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fake.mu.Lock()
		defer fake.mu.Unlock()
		switch r.URL.Path {
		case "/token":
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			fake.tokenRequest = body
			if fake.tokenStatus != http.StatusOK {
				http.Error(w, `{"error":"invalid_grant"}`, fake.tokenStatus)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token":  "access-token",
				"refresh_token": "refresh-token",
				"expires_in":    3600,
			})
		case "/create_api_key":
			fake.createKeyAuthToken = r.Header.Get("Authorization")
			_ = json.NewEncoder(w).Encode(map[string]string{"api_key": "created-key"})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	origToken, origClaudeAIToken, origCreateKey, origListen := anthropicTokenEndpoint, anthropicClaudeAITokenEndpoint, anthropicCreateKeyURL, anthropicOAuthCallbackListenAddress
	t.Cleanup(func() {
		anthropicTokenEndpoint, anthropicClaudeAITokenEndpoint, anthropicCreateKeyURL, anthropicOAuthCallbackListenAddress = origToken, origClaudeAIToken, origCreateKey, origListen
	})
	anthropicTokenEndpoint = server.URL + "/token"
	anthropicClaudeAITokenEndpoint = server.URL + "/token"
	anthropicCreateKeyURL = server.URL + "/create_api_key"
	anthropicOAuthCallbackListenAddress = "127.0.0.1:0"
	return fake
}

func authURLParams(t *testing.T, session *OAuthSession) url.Values {
	t.Helper()
	parsed, err := url.Parse(session.AuthURL)
	require.NoError(t, err)
	return parsed.Query()
}

func waitForSession(t *testing.T, session *OAuthSession) (OAuthStatus, error) {
	t.Helper()
	select {
	case <-session.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for OAuth session to finish")
	}
	return session.Status()
}

func TestOAuthSessionAnthropicSubscriptionViaCallback(t *testing.T) {
	store := stubKeyring(t, nil)
	fake := stubAnthropicOAuth(t, http.StatusOK)

	session, err := StartOAuthSession("anthropic", "work", MethodOAuthSubscription)
	require.NoError(t, err)
	defer session.Cancel()
	require.NoError(t, session.CallbackError())

	params := authURLParams(t, session)
	redirectURI := params.Get("redirect_uri")
	state := params.Get("state")
	status, _ := session.Status()
	assert.Equal(t, OAuthStatusPending, status)

	resp, err := http.Get(redirectURI + "?code=the-code&state=" + url.QueryEscape(state))
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	status, err = waitForSession(t, session)
	require.NoError(t, err)
	assert.Equal(t, OAuthStatusSucceeded, status)

	assert.Equal(t, "the-code", fake.lastTokenRequest()["code"])
	assert.Equal(t, redirectURI, fake.lastTokenRequest()["redirect_uri"])

	var creds AnthropicOAuthCredentials
	require.NoError(t, json.Unmarshal([]byte(store["WORK-"+llm.AnthropicOAuthSecretName]), &creds))
	assert.Equal(t, "access-token", creds.AccessToken)
	assert.Equal(t, "refresh-token", creds.RefreshToken)
	assert.Greater(t, creds.ExpiresAt, time.Now().Unix())
}

func TestOAuthSessionAnthropicCreateKeyViaManualCode(t *testing.T) {
	store := stubKeyring(t, nil)
	fake := stubAnthropicOAuth(t, http.StatusOK)

	session, err := StartOAuthSession("anthropic", "", MethodOAuthCreateKey)
	require.NoError(t, err)
	defer session.Cancel()

	params := authURLParams(t, session)
	assert.Equal(t, AnthropicManualRedirectURI, params.Get("redirect_uri"))
	state := params.Get("state")

	assert.Error(t, session.SubmitManualCode("the-code#wrong-state"))
	status, _ := session.Status()
	assert.Equal(t, OAuthStatusPending, status, "invalid input must leave the session open for another attempt")

	require.NoError(t, session.SubmitManualCode("the-code#"+state))
	status, err = waitForSession(t, session)
	require.NoError(t, err)
	assert.Equal(t, OAuthStatusSucceeded, status)

	assert.Equal(t, "Bearer access-token", fake.createKeyAuthToken)
	assert.Equal(t, map[string]string{llm.AnthropicApiKeySecretName: "created-key"}, store)
	assert.ErrorIs(t, session.SubmitManualCode("another-code"), ErrOAuthSessionFinished)
}

func TestOAuthSessionTokenExchangeFailure(t *testing.T) {
	store := stubKeyring(t, nil)
	stubAnthropicOAuth(t, http.StatusBadRequest)

	session, err := StartOAuthSession("anthropic", "", MethodOAuthCreateKey)
	require.NoError(t, err)
	defer session.Cancel()

	require.NoError(t, session.SubmitManualCode("the-code"))
	status, err := waitForSession(t, session)
	assert.Equal(t, OAuthStatusFailed, status)
	assert.ErrorContains(t, err, "failed to exchange code for tokens")
	assert.Empty(t, store)
}

func TestOAuthSessionCancel(t *testing.T) {
	stubKeyring(t, nil)
	stubAnthropicOAuth(t, http.StatusOK)

	session, err := StartOAuthSession("anthropic", "", MethodOAuthSubscription)
	require.NoError(t, err)

	session.Cancel()
	status, err := waitForSession(t, session)
	assert.Equal(t, OAuthStatusFailed, status)
	assert.ErrorIs(t, err, ErrOAuthSessionCanceled)
}

func TestStartOAuthSessionRejectsUnsupportedMethods(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		provider string
		method   Method
		wantErr  error
	}{
		{name: "unknown provider", provider: "acme", method: MethodOAuthSubscription, wantErr: ErrUnsupportedProvider},
		{name: "google has no OAuth", provider: "google", method: MethodOAuthSubscription, wantErr: ErrUnsupportedMethod},
		{name: "openai has no create key", provider: "openai", method: MethodOAuthCreateKey, wantErr: ErrUnsupportedMethod},
		{name: "api key is not OAuth", provider: "anthropic", method: MethodAPIKey, wantErr: ErrUnsupportedMethod},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			session, err := StartOAuthSession(tc.provider, "", tc.method)
			assert.Nil(t, session)
			assert.ErrorIs(t, err, tc.wantErr)
		})
	}
}
