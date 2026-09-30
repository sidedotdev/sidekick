package llm2

import (
	"testing"

	"sidekick/common"
	"sidekick/secret_manager"

	"github.com/stretchr/testify/require"
)

// A provisioned credential proxy makes integration tests runnable, but only
// SIDE_INTEGRATION_TEST opts in to running them.
func TestOpenAIIntegrationGating(t *testing.T) {
	proxied := OpenAIResponsesProvider{AuthType: common.ProviderAuthTypeSubscription}
	cases := []struct {
		name        string
		optIn       string
		provisioned bool
		wantRun     bool
	}{
		{name: "provisioned proxy with opt-in runs", optIn: "true", provisioned: true, wantRun: true},
		{name: "provisioned proxy without opt-in skips", optIn: "", provisioned: true, wantRun: false},
		{name: "unprovisioned without opt-in skips", optIn: "", provisioned: false, wantRun: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("SIDE_INTEGRATION_TEST", tc.optIn)
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
