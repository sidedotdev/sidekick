package main

import (
	"errors"
	"sidekick/common"
	"sidekick/provider_auth"
	"sidekick/secret_manager"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"
)

func TestCohereAuthProfileCredentials(t *testing.T) {
	tests := []struct {
		name          string
		defaultOnly   bool
		selected      []string
		existing      map[string]string
		overwrite     bool
		apiKey        string
		promptErr     error
		storageErr    bool
		wantStored    map[string]string
		wantNotified  []string
		wantPrompt    bool
		wantOverwrite []string
		wantErr       string
	}{
		{
			name:         "default profile without a selection prompt",
			defaultOnly:  true,
			apiKey:       "new-key",
			wantStored:   map[string]string{"default": "new-key"},
			wantNotified: []string{"default"},
			wantPrompt:   true,
		},
		{
			name:         "only selected named profile is changed",
			selected:     []string{"work"},
			existing:     map[string]string{"default": "default-key", "personal": "personal-key"},
			apiKey:       "new-key",
			wantStored:   map[string]string{"default": "default-key", "work": "new-key", "personal": "personal-key"},
			wantNotified: []string{"work"},
			wantPrompt:   true,
		},
		{
			name:         "multiple selected profiles",
			selected:     []string{"default", "work"},
			apiKey:       "new-key",
			wantStored:   map[string]string{"default": "new-key", "work": "new-key"},
			wantNotified: []string{"default", "work"},
			wantPrompt:   true,
		},
		{
			name:          "confirmed overwrite",
			selected:      []string{"work"},
			existing:      map[string]string{"work": "old-key"},
			overwrite:     true,
			apiKey:        "new-key",
			wantStored:    map[string]string{"work": "new-key"},
			wantNotified:  []string{"work"},
			wantPrompt:    true,
			wantOverwrite: []string{"work"},
		},
		{
			name:          "declined overwrite skips key prompt",
			selected:      []string{"work"},
			existing:      map[string]string{"work": "old-key"},
			wantStored:    map[string]string{"work": "old-key"},
			wantOverwrite: []string{"work"},
		},
		{
			name:          "declined overwrite still fills missing profiles",
			selected:      []string{"default", "work"},
			existing:      map[string]string{"default": "old-key"},
			apiKey:        "new-key",
			wantStored:    map[string]string{"default": "old-key", "work": "new-key"},
			wantNotified:  []string{"work"},
			wantPrompt:    true,
			wantOverwrite: []string{"default"},
		},
		{
			name:       "storage failure identifies the profile",
			selected:   []string{"work"},
			apiKey:     "new-key",
			storageErr: true,
			wantPrompt: true,
			wantErr:    `error storing API key in keyring: profile "work": keyring unavailable`,
		},
		{
			name:       "empty key is not stored",
			selected:   []string{"work"},
			wantPrompt: true,
			wantErr:    "Cohere API Key not provided",
		},
		{
			name:       "key prompt failure is propagated",
			selected:   []string{"work"},
			promptErr:  errors.New("prompt cancelled"),
			wantPrompt: true,
			wantErr:    "failed to get Cohere API Key: prompt cancelled",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			keyring.MockInit()
			profiles := []common.Profile{{Id: common.DefaultProfileId, Name: common.DefaultProfileName}}
			if !tt.defaultOnly {
				profiles = append(profiles, common.Profile{Id: "work", Name: "Work"}, common.Profile{Id: "personal", Name: "Personal"})
			}
			_, notifications := stubCredentialPromptsWithNotifications(t, profiles, tt.selected, nil)
			provider_auth.KeyringGet = keyring.Get
			provider_auth.KeyringSet = keyring.Set
			for profileId, key := range tt.existing {
				require.NoError(t, keyring.Set("sidekick", secret_manager.ProfileSecretName(profileId, "COHERE_API_KEY"), key))
			}
			storageErr := errors.New("keyring unavailable")
			if tt.storageErr {
				provider_auth.KeyringSet = func(service, key, value string) error {
					assert.Equal(t, "sidekick", service)
					assert.Equal(t, "WORK-COHERE_API_KEY", key)
					return storageErr
				}
			}

			originalSelect, originalPrompt, originalConfirm := selectOption, promptAPIKey, confirmOverwriteExisting
			t.Cleanup(func() {
				selectOption, promptAPIKey, confirmOverwriteExisting = originalSelect, originalPrompt, originalConfirm
			})
			selectOption = func(title string, options []string) (string, error) {
				assert.Contains(t, options, "Cohere")
				return "Cohere", nil
			}
			prompted := false
			promptAPIKey = func(providerName string) (string, error) {
				assert.Equal(t, "Cohere", providerName)
				prompted = true
				return tt.apiKey, tt.promptErr
			}
			var overwriteProfiles []string
			confirmOverwriteExisting = func(subject string, profileIds []string) (bool, error) {
				assert.Equal(t, "Cohere API key", subject)
				overwriteProfiles = profileIds
				return tt.overwrite, nil
			}

			err := handleAuthCommand()
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				if tt.storageErr {
					assert.ErrorIs(t, err, storageErr)
				}
				if tt.promptErr != nil {
					assert.ErrorIs(t, err, tt.promptErr)
				}
			} else {
				require.NoError(t, err)
			}
			assert.Equal(t, tt.wantPrompt, prompted)
			assert.Equal(t, tt.wantOverwrite, overwriteProfiles)

			for _, profileId := range []string{"default", "work", "personal"} {
				key, err := (secret_manager.KeyringSecretManager{ProfileId: profileId}).GetSecret("COHERE_API_KEY")
				expected, exists := tt.wantStored[profileId]
				if exists {
					require.NoError(t, err)
					assert.Equal(t, expected, key)
				} else {
					assert.ErrorIs(t, err, secret_manager.ErrSecretNotFound)
				}
			}
			if len(tt.wantNotified) == 0 {
				assert.Empty(t, *notifications)
			} else {
				assert.Equal(t, []credentialsUpdatedCall{{
					provider:   "cohere",
					profileIds: tt.wantNotified,
					method:     provider_auth.MethodAPIKey,
				}}, *notifications)
			}
		})
	}
}

