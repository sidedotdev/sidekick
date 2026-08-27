package common

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLocalPublicConfigFromProfiles(t *testing.T) {
	t.Parallel()

	config := LocalConfig{
		Profiles: []ProfileConfig{{Id: "default", Name: "Personal"}, {Id: "work"}},
		Providers: []ModelProviderConfig{
			{Name: "openai", Type: "openai", Key: "personal-key"},
			{
				Name:     "unassociated",
				Type:     "openai_compatible",
				BaseURL:  "https://example.com",
				Key:      "abc123",
				Profiles: &[]string{},
			},
			{Name: "anthropic", Type: "anthropic", Key: "work-key", Profiles: &[]string{"work"}},
		},
		LLM: map[string][]ModelConfig{DefaultKey: {{Provider: "openai"}}},
	}

	publicConfig, err := localPublicConfigFrom(config)
	require.NoError(t, err)

	assert.Equal(t, []Profile{
		{Id: "default", Name: "Personal"},
		{Id: "work", Name: "work"},
	}, publicConfig.Profiles)

	require.Len(t, publicConfig.Providers, 3)
	assert.Nil(t, publicConfig.Providers[0].Profiles)
	assert.Equal(t, []string{DefaultProfileId}, publicConfig.Providers[0].EffectiveProfiles())
	require.NotNil(t, publicConfig.Providers[1].Profiles, "an explicitly empty profile list must not fall back to default")
	assert.Empty(t, publicConfig.Providers[1].EffectiveProfiles())
	assert.Equal(t, []string{"work"}, publicConfig.Providers[2].EffectiveProfiles())

	serialized, err := json.Marshal(publicConfig)
	require.NoError(t, err)
	assert.NotContains(t, string(serialized), "personal-key")
	assert.NotContains(t, string(serialized), "work-key")
	assert.Contains(t, string(serialized), `"profiles":[]`, "an explicitly empty profile list must survive serialization")
	var roundTripped LocalPublicConfig
	require.NoError(t, json.Unmarshal(serialized, &roundTripped))

	assert.Equal(t, publicConfig, roundTripped)
	assert.Nil(t, roundTripped.Providers[0].Profiles)
	require.NotNil(t, roundTripped.Providers[1].Profiles)
	assert.Empty(t, *roundTripped.Providers[1].Profiles)
	assert.Equal(t, []string{"work"}, *roundTripped.Providers[2].Profiles)
}
