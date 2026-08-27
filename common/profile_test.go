package common

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveProfiles(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		declarations []ProfileConfig
		expected     []Profile
	}{
		{
			name:         "default profile exists without declarations",
			declarations: nil,
			expected:     []Profile{{Id: "default", Name: "Default"}},
		},
		{
			name:         "names fall back to ids",
			declarations: []ProfileConfig{{Id: "work"}},
			expected: []Profile{
				{Id: "default", Name: "Default"},
				{Id: "work", Name: "work"},
			},
		},
		{
			name:         "declared names are used",
			declarations: []ProfileConfig{{Id: "work", Name: "Work"}},
			expected: []Profile{
				{Id: "default", Name: "Default"},
				{Id: "work", Name: "Work"},
			},
		},
		{
			name:         "declaring default overrides its name without duplicating it",
			declarations: []ProfileConfig{{Id: "default", Name: "Personal"}, {Id: "work", Name: "Work"}},
			expected: []Profile{
				{Id: "default", Name: "Personal"},
				{Id: "work", Name: "Work"},
			},
		},
		{
			name:         "declaring default without a name keeps the default name",
			declarations: []ProfileConfig{{Id: "default"}},
			expected:     []Profile{{Id: "default", Name: "Default"}},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.expected, ResolveProfiles(tt.declarations))
		})
	}
}

func TestProfileConfigValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		profile ProfileConfig
		error   string
	}{
		{name: "valid", profile: ProfileConfig{Id: "work-1_a", Name: "Work"}},
		{name: "id with unusual characters", profile: ProfileConfig{Id: "client a/b", Name: "Client A/B"}},
		{name: "missing id", profile: ProfileConfig{Name: "Work"}, error: "profile id is required"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := tt.profile.Validate()
			if tt.error == "" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.error)
			}
		})
	}
}

func TestProviderProfilesJSONRoundTrip(t *testing.T) {
	t.Parallel()

	providers := []ModelProviderConfig{
		{Name: "openai", Type: "openai", Key: "abc123"},
		{Name: "unassociated", Type: "openai_compatible", BaseURL: "https://example.com", Key: "abc123", Profiles: &[]string{}},
		{Name: "anthropic", Type: "anthropic", Key: "abc123", Profiles: &[]string{"work"}},
	}

	serialized, err := json.Marshal(providers)
	require.NoError(t, err)

	var roundTripped []ModelProviderConfig
	require.NoError(t, json.Unmarshal(serialized, &roundTripped))

	require.Equal(t, providers, roundTripped)
	assert.Nil(t, roundTripped[0].Profiles)
	assert.Equal(t, []string{DefaultProfileId}, roundTripped[0].EffectiveProfiles())
	require.NotNil(t, roundTripped[1].Profiles, "an explicitly empty profile list must not fall back to default")
	assert.Empty(t, roundTripped[1].EffectiveProfiles())
	assert.Equal(t, []string{"work"}, roundTripped[2].EffectiveProfiles())
}

func TestProviderProfileAssociation(t *testing.T) {
	t.Parallel()

	work := []string{"work"}
	none := []string{}

	tests := []struct {
		name              string
		profiles          *[]string
		expectedEffective []string
		matches           map[string]bool
	}{
		{
			name:              "unconfigured profiles default to the default profile",
			profiles:          nil,
			expectedEffective: []string{"default"},
			matches:           map[string]bool{"": true, "default": true, "work": false},
		},
		{
			name:              "explicitly empty profiles match nothing",
			profiles:          &none,
			expectedEffective: []string{},
			matches:           map[string]bool{"": false, "default": false, "work": false},
		},
		{
			name:              "configured profiles match only themselves",
			profiles:          &work,
			expectedEffective: []string{"work"},
			matches:           map[string]bool{"": false, "default": false, "work": true},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			provider := ModelProviderConfig{Name: "p", Type: "openai", Key: "k", Profiles: tt.profiles}
			publicProvider := ModelProviderPublicConfig{Name: "p", Type: "openai", Profiles: tt.profiles}
			assert.Equal(t, tt.expectedEffective, provider.EffectiveProfiles())
			assert.Equal(t, tt.expectedEffective, publicProvider.EffectiveProfiles())
			for profileId, expected := range tt.matches {
				assert.Equal(t, expected, provider.MatchesProfile(profileId), "profile %q", profileId)
				assert.Equal(t, expected, publicProvider.MatchesProfile(profileId), "profile %q", profileId)
			}
		})
	}
}
