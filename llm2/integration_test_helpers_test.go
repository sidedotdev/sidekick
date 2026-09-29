package llm2

import (
	"os"
	"sidekick/common"
	"sidekick/secret_manager"
	"strings"
	"testing"
)

func requireIntegrationAPIKey(t *testing.T, names ...string) secret_manager.SecretManager {
	t.Helper()

	if os.Getenv("SIDE_INTEGRATION_TEST") != "true" {
		t.Skip("Skipping integration test; SIDE_INTEGRATION_TEST not set")
	}

	// TODO: instead of skipping, use some TBD mechanism to run this test on
	// the host, where API keys are available.
	if common.IsActiveEnvNonLocal() {
		t.Skip("Skipping integration test; LLM API keys are unavailable in non-local sidekick environments")
	}

	secretManager := secret_manager.NewCompositeSecretManager([]secret_manager.SecretManager{
		&secret_manager.EnvSecretManager{},
		&secret_manager.KeyringSecretManager{},
		&secret_manager.LocalConfigSecretManager{},
	})

	if !hasAnyIntegrationSecret(secretManager, names...) {
		t.Fatalf("SIDE_INTEGRATION_TEST=true requires one of: %s", strings.Join(names, ", "))
	}

	return secretManager
}

// IntegrationEnvironment is how an integration test must run in the
// environment it finds itself in: with an adapted provider and the credentials
// that belong to it, or skipped or failed when that environment is configured
// but cannot serve the request.
type IntegrationEnvironment struct {
	Provider   Provider
	Secrets    secret_manager.SecretManager
	SkipReason string
	Err        error
}

// IntegrationProviderAdapter adapts a provider, and the credentials it runs
// with, to the environment integration tests run in. A sandbox provisioned
// with a host credential proxy routes requests through it instead of reading
// local secrets, which is how these tests exercise real providers where no
// credentials exist. The adapter imports llm2, so the external test package
// installs it here; it reports false when the environment offers nothing and
// the test should use local credentials.
var IntegrationProviderAdapter func(provider Provider, authType common.ProviderAuthType) (IntegrationEnvironment, bool)

// requireOpenAIResponsesIntegration gates a built-in OpenAI Responses
// integration test and returns the provider and secret manager to run it with.
func requireOpenAIResponsesIntegration(t *testing.T, provider OpenAIResponsesProvider) (Provider, secret_manager.SecretManager) {
	t.Helper()
	return requireOpenAIIntegrationProvider(t, provider, provider.AuthType)
}

// requireOpenAIChatIntegration is the chat completions counterpart, which only
// ever authenticates with an API key.
func requireOpenAIChatIntegration(t *testing.T, provider OpenAIProvider) (Provider, secret_manager.SecretManager) {
	t.Helper()
	return requireOpenAIIntegrationProvider(t, provider, common.ProviderAuthTypeAPI)
}

// A provisioned credential proxy opts the test in by itself: the host is
// explicitly lending credentials, so no SIDE_INTEGRATION_TEST is needed.
func requireOpenAIIntegrationProvider(t *testing.T, provider Provider, authType common.ProviderAuthType) (Provider, secret_manager.SecretManager) {
	t.Helper()

	if IntegrationProviderAdapter != nil {
		if environment, adapted := IntegrationProviderAdapter(provider, authType); adapted {
			if environment.Err != nil {
				t.Fatal(environment.Err)
			}
			if environment.SkipReason != "" {
				t.Skip(environment.SkipReason)
			}
			return environment.Provider, environment.Secrets
		}
	}
	return provider, requireIntegrationAPIKey(t, openAIIntegrationSecretNames(authType)...)
}

func openAIIntegrationSecretNames(authType common.ProviderAuthType) []string {
	switch common.NormalizeProviderAuthType(string(authType)) {
	case common.ProviderAuthTypeAPI:
		return []string{"OPENAI_API_KEY"}
	case common.ProviderAuthTypeSubscription:
		return []string{"OPENAI_OAUTH"}
	default:
		return []string{"OPENAI_OAUTH", "OPENAI_API_KEY"}
	}
}

func hasAnyIntegrationSecret(secretManager secret_manager.SecretManager, names ...string) bool {
	for _, name := range names {
		if _, err := secretManager.GetSecret(name); err == nil {
			return true
		}
	}
	return false
}

// requireAWSCredentialsForIntegration gates Bedrock integration tests. Unlike
// other providers, AWS auth is resolved by the SDK's default credential chain
// rather than via secret_manager, so we just ensure SIDE_INTEGRATION_TEST is
// set and return a fallback profile name when no other AWS credential signal
// is present in the environment. Callers should pass the returned profile to
// BedrockProvider.Profile; returning a value (rather than calling t.Setenv)
// keeps this helper compatible with t.Parallel().
func requireAWSCredentialsForIntegration(t *testing.T) string {
	t.Helper()

	if os.Getenv("SIDE_INTEGRATION_TEST") != "true" {
		t.Skip("Skipping integration test; SIDE_INTEGRATION_TEST not set")
	}

	// TODO: instead of skipping, use some TBD mechanism to run this test on
	// the host, where AWS credentials are available.
	if common.IsActiveEnvNonLocal() {
		t.Skip("Skipping integration test; AWS credentials are unavailable in non-local sidekick environments")
	}

	if os.Getenv("AWS_PROFILE") == "" &&
		os.Getenv("AWS_ACCESS_KEY_ID") == "" &&
		os.Getenv("AWS_WEB_IDENTITY_TOKEN_FILE") == "" {
		return "personal"
	}
	return ""
}
