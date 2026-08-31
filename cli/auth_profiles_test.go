package main

import (
	"encoding/json"
	"fmt"
	"io"
	"sidekick/common"
	"sidekick/llm"
	"sidekick/openai_oauth"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubCredentialPrompts wires deterministic stand-ins for the interactive
// prompts and keyring access used by the auth flows, returning the map of
// secrets written during the test.
func stubCredentialPrompts(t *testing.T, profiles []common.Profile, selectedProfileIds []string, existingSecrets map[string]string) map[string]string {
	t.Helper()

	origLoad := loadDeclaredProfiles
	origSelect := promptProfileSelection
	origGet := keyringGet
	origSet := keyringSet
	t.Cleanup(func() {
		loadDeclaredProfiles = origLoad
		promptProfileSelection = origSelect
		keyringGet = origGet
		keyringSet = origSet
	})

	loadDeclaredProfiles = func() ([]common.Profile, error) {
		return profiles, nil
	}
	promptProfileSelection = func(credentialName string, prompted []common.Profile) ([]string, error) {
		if selectedProfileIds == nil {
			return nil, fmt.Errorf("unexpected profile prompt for %s", credentialName)
		}
		return selectedProfileIds, nil
	}
	keyringGet = newMockKeyringGet(existingSecrets)

	written := map[string]string{}
	keyringSet = func(service, key, value string) error {
		written[key] = value
		return nil
	}
	return written
}

// stubDefaultProfileOnly keeps profile-dependent lookups from reading the
// developer's real local config.
func stubDefaultProfileOnly(t *testing.T) {
	t.Helper()

	orig := loadDeclaredProfiles
	t.Cleanup(func() { loadDeclaredProfiles = orig })
	loadDeclaredProfiles = func() ([]common.Profile, error) {
		return []common.Profile{{Id: common.DefaultProfileId, Name: common.DefaultProfileName}}, nil
	}
}

// submitProfileSelectionPrompt runs the real profile prompt, feeding it the
// given keystrokes, and returns the profiles it yields.
func submitProfileSelectionPrompt(t *testing.T, profiles []common.Profile, keystrokes string) ([]string, error) {
	t.Helper()

	var selected []string
	err := profileSelectionForm("Anthropic", profiles, &selected).
		WithInput(strings.NewReader(keystrokes)).
		WithOutput(io.Discard).
		WithTimeout(5 * time.Second).
		Run()
	if err != nil {
		return nil, err
	}
	return selected, nil
}

func TestProfileSelectionPromptAcceptsDefaultProfileOnSubmit(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		profiles []common.Profile
		expected []string
	}{
		{
			name: "renamed default profile is selected without toggling",
			profiles: []common.Profile{
				{Id: common.DefaultProfileId, Name: "Personal"},
				{Id: "work", Name: "Work"},
			},
			expected: []string{common.DefaultProfileId},
		},
		{
			name: "default profile declared with different casing is selected",
			profiles: []common.Profile{
				{Id: "Default", Name: "Personal"},
				{Id: "work", Name: "Work"},
			},
			expected: []string{"Default"},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			selected, err := submitProfileSelectionPrompt(t, tc.profiles, "\r")
			require.NoError(t, err)
			assert.Equal(t, tc.expected, selected)
		})
	}
}

func TestProfileSelectionPromptRejectsEmptySelection(t *testing.T) {
	t.Parallel()

	assert.ErrorContains(t, validateProfileSelection("Anthropic", nil), "no profile selected for Anthropic credentials")
}

func TestHandleManualAPIKeyAuthStoresKeyPerSelectedProfile(t *testing.T) {
	tests := []struct {
		name            string
		profiles        []common.Profile
		selectedProfile []string
		existingSecrets map[string]string
		expectedKeys    []string
	}{
		{
			name:         "only default profile declared stores unprefixed key",
			profiles:     []common.Profile{{Id: common.DefaultProfileId, Name: common.DefaultProfileName}},
			expectedKeys: []string{llm.OpenaiApiKeySecretName},
		},
		{
			name: "non-default profile stores prefixed key",
			profiles: []common.Profile{
				{Id: common.DefaultProfileId, Name: common.DefaultProfileName},
				{Id: "work", Name: "Work"},
			},
			selectedProfile: []string{"work"},
			expectedKeys:    []string{"WORK-" + llm.OpenaiApiKeySecretName},
		},
		{
			name: "multiple selected profiles each get their own key",
			profiles: []common.Profile{
				{Id: common.DefaultProfileId, Name: common.DefaultProfileName},
				{Id: "work", Name: "Work"},
			},
			selectedProfile: []string{common.DefaultProfileId, "work"},
			expectedKeys: []string{
				llm.OpenaiApiKeySecretName,
				"WORK-" + llm.OpenaiApiKeySecretName,
			},
		},
		{
			name: "existing key under another profile does not block storage",
			profiles: []common.Profile{
				{Id: common.DefaultProfileId, Name: common.DefaultProfileName},
				{Id: "work", Name: "Work"},
			},
			selectedProfile: []string{"work"},
			existingSecrets: map[string]string{llm.OpenaiApiKeySecretName: "existing-default-key"},
			expectedKeys:    []string{"WORK-" + llm.OpenaiApiKeySecretName},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			written := stubCredentialPrompts(t, tc.profiles, tc.selectedProfile, tc.existingSecrets)

			origPromptAPIKey := promptAPIKey
			origConfirm := confirmOverwriteExisting
			t.Cleanup(func() {
				promptAPIKey = origPromptAPIKey
				confirmOverwriteExisting = origConfirm
			})
			promptAPIKey = func(providerName string) (string, error) { return "new-api-key", nil }
			confirmOverwriteExisting = func(subject string, existingProfileIds []string) (bool, error) {
				return false, fmt.Errorf("unexpected overwrite prompt for %s", subject)
			}

			err := handleManualAPIKeyAuth("OpenAI", llm.OpenaiApiKeySecretName)
			require.NoError(t, err)

			expected := map[string]string{}
			for _, key := range tc.expectedKeys {
				expected[key] = "new-api-key"
			}
			assert.Equal(t, expected, written)
		})
	}
}

