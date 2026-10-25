package provider_auth

import (
	"sidekick/common"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMethodsForProvider(t *testing.T) {
	t.Parallel()

	tests := []struct {
		provider string
		want     []Method
	}{
		{provider: "anthropic", want: []Method{MethodOAuthSubscription, MethodOAuthCreateKey, MethodAPIKey}},
		{provider: " OpenAI ", want: []Method{MethodOAuthSubscription, MethodAPIKey}},
		{provider: "google", want: []Method{MethodAPIKey}},
		{provider: "acme", want: nil},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.provider, func(t *testing.T) {
			t.Parallel()
			var got []Method
			for _, info := range MethodsForProvider(tc.provider) {
				assert.NotEmpty(t, info.Label)
				got = append(got, info.Method)
			}
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestValidateMethod(t *testing.T) {
	t.Parallel()

	assert.NoError(t, ValidateMethod("anthropic", MethodOAuthCreateKey))
	assert.NoError(t, ValidateMethod("google", MethodAPIKey))
	assert.ErrorIs(t, ValidateMethod("openai", MethodOAuthCreateKey), ErrUnsupportedMethod)
	assert.ErrorIs(t, ValidateMethod("google", MethodOAuthSubscription), ErrUnsupportedMethod)
	assert.ErrorIs(t, ValidateMethod("acme", MethodAPIKey), ErrUnsupportedProvider)
}

func TestNewCredentialUpdatedEvent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		method       Method
		wantAuthType common.ProviderAuthType
	}{
		{method: MethodOAuthSubscription, wantAuthType: common.ProviderAuthTypeSubscription},
		{method: MethodOAuthCreateKey, wantAuthType: common.ProviderAuthTypeAPI},
		{method: MethodAPIKey, wantAuthType: common.ProviderAuthTypeAPI},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(string(tc.method), func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, CredentialUpdatedEvent{
				Provider:  "anthropic",
				ProfileId: common.DefaultProfileId,
				Method:    tc.method,
				AuthType:  tc.wantAuthType,
			}, NewCredentialUpdatedEvent("Anthropic", "", tc.method))
		})
	}
}
