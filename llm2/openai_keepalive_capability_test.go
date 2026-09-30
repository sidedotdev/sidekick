package llm2

import (
	"sidekick/common"
	"sidekick/secret_manager"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOpenAIResponsesExpectsKeepalives(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name     string
		model    string
		baseURL  string
		authType common.ProviderAuthType
		want     bool
	}{
		{"luna", "gpt-5.6-luna", "", common.ProviderAuthTypeAPI, true},
		{"terra", "gpt-5.6-terra", "", common.ProviderAuthTypeAPI, true},
		{"sol", "gpt-5.6-sol", "", common.ProviderAuthTypeAPI, true},
		{"base model", "gpt-5.6", "", common.ProviderAuthTypeAPI, true},
		{"astra assumed", "gpt-6-astra", "", common.ProviderAuthTypeAPI, true},
		{"gpt6 base", "gpt-6", "", common.ProviderAuthTypeAPI, true},
		{"future minor", "gpt-6.5", "", common.ProviderAuthTypeAPI, true},
		{"future major", "gpt-7", "", common.ProviderAuthTypeAPI, true},
		{"double digit major", "gpt-10-pro", "", common.ProviderAuthTypeAPI, true},
		{"unrelated name", "other-7", "", common.ProviderAuthTypeAPI, false},
		{"invalid major", "gpt-6foo", "", common.ProviderAuthTypeAPI, false},
		{"astra custom endpoint", "gpt-6-astra", "https://example.com/v1", common.ProviderAuthTypeAPI, false},
		{"astra subscription", "gpt-6-astra", "", common.ProviderAuthTypeSubscription, false},
		{"different version", "gpt-5.60", "", common.ProviderAuthTypeAPI, false},
		{"custom endpoint", "gpt-5.6-sol", "https://example.com/v1", common.ProviderAuthTypeAPI, false},
		{"explicit direct endpoint", "gpt-5.6-sol", "https://api.openai.com/v1/", common.ProviderAuthTypeAPI, true},
		{"subscription", "gpt-5.6-sol", "", common.ProviderAuthTypeSubscription, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			provider := OpenAIResponsesProvider{BaseURL: tt.baseURL, AuthType: tt.authType}
			request := StreamRequest{
				Options:       Options{ModelConfig: common.ModelConfig{Provider: "openai", Model: tt.model}},
				SecretManager: &secret_manager.MockSecretManager{},
			}
			require.Equal(t, tt.want, provider.ExpectsKeepalives(request))
		})
	}
}
