package main

import (
	"fmt"
	"sidekick/common"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubWorkspaceProfilePrompts wires deterministic stand-ins for profile
// loading and the workspace profile prompt. A nil promptedProfileId makes any
// prompt fail the test, so no-prompt cases are asserted implicitly.
func stubWorkspaceProfilePrompts(t *testing.T, profiles []common.Profile, promptedProfileId *string) {
	t.Helper()

	origLoad := loadDeclaredProfiles
	origPrompt := promptWorkspaceProfileSelection
	t.Cleanup(func() {
		loadDeclaredProfiles = origLoad
		promptWorkspaceProfileSelection = origPrompt
	})

	loadDeclaredProfiles = func() ([]common.Profile, error) {
		return profiles, nil
	}
	promptWorkspaceProfileSelection = func(prompted []common.Profile) (string, error) {
		if promptedProfileId == nil {
			return "", fmt.Errorf("unexpected workspace profile prompt")
		}
		assert.Equal(t, profiles, prompted)
		return *promptedProfileId, nil
	}
}

func TestResolveWorkspaceProfileId(t *testing.T) {
	defaultOnly := []common.Profile{
		{Id: common.DefaultProfileId, Name: common.DefaultProfileName},
	}
	multiple := []common.Profile{
		{Id: common.DefaultProfileId, Name: common.DefaultProfileName},
		{Id: "Work", Name: "Work"},
	}

	promptWork := "Work"
	tests := []struct {
		name               string
		profiles           []common.Profile
		requestedProfileId string
		promptedProfileId  *string
		expected           string
		expectedErr        string
	}{
		{
			name:     "default profile only resolves without prompting",
			profiles: defaultOnly,
			expected: common.DefaultProfileId,
		},
		{
			name:              "multiple profiles use the prompted selection",
			profiles:          multiple,
			promptedProfileId: &promptWork,
			expected:          "Work",
		},
		{
			name:               "valid requested profile skips the prompt",
			profiles:           multiple,
			requestedProfileId: "Work",
			expected:           "Work",
		},
		{
			name:               "requested profile matches case-insensitively and canonicalizes",
			profiles:           multiple,
			requestedProfileId: "wOrK",
			expected:           "Work",
		},
		{
			name:               "unknown requested profile errors with valid ids",
			profiles:           multiple,
			requestedProfileId: "personal",
			expectedErr:        `profile "personal" is not declared, valid profile ids are: default, Work`,
		},
		{
			name:               "invalid requested profile id is rejected",
			profiles:           multiple,
			requestedProfileId: "not valid!",
			expectedErr:        "invalid profile id",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			stubWorkspaceProfilePrompts(t, tc.profiles, tc.promptedProfileId)

			profileId, err := resolveWorkspaceProfileId(tc.requestedProfileId)
			if tc.expectedErr != "" {
				require.ErrorContains(t, err, tc.expectedErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.expected, profileId)
		})
	}
}

func TestResolveWorkspaceProfileIdLoadError(t *testing.T) {
	origLoad := loadDeclaredProfiles
	t.Cleanup(func() { loadDeclaredProfiles = origLoad })
	loadDeclaredProfiles = func() ([]common.Profile, error) {
		return nil, fmt.Errorf("boom")
	}

	_, err := resolveWorkspaceProfileId("")
	require.ErrorContains(t, err, "boom")
}
