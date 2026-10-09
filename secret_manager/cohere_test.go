package secret_manager

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sidekick/common"
	"testing"

	"github.com/adrg/xdg"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"
)

func writeCohereLocalConfig(t *testing.T, providers string) {
	t.Helper()

	originalHome, originalDirs := xdg.ConfigHome, xdg.ConfigDirs
	t.Cleanup(func() {
		xdg.ConfigHome, xdg.ConfigDirs = originalHome, originalDirs
	})
	xdg.ConfigHome, xdg.ConfigDirs = t.TempDir(), nil

	configDir := common.GetSidekickConfigDir()
	require.NoError(t, os.MkdirAll(configDir, 0700))
	config := "profiles:\n  - id: work\n  - id: personal\n" + providers
	require.NoError(t, os.WriteFile(filepath.Join(configDir, "config.yml"), []byte(config), 0600))
}

func TestLocalConfigCohereCredentials(t *testing.T) {
	tests := []struct {
		name       string
		providers  string
		profileId  string
		secretName string
		wantKey    string
		wantErr    string
		notFound   bool
	}{
		{
			name: "type lookup with arbitrary name",
			providers: `providers:
  - name: ranking-service
    type: cohere
    key: default-key
`,
			wantKey: "default-key",
		},
		{
			name: "type and name match the same provider only once",
			providers: `providers:
  - name: cohere
    type: cohere
    key: default-key
`,
			wantKey: "default-key",
		},
		{
			name: "legacy name lookup",
			providers: `providers:
  - name: cohere
    type: openai_compatible
    key: legacy-key
`,
			wantKey: "legacy-key",
		},
		{
			name: "custom name lookup remains available",
			providers: `providers:
  - name: ranking
    type: cohere
    key: named-key
`,
			secretName: "RANKING_API_KEY",
			wantKey:    "named-key",
		},
		{
			name: "type lookup filters other profiles before matching",
			providers: `providers:
  - name: default-ranking
    type: cohere
    key: default-key
  - name: work-ranking
    type: cohere
    key: work-key
    profiles: [work]
  - name: personal-ranking
    type: cohere
    key: personal-key
    profiles: [personal]
`,
			profileId: "work",
			wantKey:   "work-key",
		},
		{
			name: "shared credential includes selected profile",
			providers: `providers:
  - name: shared-ranking
    type: cohere
    key: shared-key
    profiles: [default, work]
`,
			profileId: "work",
			wantKey:   "shared-key",
		},
		{
			name: "named profile cannot use default credentials",
			providers: `providers:
  - name: ranking
    type: cohere
    key: default-key
`,
			profileId: "work",
			notFound:  true,
		},
		{
			name: "default cannot use named profile credentials",
			providers: `providers:
  - name: ranking
    type: cohere
    key: work-key
    profiles: [work]
`,
			notFound: true,
		},
		{
			name: "legacy name lookup respects profiles",
			providers: `providers:
  - name: cohere
    type: openai_compatible
    key: default-key
`,
			profileId: "work",
			notFound:  true,
		},
		{
			name: "empty membership excludes all profiles",
			providers: `providers:
  - name: ranking
    type: cohere
    key: unused-key
    profiles: []
`,
			notFound: true,
		},
		{
			name: "multiple type matches are ambiguous",
			providers: `providers:
  - name: first-ranking
    type: cohere
    key: first-key
  - name: second-ranking
    type: cohere
    key: second-key
`,
			wantErr: "multiple providers found",
		},
		{
			name: "type and legacy name matches are ambiguous",
			providers: `providers:
  - name: ranking
    type: cohere
    key: typed-key
  - name: cohere
    type: openai_compatible
    key: legacy-key
`,
			wantErr: "multiple providers found",
		},
		{
			name: "multiple legacy name matches are ambiguous",
			providers: `providers:
  - name: cohere
    type: openai_compatible
    key: first-key
  - name: cohere
    type: openai_compatible
    key: second-key
`,
			wantErr: "multiple providers found",
		},
		{
			name:      "missing credential",
			providers: "providers: []\n",
			notFound:  true,
		},
		{
			name:      "config errors propagate",
			providers: "providers: [\n",
			wantErr:   "error loading local config",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			writeCohereLocalConfig(t, tt.providers)
			secretName := tt.secretName
			if secretName == "" {
				secretName = "COHERE_API_KEY"
			}

			key, err := (LocalConfigSecretManager{ProfileId: tt.profileId}).GetSecret(secretName)
			switch {
			case tt.notFound:
				require.ErrorIs(t, err, ErrSecretNotFound)
				assert.Empty(t, key)
			case tt.wantErr != "":
				require.ErrorContains(t, err, tt.wantErr)
				assert.NotErrorIs(t, err, ErrSecretNotFound)
				assert.Empty(t, key)
			default:
				require.NoError(t, err)
				assert.Equal(t, tt.wantKey, key)
			}
		})
	}
}

