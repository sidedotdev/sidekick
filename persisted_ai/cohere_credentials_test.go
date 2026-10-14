package persisted_ai

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sidekick/common"
	"sidekick/secret_manager"
	"testing"
	"time"

	"github.com/adrg/xdg"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"
)

func TestGetRerankerUsesProfileCredentials(t *testing.T) {
	const childEnv = "SIDE_TEST_RERANKER_CREDENTIALS_CHILD"
	if os.Getenv(childEnv) != "1" {
		t.Parallel()

		// MockInit replaces the process-wide keyring, so it must not run
		// in the process that also runs live credential integration tests.
		ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestGetRerankerUsesProfileCredentials$", "-test.timeout=30s", "-test.v")
		cmd.Env = append(os.Environ(), childEnv+"=1")
		for _, providerName := range []string{"cohere", "company-ranking"} {
			model := common.ModelConfig{Provider: providerName}
			secretName := model.NormalizedProviderName() + "_API_KEY"
			for _, profileId := range []string{"default", "work", "personal"} {
				envName := secret_manager.ProfileEnvSecretName(profileId, secretName)
				key := ""
				if profileId != "personal" {
					key = profileId + "-key"
				}
				cmd.Env = append(cmd.Env, envName+"="+key, "SIDE_"+envName+"=")
			}
		}
		output, err := cmd.CombinedOutput()
		require.NoError(t, err, "isolated credential test failed:\n%s", output)
		return
	}

	originalHome, originalDirs := xdg.ConfigHome, xdg.ConfigDirs
	t.Cleanup(func() {
		xdg.ConfigHome, xdg.ConfigDirs = originalHome, originalDirs
	})
	xdg.ConfigHome, xdg.ConfigDirs = t.TempDir(), nil
	configDir := common.GetSidekickConfigDir()
	require.NoError(t, os.MkdirAll(configDir, 0700))
	configPath := filepath.Join(configDir, "config.yml")

	for _, providerName := range []string{"cohere", "company-ranking"} {
		t.Run(providerName, func(t *testing.T) {
			keyring.MockInit()
			model := common.ModelConfig{Provider: providerName}
			providers := []common.ModelProviderPublicConfig{{Name: providerName, Type: "cohere"}}
			secretName := model.NormalizedProviderName() + "_API_KEY"
			config := fmt.Sprintf(`profiles:
  - id: work
  - id: personal
providers:
  - name: %[1]s
    type: cohere
    key: default-key
  - name: %[1]s
    type: cohere
    key: work-key
    profiles: [work]
`, providerName)
			require.NoError(t, os.WriteFile(configPath, []byte(config), 0600))

			for _, profileId := range []string{"default", "work", "personal"} {
				if profileId == "personal" {
					continue
				}
				key := profileId + "-key"
				require.NoError(t, keyring.Set("sidekick", secret_manager.ProfileSecretName(profileId, secretName), key))
			}

			reranker, err := GetReranker(model, providers, nil)
			require.NoError(t, err)
			assert.Nil(t, reranker)

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
								reranker, err := GetReranker(model, providers, source)
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

			t.Run("ambiguous names are not missing keys", func(t *testing.T) {
				ambiguousConfig := config + fmt.Sprintf(`  - name: %s
    type: cohere
    key: other-work-key
    profiles: [work]
`, providerName)
				require.NoError(t, os.WriteFile(configPath, []byte(ambiguousConfig), 0600))
				reranker, err := GetReranker(model, providers, secret_manager.LocalConfigSecretManager{ProfileId: "work"})
				require.ErrorContains(t, err, "multiple providers found")
				assert.Nil(t, reranker)
			})
		})
	}
}
