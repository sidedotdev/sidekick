package persisted_ai

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sidekick/common"
	"sidekick/secret_manager"
	"testing"

	"github.com/adrg/xdg"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"
)

func TestGetRerankerUsesProfileCredentials(t *testing.T) {
	keyring.MockInit()
	originalHome, originalDirs := xdg.ConfigHome, xdg.ConfigDirs
	t.Cleanup(func() {
		xdg.ConfigHome, xdg.ConfigDirs = originalHome, originalDirs
	})
	xdg.ConfigHome, xdg.ConfigDirs = t.TempDir(), nil
	configDir := common.GetSidekickConfigDir()
	require.NoError(t, os.MkdirAll(configDir, 0700))
	configPath := filepath.Join(configDir, "config.yml")
	config := `profiles:
  - id: work
  - id: personal
providers:
  - name: default-ranking
    type: cohere
    key: default-key
  - name: work-ranking
    type: cohere
    key: work-key
    profiles: [work]
`
	require.NoError(t, os.WriteFile(configPath, []byte(config), 0600))

	for _, profileId := range []string{"default", "work", "personal"} {
		envName := secret_manager.ProfileEnvSecretName(profileId, "COHERE_API_KEY")
		t.Setenv(envName, "")
		t.Setenv("SIDE_"+envName, "")
		if profileId == "personal" {
			continue
		}
		key := profileId + "-key"
		require.NoError(t, keyring.Set("sidekick", secret_manager.ProfileSecretName(profileId, "COHERE_API_KEY"), key))
		t.Setenv(envName, key)
	}

	t.Run("nil manager ignores configured credentials", func(t *testing.T) {
		reranker, err := GetReranker(nil)
		require.NoError(t, err)
		assert.Nil(t, reranker)
	})

	backends := []struct {
		name string
		new  func(string) secret_manager.SecretManager
	}{
		{"keyring", func(id string) secret_manager.SecretManager {
			return secret_manager.KeyringSecretManager{ProfileId: id}
		}},
		{"local config", func(id string) secret_manager.SecretManager {
			return secret_manager.LocalConfigSecretManager{ProfileId: id}
		}},
		{"environment", func(id string) secret_manager.SecretManager {
			return secret_manager.EnvSecretManager{ProfileId: id}
		}},
		{"composite", func(id string) secret_manager.SecretManager {
			return secret_manager.NewProfileSecretManager(id)
		}},
	}
	for _, backend := range backends {
		t.Run(backend.name, func(t *testing.T) {
			for _, profileId := range []string{"default", "work", "personal"} {
				t.Run(profileId, func(t *testing.T) {
					manager := backend.new(profileId)
					data, err := json.Marshal(secret_manager.SecretManagerContainer{SecretManager: manager})
					require.NoError(t, err)
					var decoded secret_manager.SecretManagerContainer
					require.NoError(t, json.Unmarshal(data, &decoded))

					for _, source := range []secret_manager.SecretManager{manager, decoded} {
						reranker, err := GetReranker(source)
						require.NoError(t, err)
						if profileId == "personal" {
							assert.Nil(t, reranker)
						} else {
							assert.NotNil(t, reranker)
						}
					}
				})
			}
		})
	}

	t.Run("ambiguous config is not treated as a missing key", func(t *testing.T) {
		ambiguousConfig := config + `  - name: other-work-ranking
    type: cohere
    key: other-work-key
    profiles: [work]
`
		require.NoError(t, os.WriteFile(configPath, []byte(ambiguousConfig), 0600))
		reranker, err := GetReranker(secret_manager.LocalConfigSecretManager{ProfileId: "work"})
		require.ErrorContains(t, err, "multiple providers found")
		assert.Nil(t, reranker)
	})
}
