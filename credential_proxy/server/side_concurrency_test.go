package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sidekick/common"
	"sidekick/openai_oauth"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type sharedOAuthSecrets struct {
	mu  sync.Mutex
	raw string
}

type scopedOAuthSecrets struct {
	*sharedOAuthSecrets
	profile string
}

func (s scopedOAuthSecrets) GetProfileId() string {
	return s.profile
}

func (s scopedOAuthSecrets) GetSecret(string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.raw, nil
}

func TestOpenAIRoutesCoordinateProfileRefresh(t *testing.T) {
	raw, err := json.Marshal(openai_oauth.Credentials{
		AccessToken: "expired", RefreshToken: "rotating", ExpiresAt: 1,
		AccountID: "test-account",
	})
	if err != nil {
		t.Fatal(err)
	}
	freshToken := "header." + base64.RawURLEncoding.EncodeToString(
		[]byte(`{"chatgpt_account_id":"test-account"}`)) + ".signature"
	response, err := json.Marshal(map[string]any{
		"access_token":  freshToken,
		"refresh_token": "rotated",
		"expires_in":    3600,
	})
	if err != nil {
		t.Fatal(err)
	}
	secrets := &sharedOAuthSecrets{raw: string(raw)}
	var refreshes atomic.Int32
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		refreshes.Add(1)
		entered <- struct{}{}
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(response)
	}))
	defer server.Close()
	oldEndpoint, oldClient, oldStore := openai_oauth.TokenEndpoint, openai_oauth.HTTPClient, openai_oauth.StoreForProfileFn
	t.Cleanup(func() {
		openai_oauth.TokenEndpoint = oldEndpoint
		openai_oauth.HTTPClient = oldClient
		openai_oauth.StoreForProfileFn = oldStore
	})
	openai_oauth.TokenEndpoint = server.URL
	openai_oauth.HTTPClient = server.Client()
	openai_oauth.StoreForProfileFn = func(_ string, creds *openai_oauth.Credentials) error {
		data, err := json.Marshal(creds)
		if err != nil {
			return err
		}
		secrets.mu.Lock()
		secrets.raw = string(data)
		secrets.mu.Unlock()
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	start := make(chan struct{})
	ready := make(chan struct{}, 2)
	results := make(chan error, 2)
	for _, profile := range []string{"", "DEFAULT"} {
		authorize := openAIAuthorizer(scopedOAuthSecrets{secrets, profile}, common.CredentialProxyAuthTypeSubscription)
		go func() {
			ready <- struct{}{}
			<-start
			headers, err := authorize(ctx)
			if err == nil && (headers.Get("Authorization") != "Bearer "+freshToken ||
				headers.Get("ChatGPT-Account-Id") != "test-account") {
				t.Error("refreshed authorization did not preserve token and account")
			}
			results <- err
		}()
	}
	<-ready
	<-ready
	close(start)
	select {
	case <-entered:
		// A second request arriving before release proves refresh overlap.
		// Serialized callers cannot enter until the first response is released.
		select {
		case <-entered:
		case <-time.After(time.Second):
		}
	case <-ctx.Done():
		t.Error("first refresh did not reach the fake endpoint")
	}
	close(release)
	for range 2 {
		if err := <-results; err != nil {
			t.Errorf("authorization failed: %v", err)
		}
	}
	if refreshes.Load() != 1 {
		t.Fatalf("expected one refresh across default-profile aliases, got %d", refreshes.Load())
	}
}

type blockingOAuthSecrets struct {
	entered chan struct{}
	release chan struct{}
	calls   atomic.Int32
	raw     string
}

func (s *blockingOAuthSecrets) GetProfileId() string {
	return "canceled-refresh-waiter"
}

func (s *blockingOAuthSecrets) GetSecret(string) (string, error) {
	if s.calls.Add(1) == 1 {
		close(s.entered)
		<-s.release
	}
	return s.raw, nil
}

func TestOpenAIRoutesCanceledRefreshWaiter(t *testing.T) {
	t.Parallel()

	raw, err := json.Marshal(openai_oauth.Credentials{
		AccessToken: "test-token", RefreshToken: "test-refresh",
		AccountID: "test-account", ExpiresAt: time.Now().Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	secrets := &blockingOAuthSecrets{
		entered: make(chan struct{}), release: make(chan struct{}), raw: string(raw),
	}
	first := openAIAuthorizer(secrets, common.CredentialProxyAuthTypeSubscription)
	second := openAIAuthorizer(secrets, common.CredentialProxyAuthTypeSubscription)
	firstResult := make(chan error, 1)
	go func() {
		_, err := first(context.Background())
		firstResult <- err
	}()
	<-secrets.entered

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	waiterStarted := make(chan struct{})
	waiterResult := make(chan error, 1)
	go func() {
		close(waiterStarted)
		_, err := second(ctx)
		waiterResult <- err
	}()
	<-waiterStarted
	cancel()

	returned := false
	select {
	case err := <-waiterResult:
		returned = true
		if !errors.Is(err, context.Canceled) {
			t.Errorf("expected canceled waiter, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Error("canceled waiter remained blocked behind credential loading")
	}
	close(secrets.release)
	if err := <-firstResult; err != nil {
		t.Errorf("first authorization failed: %v", err)
	}
	if !returned {
		<-waiterResult
	}
	if got := secrets.calls.Load(); got != 1 {
		t.Errorf("canceled waiter resolved credentials: %d reads", got)
	}
}