func TestHandleManualAPIKeyAuthKeepsExistingKeyForSelectedProfile(t *testing.T) {
	profiles := []common.Profile{
		{Id: common.DefaultProfileId, Name: common.DefaultProfileName},
		{Id: "work", Name: "Work"},
	}
	existing := map[string]string{"WORK-" + llm.OpenaiApiKeySecretName: "existing-work-key"}
	written := stubCredentialPrompts(t, profiles, []string{"work"}, existing)

	origPromptAPIKey := promptAPIKey
	origConfirm := confirmOverwriteExisting
	t.Cleanup(func() {
		promptAPIKey = origPromptAPIKey
		confirmOverwriteExisting = origConfirm
	})
	promptAPIKey = func(providerName string) (string, error) {
		return "", fmt.Errorf("unexpected API key prompt for %s", providerName)
	}
	confirmedSubject := ""
	var confirmedProfileIds []string
	confirmOverwriteExisting = func(subject string, existingProfileIds []string) (bool, error) {
		confirmedSubject = subject
		confirmedProfileIds = existingProfileIds
		return false, nil
	}

	require.NoError(t, handleManualAPIKeyAuth("OpenAI", llm.OpenaiApiKeySecretName))
	assert.Equal(t, "OpenAI API key", confirmedSubject)
	assert.Equal(t, []string{"work"}, confirmedProfileIds)
	assert.Empty(t, written)
}

func TestHandleManualAPIKeyAuthStoresForProfilesMissingTheKey(t *testing.T) {
	profiles := []common.Profile{
		{Id: common.DefaultProfileId, Name: common.DefaultProfileName},
		{Id: "work", Name: "Work"},
	}
	existing := map[string]string{llm.OpenaiApiKeySecretName: "existing-default-key"}
	written := stubCredentialPrompts(t, profiles, []string{common.DefaultProfileId, "work"}, existing)

	origPromptAPIKey := promptAPIKey
	origConfirm := confirmOverwriteExisting
	t.Cleanup(func() {
		promptAPIKey = origPromptAPIKey
		confirmOverwriteExisting = origConfirm
	})
	promptAPIKey = func(providerName string) (string, error) { return "new-api-key", nil }
	confirmOverwriteExisting = func(subject string, existingProfileIds []string) (bool, error) {
		return false, nil
	}

	require.NoError(t, handleManualAPIKeyAuth("OpenAI", llm.OpenaiApiKeySecretName))
	assert.Equal(t, map[string]string{
		"WORK-" + llm.OpenaiApiKeySecretName: "new-api-key",
	}, written, "keeping an existing key for one profile must not skip profiles without one")
}

func TestSaveOpenAIOAuthCredentialsPerProfile(t *testing.T) {
	written := stubCredentialPrompts(t, nil, nil, nil)

	creds := &openai_oauth.Credentials{
		AccessToken:  "access",
		RefreshToken: "refresh",
		AccountID:    "account",
	}
	require.NoError(t, saveOpenAIOAuthCredentials(creds, []string{common.DefaultProfileId, "work"}))

	credsJSON, err := json.Marshal(creds)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{
		openai_oauth.SecretName:           string(credsJSON),
		"WORK-" + openai_oauth.SecretName: string(credsJSON),
	}, written)
}

func TestSaveAnthropicOAuthCredentialsPerProfile(t *testing.T) {
	written := stubCredentialPrompts(t, nil, nil, nil)

	creds := OAuthCredentials{
		AccessToken:  "access",
		RefreshToken: "refresh",
		ExpiresAt:    123,
	}
	require.NoError(t, saveAnthropicOAuthCredentials(creds, []string{"work"}))

	credsJSON, err := json.Marshal(creds)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{
		"WORK-" + AnthropicOAuthSecretName: string(credsJSON),
	}, written)
}

func TestConfiguredSecretExistsAcrossProfiles(t *testing.T) {
	profiles := []common.Profile{
		{Id: common.DefaultProfileId, Name: common.DefaultProfileName},
		{Id: "work", Name: "Work"},
	}
	stubCredentialPrompts(t, profiles, nil, map[string]string{
		"WORK-" + llm.AnthropicApiKeySecretName: "work-key",
	})

	assert.True(t, configuredSecretExists(llm.AnthropicApiKeySecretName))
	assert.False(t, configuredSecretExists(llm.GoogleApiKeySecretName))
	assert.Equal(t, []string{"anthropic"}, getConfiguredBuiltinLLMProviders())
}