func TestCohereSecretManagersProfileIsolation(t *testing.T) {
	keyring.MockInit()
	writeCohereLocalConfig(t, `providers:
  - name: default-ranking
    type: cohere
    key: config-default
  - name: work-ranking
    type: cohere
    key: config-work
    profiles: [work]
  - name: personal-ranking
    type: cohere
    key: config-personal
    profiles: [personal]
`)

	for _, profileId := range []string{"default", "work", "personal", "missing"} {
		envName := ProfileEnvSecretName(profileId, "COHERE_API_KEY")
		t.Setenv(envName, "")
		t.Setenv("SIDE_"+envName, "")
		if profileId == "missing" {
			continue
		}
		require.NoError(t, keyring.Set("sidekick", ProfileSecretName(profileId, "COHERE_API_KEY"), "keyring-"+profileId))
		t.Setenv(envName, "unpreferred-"+profileId)
		t.Setenv("SIDE_"+envName, "env-"+profileId)
	}

	backends := []struct {
		name      string
		new       func(string) SecretManager
		keyPrefix string
	}{
		{
			name:      "keyring",
			new:       func(id string) SecretManager { return KeyringSecretManager{ProfileId: id} },
			keyPrefix: "keyring-",
		},
		{
			name:      "local config",
			new:       func(id string) SecretManager { return LocalConfigSecretManager{ProfileId: id} },
			keyPrefix: "config-",
		},
		{
			name:      "environment",
			new:       func(id string) SecretManager { return EnvSecretManager{ProfileId: id} },
			keyPrefix: "env-",
		},
		{
			name:      "composite",
			new:       func(id string) SecretManager { return NewProfileSecretManager(id) },
			keyPrefix: "keyring-",
		},
	}
	for _, backend := range backends {
		t.Run(backend.name, func(t *testing.T) {
			for _, profileId := range []string{"", "default", "work", "personal", "missing"} {
				t.Run("profile="+profileId, func(t *testing.T) {
					manager := backend.new(profileId)
					data, err := json.Marshal(SecretManagerContainer{SecretManager: manager})
					require.NoError(t, err)
					var decoded SecretManagerContainer
					require.NoError(t, json.Unmarshal(data, &decoded))

					for _, source := range []SecretManager{manager, decoded} {
						key, err := source.GetSecret("COHERE_API_KEY")
						if profileId == "missing" {
							require.ErrorIs(t, err, ErrSecretNotFound)
							assert.Empty(t, key)
							continue
						}
						require.NoError(t, err)
						effectiveProfile := profileId
						if effectiveProfile == "" {
							effectiveProfile = common.DefaultProfileId
						}
						assert.Equal(t, backend.keyPrefix+effectiveProfile, key)
					}
				})
			}
		})
	}
}

func TestCohereCompositeCredentialPrecedence(t *testing.T) {
	tests := []struct {
		name      string
		keyring   string
		config    string
		preferred string
		env       string
		wantKey   string
	}{
		{
			name:    "keyring precedes config and environment",
			keyring: "keyring-key", config: "config-key", preferred: "preferred-key", env: "env-key",
			wantKey: "keyring-key",
		},
		{
			name:   "config precedes environment",
			config: "config-key", preferred: "preferred-key", env: "env-key",
			wantKey: "config-key",
		},
		{
			name:      "preferred environment",
			preferred: "preferred-key", env: "env-key",
			wantKey: "preferred-key",
		},
		{
			name:    "unprefixed environment fallback",
			env:     "env-key",
			wantKey: "env-key",
		},
		{
			name: "no fallback to other profiles",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			keyring.MockInit()
			require.NoError(t, keyring.Set("sidekick", "COHERE_API_KEY", "default-keyring-key"))
			require.NoError(t, keyring.Set("sidekick", "PERSONAL-COHERE_API_KEY", "personal-keyring-key"))
			t.Setenv("COHERE_API_KEY", "default-env-key")
			t.Setenv("SIDE_COHERE_API_KEY", "default-preferred-key")
			t.Setenv("PERSONAL_COHERE_API_KEY", "personal-env-key")
			t.Setenv("SIDE_PERSONAL_COHERE_API_KEY", "personal-preferred-key")
			t.Setenv("WORK_COHERE_API_KEY", tt.env)
			t.Setenv("SIDE_WORK_COHERE_API_KEY", tt.preferred)
			providers := `providers:
  - name: default-ranking
    type: cohere
    key: default-config-key
  - name: personal-ranking
    type: cohere
    key: personal-config-key
    profiles: [personal]
`
			if tt.config != "" {
				providers += "  - name: work-ranking\n    type: cohere\n    key: " + tt.config + "\n    profiles: [work]\n"
			}
			writeCohereLocalConfig(t, providers)
			if tt.keyring != "" {
				require.NoError(t, keyring.Set("sidekick", "WORK-COHERE_API_KEY", tt.keyring))
			}

			data, err := json.Marshal(SecretManagerContainer{SecretManager: NewProfileSecretManager("work")})
			require.NoError(t, err)
			var decoded SecretManagerContainer
			require.NoError(t, json.Unmarshal(data, &decoded))
			key, err := decoded.GetSecret("COHERE_API_KEY")
			if tt.wantKey == "" {
				require.ErrorIs(t, err, ErrSecretNotFound)
				assert.Empty(t, key)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.wantKey, key)
			}
		})
	}
}
