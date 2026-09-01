package secret_manager

import (
	"encoding/json"
	"sidekick/common"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"
)

func TestProfileSecretName(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		profileId string
		expected  string
	}{
		{name: "unset profile is default", profileId: "", expected: "OPENAI_API_KEY"},
		{name: "default profile is unprefixed", profileId: common.DefaultProfileId, expected: "OPENAI_API_KEY"},
		{name: "default profile is case-insensitive", profileId: "Default", expected: "OPENAI_API_KEY"},
		{name: "non-default profile is prefixed", profileId: "work", expected: "WORK-OPENAI_API_KEY"},
		{name: "profile id casing is ignored", profileId: "Work", expected: "WORK-OPENAI_API_KEY"},
		{name: "underscores are kept intact", profileId: "acme_corp", expected: "ACME_CORP-OPENAI_API_KEY"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.expected, ProfileSecretName(tc.profileId, "OPENAI_API_KEY"))
		})
	}
}

func TestProfileEnvSecretName(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		profileId string
		expected  string
	}{
		{name: "unset profile is default", profileId: "", expected: "OPENAI_API_KEY"},
		{name: "default profile is unprefixed", profileId: common.DefaultProfileId, expected: "OPENAI_API_KEY"},
		{name: "non-default profile is prefixed", profileId: "work", expected: "WORK_OPENAI_API_KEY"},
		{name: "profile id casing is ignored", profileId: "Work", expected: "WORK_OPENAI_API_KEY"},
		{name: "underscores are kept intact", profileId: "acme_corp", expected: "ACME_CORP_OPENAI_API_KEY"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.expected, ProfileEnvSecretName(tc.profileId, "OPENAI_API_KEY"))
		})
	}
}

func TestEnvSecretManagerProfileScoping(t *testing.T) {
	t.Setenv("SIDE_OPENAI_API_KEY", "default-key")
	t.Setenv("SIDE_WORK_OPENAI_API_KEY", "work-key")

	defaultSecret, err := EnvSecretManager{}.GetSecret("OPENAI_API_KEY")
	require.NoError(t, err)
	assert.Equal(t, "default-key", defaultSecret)

	workSecret, err := EnvSecretManager{ProfileId: "work"}.GetSecret("OPENAI_API_KEY")
	require.NoError(t, err)
	assert.Equal(t, "work-key", workSecret)

	_, err = EnvSecretManager{ProfileId: "personal"}.GetSecret("OPENAI_API_KEY")
	assert.ErrorIs(t, err, ErrSecretNotFound)
}

func TestKeyringSecretManagerProfileScoping(t *testing.T) {
	keyring.MockInit()
	require.NoError(t, keyring.Set("sidekick", "OPENAI_API_KEY", "default-key"))
	require.NoError(t, keyring.Set("sidekick", "WORK-OPENAI_API_KEY", "work-key"))

	defaultSecret, err := KeyringSecretManager{}.GetSecret("OPENAI_API_KEY")
	require.NoError(t, err)
	assert.Equal(t, "default-key", defaultSecret)

	workSecret, err := KeyringSecretManager{ProfileId: "Work"}.GetSecret("OPENAI_API_KEY")
	require.NoError(t, err)
	assert.Equal(t, "work-key", workSecret)

	_, err = KeyringSecretManager{ProfileId: "personal"}.GetSecret("OPENAI_API_KEY")
	assert.ErrorIs(t, err, ErrSecretNotFound)
}

func TestValidProfileIdsResolveOnlyTheirOwnSecrets(t *testing.T) {
	t.Setenv("SIDE_ACME_CORP_OPENAI_API_KEY", "acme-corp-key")
	t.Setenv("SIDE_ACME_OPENAI_API_KEY", "acme-key")
	t.Setenv("SIDE_CORP_OPENAI_API_KEY", "corp-key")

	for profileId, expected := range map[string]string{
		"acme_corp": "acme-corp-key",
		"acme":      "acme-key",
		"corp":      "corp-key",
	} {
		secret, err := EnvSecretManager{ProfileId: profileId}.GetSecret("OPENAI_API_KEY")
		require.NoError(t, err)
		assert.Equal(t, expected, secret, "profile %s resolved another profile's secret", profileId)
	}

	// profile ids that would otherwise collapse onto an existing profile's
	// variable name are rejected as config rather than silently sharing secrets
	for _, profileId := range []string{"acme corp", "acme-corp", "acme.corp"} {
		assert.Error(t, common.ValidateProfileId(profileId))
	}
}

func TestLocalConfigSecretManagerProfileFiltering(t *testing.T) {
	t.Parallel()

	config := common.LocalConfig{
		Providers: []common.ModelProviderConfig{
			{Name: "openai", Type: "openai", Key: "default-key"},
			{Name: "openai-work", Type: "openai", Key: "work-key", Profiles: &[]string{"work"}},
		},
	}

	defaultKey, err := LocalConfigSecretManager{}.findProviderKey(config, "", "openai")
	require.NoError(t, err)
	assert.Equal(t, "default-key", defaultKey)

	workKey, err := LocalConfigSecretManager{ProfileId: "work"}.findProviderKey(config, "", "openai")
	require.NoError(t, err)
	assert.Equal(t, "work-key", workKey)

	_, err = LocalConfigSecretManager{ProfileId: "personal"}.findProviderKey(config, "", "openai")
	assert.ErrorIs(t, err, ErrSecretNotFound)
}

func TestLocalConfigSecretManagerProfileEdgeCases(t *testing.T) {
	t.Parallel()

	t.Run("explicitly empty association matches no profile", func(t *testing.T) {
		t.Parallel()
		config := common.LocalConfig{
			Providers: []common.ModelProviderConfig{
				{Name: "openai", Type: "openai", Key: "unscoped-key", Profiles: &[]string{}},
			},
		}

		_, err := LocalConfigSecretManager{}.findProviderKey(config, "", "openai")
		assert.ErrorIs(t, err, ErrSecretNotFound)

		_, err = LocalConfigSecretManager{ProfileId: "work"}.findProviderKey(config, "", "openai")
		assert.ErrorIs(t, err, ErrSecretNotFound)
	})

	t.Run("multiple providers matching the profile are ambiguous", func(t *testing.T) {
		t.Parallel()
		config := common.LocalConfig{
			Providers: []common.ModelProviderConfig{
				{Name: "openai", Type: "openai", Key: "work-key", Profiles: &[]string{"work"}},
				{Name: "openai_alt", Type: "openai", Key: "other-work-key", Profiles: &[]string{"work"}},
			},
		}

		_, err := LocalConfigSecretManager{ProfileId: "work"}.findProviderKey(config, "", "openai")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "multiple providers found")
	})
}

func TestProfileSecretManagerJSONRoundTrip(t *testing.T) {
	keyring.MockInit()
	t.Setenv("SIDE_WORK_PROFILE_ROUNDTRIP_API_KEY", "work-key")

	data, err := json.Marshal(SecretManagerContainer{SecretManager: NewProfileSecretManager("work")})
	require.NoError(t, err)

	var decoded SecretManagerContainer
	require.NoError(t, json.Unmarshal(data, &decoded))

	secret, err := decoded.GetSecret("PROFILE_ROUNDTRIP_API_KEY")
	require.NoError(t, err)
	assert.Equal(t, "work-key", secret)
}
