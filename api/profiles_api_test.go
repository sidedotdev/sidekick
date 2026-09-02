package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sidekick/common"
	"sidekick/secret_manager"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newLocalConfigController(t *testing.T, config common.LocalConfig, loadErr error) Controller {
	t.Helper()
	ctrl := NewMockController(t)
	ctrl.loadLocalConfig = func() (common.LocalConfig, error) { return config, loadErr }
	return ctrl
}

func profileIds(ids ...string) *[]string {
	return &ids
}

func getProfiles(t *testing.T, ctrl Controller) []common.Profile {
	t.Helper()
	router := DefineRoutes(ctrl, TestAllowedOrigins())
	req, _ := http.NewRequest("GET", "/api/v1/profiles", nil)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)

	var response struct {
		Profiles []common.Profile `json:"profiles"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &response))
	return response.Profiles
}

func getProviders(t *testing.T, ctrl Controller, profileId string) []string {
	t.Helper()
	router := DefineRoutes(ctrl, TestAllowedOrigins())
	url := "/api/v1/providers"
	if profileId != "" {
		url += "?profileId=" + profileId
	}
	req, _ := http.NewRequest("GET", url, nil)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)

	var response struct {
		Providers []string `json:"providers"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &response))
	return response.Providers
}

func TestGetProfilesHandler(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		declarations []common.ProfileConfig
		loadErr      error
		expected     []common.Profile
	}{
		{
			name:     "returns only the default profile without declarations",
			expected: []common.Profile{{Id: "default", Name: "Default"}},
		},
		{
			name: "returns declared profiles alongside the default profile",
			declarations: []common.ProfileConfig{
				{Id: "work", Name: "Work"},
				{Id: "side_project"},
			},
			expected: []common.Profile{
				{Id: "default", Name: "Default"},
				{Id: "work", Name: "Work"},
				{Id: "side_project", Name: "side_project"},
			},
		},
		{
			name:         "honors an overridden default profile name",
			declarations: []common.ProfileConfig{{Id: "default", Name: "Personal"}},
			expected:     []common.Profile{{Id: "default", Name: "Personal"}},
		},
		{
			name:     "returns the default profile when the config cannot be loaded",
			loadErr:  assert.AnError,
			expected: []common.Profile{{Id: "default", Name: "Default"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctrl := newLocalConfigController(t, common.LocalConfig{Profiles: tt.declarations}, tt.loadErr)

			assert.Equal(t, tt.expected, getProfiles(t, ctrl))
		})
	}
}

func TestGetProvidersHandlerFiltersByProfile(t *testing.T) {
	t.Parallel()

	config := common.LocalConfig{
		Profiles: []common.ProfileConfig{{Id: "work"}},
		Providers: []common.ModelProviderConfig{
			{Name: "work_openai", Type: "openai", Key: "work-key", Profiles: profileIds("work")},
			{Name: "personal_openai", Type: "openai", Key: "personal-key"},
			{Name: "shared_openai", Type: "openai", Key: "shared-key", Profiles: profileIds("default", "work")},
			{Name: "unassigned_openai", Type: "openai", Key: "unassigned-key", Profiles: profileIds()},
		},
	}

	tests := []struct {
		name                 string
		profileId            string
		expectedToContain    []string
		expectedNotToContain []string
	}{
		{
			name:                 "defaults to the default profile",
			profileId:            "",
			expectedToContain:    []string{"personal_openai", "shared_openai"},
			expectedNotToContain: []string{"work_openai", "unassigned_openai"},
		},
		{
			name:                 "returns providers of the requested profile",
			profileId:            "work",
			expectedToContain:    []string{"work_openai", "shared_openai"},
			expectedNotToContain: []string{"personal_openai", "unassigned_openai"},
		},
		{
			name:                 "matches profile ids case-insensitively",
			profileId:            "WORK",
			expectedToContain:    []string{"work_openai", "shared_openai"},
			expectedNotToContain: []string{"personal_openai", "unassigned_openai"},
		},
		{
			name:                 "returns no configured providers for an unknown profile",
			profileId:            "unknown",
			expectedToContain:    []string{},
			expectedNotToContain: []string{"work_openai", "personal_openai", "shared_openai", "unassigned_openai"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctrl := newLocalConfigController(t, config, nil)
			ctrl.secretManager = testSecretManager{secrets: map[string]string{}}
			providers := getProviders(t, ctrl, tt.profileId)

			for _, expected := range tt.expectedToContain {
				assert.Contains(t, providers, expected)
			}
			for _, notExpected := range tt.expectedNotToContain {
				assert.NotContains(t, providers, notExpected)
			}
		})
	}
}

func TestGetProvidersHandlerBuiltinCredentialsAreProfileScoped(t *testing.T) {
	t.Parallel()

	profileSecrets := map[string]map[string]string{
		"default": {"OPENAI_API_KEY": "personal-key"},
		"work":    {"ANTHROPIC_API_KEY": "work-key"},
	}

	ctrl := newLocalConfigController(t, common.LocalConfig{
		Profiles: []common.ProfileConfig{{Id: "work"}},
	}, nil)
	ctrl.secretManagerForProfile = func(profileId string) secret_manager.SecretManager {
		return testSecretManager{secrets: profileSecrets[profileId]}
	}

	defaultProviders := getProviders(t, ctrl, "")
	assert.Contains(t, defaultProviders, "openai")
	assert.NotContains(t, defaultProviders, "anthropic")

	workProviders := getProviders(t, ctrl, "work")
	assert.Contains(t, workProviders, "anthropic")
	assert.NotContains(t, workProviders, "openai")
}
