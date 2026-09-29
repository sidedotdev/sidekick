package llm2

import (
	"testing"

	"sidekick/common"
	"sidekick/secret_manager"

	"github.com/stretchr/testify/require"
)

// A provisioned credential proxy is the host explicitly lending credentials
// for these tests, so it opts them in without SIDE_INTEGRATION_TEST.
func TestOpenAIIntegrationGating(t *testing.T) {
	proxied := OpenAIResponsesProvider{AuthType: common.ProviderAuthTypeSubscription}
	cases := []struct {
		name        string
		provisioned bool
		wantRun     bool
	}{
		{name: "provisioned proxy runs without opt-in", provisioned: true, wantRun: true},
		{name: "unprovisioned without opt-in skips", provisioned: false, wantRun: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("SIDE_INTEGRATION_TEST", "")
			original := IntegrationProviderAdapter
			t.Cleanup(func() { IntegrationProviderAdapter = original })
			IntegrationProviderAdapter = func(Provider, common.ProviderAuthType) (IntegrationEnvironment, bool) {
				if !tc.provisioned {
					return IntegrationEnvironment{}, false
				}
				return IntegrationEnvironment{Provider: proxied, Secrets: &secret_manager.EnvSecretManager{}}, true
			}

			var got Provider
			t.Run("gate", func(t *testing.T) {
				got, _ = requireOpenAIResponsesIntegration(t, OpenAIResponsesProvider{})
			})
			if tc.wantRun {
				require.Equal(t, Provider(proxied), got)
			} else {
				require.Nil(t, got)
			}
		})
	}
}