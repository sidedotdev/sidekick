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
