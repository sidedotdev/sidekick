package llm2

import (
	"os"
	"sidekick/secret_manager"
	"strings"
	"testing"
)

func requireIntegrationAPIKey(t *testing.T, names ...string) secret_manager.SecretManager {
	t.Helper()

	if os.Getenv("SIDE_INTEGRATION_TEST") != "true" {
		t.Skip("Skipping integration test; SIDE_INTEGRATION_TEST not set")
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