func TestInitExcludesCohereFromChatSetup(t *testing.T) {
	tests := []struct {
		name       string
		providers  []common.ModelProviderConfig
		addNew     bool
		wantChoice bool
	}{
		{name: "no configured providers"},
		{
			name:      "only Cohere is configured",
			providers: []common.ModelProviderConfig{{Name: "ranking-service", Type: "cohere"}},
		},
		{
			name: "configured chat choices omit Cohere",
			providers: []common.ModelProviderConfig{
				{Name: "ranking-service", Type: "cohere"},
				{Name: "chat-service", Type: "openai_compatible"},
			},
			wantChoice: true,
		},
		{
			name: "adding a provider uses only chat authentication choices",
			providers: []common.ModelProviderConfig{
				{Name: "ranking-service", Type: "cohere"},
				{Name: "chat-service", Type: "openai_compatible"},
			},
			addNew:     true,
			wantChoice: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stubCredentialPrompts(t, []common.Profile{{Id: common.DefaultProfileId, Name: common.DefaultProfileName}}, nil, map[string]string{
				"COHERE_API_KEY": "ranking-key",
			})
			originalSelect := selectOption
			t.Cleanup(func() { selectOption = originalSelect })
			cancelled := errors.New("selection cancelled")
			choiceSeen, authSeen := false, false
			selectOption = func(title string, options []string) (string, error) {
				switch title {
				case "Select your LLM provider":
					choiceSeen = true
					assert.Equal(t, []string{"chat-service", "Add new provider"}, options)
					if tt.addNew {
						return "Add new provider", nil
					}
					return "chat-service", nil
				case "Select your API provider":
					authSeen = true
					assert.Equal(t, []string{"OpenAI", "Google", "Anthropic"}, options)
					return "", cancelled
				default:
					t.Fatalf("unexpected prompt: %s", title)
					return "", cancelled
				}
			}

			selected, err := selectLLMProvider(common.LocalConfig{Providers: tt.providers})
			assert.Equal(t, tt.wantChoice, choiceSeen)
			if tt.wantChoice && !tt.addNew {
				require.NoError(t, err)
				assert.Equal(t, "chat-service", selected)
				assert.False(t, authSeen)
			} else {
				require.ErrorIs(t, err, cancelled)
				assert.Empty(t, selected)
				assert.True(t, authSeen)
			}
		})
	}
}
