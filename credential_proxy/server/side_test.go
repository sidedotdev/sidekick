package server

import (
	"context"
	"encoding/json"
	"sidekick/common"
	"sidekick/openai_oauth"
	"sidekick/secret_manager"
	"testing"
	"time"
)

type testSecrets map[string]string

func (s testSecrets) GetSecret(name string) (string, error) {
	if value, ok := s[name]; ok {
		return value, nil
	}
	return "", secret_manager.ErrSecretNotFound
}

func TestOpenAIAuthorizer(t *testing.T) {
	t.Parallel()
	raw, err := json.Marshal(openai_oauth.Credentials{
		AccessToken:  "oauth-secret",
		RefreshToken: "refresh-secret",
		AccountID:    "host-account",
		ExpiresAt:    time.Now().Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	secrets := testSecrets{
		"OPENAI_API_KEY":        "api-secret",
		openai_oauth.SecretName: string(raw),
	}
	for _, tc := range []struct {
		mode, token, account string
	}{
		{common.CredentialProxyAuthTypeAPI, "api-secret", ""},
		{common.CredentialProxyAuthTypeSubscription, "oauth-secret", "host-account"},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			t.Parallel()
			authorize := openAIAuthorizer(secrets, tc.mode)
			if authorize == nil {
				t.Fatal("mode has no authorizer")
			}
			headers, err := authorize(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if headers.Get("Authorization") != "Bearer "+tc.token || headers.Get("ChatGPT-Account-Id") != tc.account {
				t.Fatal("incorrect host credentials")
			}
		})
	}
	if openAIAuthorizer(secrets, common.CredentialProxyAuthTypeAny) != nil {
		t.Fatal("any must be resolved before an authorizer is built")
	}
}

func TestResolveSideModeMatchesInferenceFallback(t *testing.T) {
	t.Parallel()

	withOAuth := testSecrets{openai_oauth.SecretName: "{}"}
	for _, tc := range []struct {
		name     string
		authType string
		secrets  testSecrets
		want     string
	}{
		{"any with oauth", "", withOAuth, common.CredentialProxyAuthTypeSubscription},
		{"any without oauth", "any", testSecrets{}, common.CredentialProxyAuthTypeAPI},
		{"explicit api ignores oauth", "api", withOAuth, common.CredentialProxyAuthTypeAPI},
		{"explicit subscription without oauth still binds", "subscription", testSecrets{}, common.CredentialProxyAuthTypeSubscription},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			side := &common.CredentialProxySideAuthConfig{Provider: "openai", AuthType: tc.authType}
			mode, err := resolveSideMode(side, tc.secrets)
			if err != nil || mode != tc.want {
				t.Fatalf("resolveSideMode() = %q, %v; want %q", mode, err, tc.want)
			}
		})
	}
}
