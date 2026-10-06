package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sidekick/common"
	"sidekick/llm"
	"sidekick/provider_auth"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type credentialsUpdatedCall struct {
	provider   string
	profileIds []string
	method     provider_auth.Method
}

// stubCredentialPrompts wires deterministic stand-ins for the interactive
// prompts, keyring access and server notification used by the auth flows,
// returning the map of secrets written during the test.
func stubCredentialPrompts(t *testing.T, profiles []common.Profile, selectedProfileIds []string, existingSecrets map[string]string) map[string]string {
	t.Helper()
	written, _ := stubCredentialPromptsWithNotifications(t, profiles, selectedProfileIds, existingSecrets)
	return written
}

func stubCredentialPromptsWithNotifications(t *testing.T, profiles []common.Profile, selectedProfileIds []string, existingSecrets map[string]string) (map[string]string, *[]credentialsUpdatedCall) {
	t.Helper()

	origLoad := loadDeclaredProfiles
	origSelect := promptProfileSelection
	origGet := provider_auth.KeyringGet
	origSet := provider_auth.KeyringSet
	origNotify := notifyCredentialsUpdated
	t.Cleanup(func() {
		loadDeclaredProfiles = origLoad
		promptProfileSelection = origSelect
		provider_auth.KeyringGet = origGet
		provider_auth.KeyringSet = origSet
		notifyCredentialsUpdated = origNotify
	})

	notifications := &[]credentialsUpdatedCall{}
	notifyCredentialsUpdated = func(provider string, profileIds []string, method provider_auth.Method) {
		*notifications = append(*notifications, credentialsUpdatedCall{provider: provider, profileIds: profileIds, method: method})
	}

	loadDeclaredProfiles = func() ([]common.Profile, error) {
		return profiles, nil
	}
	promptProfileSelection = func(credentialName string, prompted []common.Profile) ([]string, error) {
		if selectedProfileIds == nil {
			return nil, fmt.Errorf("unexpected profile prompt for %s", credentialName)
		}
		return selectedProfileIds, nil
	}
	provider_auth.KeyringGet = newMockKeyringGet(existingSecrets)

	written := map[string]string{}
	provider_auth.KeyringSet = func(service, key, value string) error {
		written[key] = value
		return nil
	}
	return written, notifications
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

func TestProfileSelectionPromptRequiresExplicitSelection(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		profiles   []common.Profile
		keystrokes string
		expected   []string
	}{
		{
			name: "submitting without toggling keeps the prompt open",
			profiles: []common.Profile{
				{Id: common.DefaultProfileId, Name: "Personal"},
				{Id: "work", Name: "Work"},
			},
			keystrokes: "\r\x1b[Bx\r",
			expected:   []string{"work"},
		},
		{
			name: "toggled profile is selected on submit",
			profiles: []common.Profile{
				{Id: "Default", Name: "Personal"},
				{Id: "work", Name: "Work"},
			},
			keystrokes: "x\r",
			expected:   []string{"Default"},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			selected, err := submitProfileSelectionPrompt(t, tc.profiles, tc.keystrokes)
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

func TestHandleManualAPIKeyAuthNotifiesServerOfSavedProfiles(t *testing.T) {
	profiles := []common.Profile{
		{Id: common.DefaultProfileId, Name: common.DefaultProfileName},
		{Id: "work", Name: "Work"},
	}

	tests := []struct {
		name     string
		existing map[string]string
		expected []credentialsUpdatedCall
	}{
		{
			name: "saved key notifies for its profiles",
			expected: []credentialsUpdatedCall{
				{provider: "openai", profileIds: []string{"work"}, method: provider_auth.MethodAPIKey},
			},
		},
		{
			name:     "kept existing key does not notify",
			existing: map[string]string{"WORK-" + llm.OpenaiApiKeySecretName: "existing-work-key"},
			expected: []credentialsUpdatedCall{},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, notifications := stubCredentialPromptsWithNotifications(t, profiles, []string{"work"}, tc.existing)

			origPromptAPIKey := promptAPIKey
			origConfirm := confirmOverwriteExisting
			t.Cleanup(func() {
				promptAPIKey = origPromptAPIKey
				confirmOverwriteExisting = origConfirm
			})
			promptAPIKey = func(providerName string) (string, error) { return "new-api-key", nil }
			confirmOverwriteExisting = func(subject string, existingProfileIds []string) (bool, error) { return false, nil }

			require.NoError(t, handleManualAPIKeyAuth("OpenAI", llm.OpenaiApiKeySecretName))
			assert.Equal(t, tc.expected, *notifications)
		})
	}
}

func TestPostCredentialUpdatedEvents(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	var received []provider_auth.CredentialUpdatedEvent
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/api/v1/provider_auth/credential_updated", r.URL.Path)
		var event provider_auth.CredentialUpdatedEvent
		require.NoError(t, json.NewDecoder(r.Body).Decode(&event))
		mu.Lock()
		received = append(received, event)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	postCredentialUpdatedEvents(server.URL, provider_auth.ProviderAnthropic, []string{common.DefaultProfileId, "work"}, provider_auth.MethodOAuthSubscription)

	assert.Equal(t, []provider_auth.CredentialUpdatedEvent{
		{Provider: "anthropic", ProfileId: common.DefaultProfileId, Method: provider_auth.MethodOAuthSubscription, AuthType: common.ProviderAuthTypeSubscription},
		{Provider: "anthropic", ProfileId: "work", Method: provider_auth.MethodOAuthSubscription, AuthType: common.ProviderAuthTypeSubscription},
	}, received)
}

func TestPostCredentialUpdatedEventsIgnoresUnreachableServer(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.NotFoundHandler())
	unreachableURL := server.URL
	server.Close()

	postCredentialUpdatedEvents(unreachableURL, provider_auth.ProviderOpenAI, []string{"work"}, provider_auth.MethodAPIKey)
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
