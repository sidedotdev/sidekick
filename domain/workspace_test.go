package domain

import (
	"encoding/json"
	"sidekick/common"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestWorkspaceEffectiveProfileId(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		profileId string
		expected  string
	}{
		{name: "unset profile falls back to the default profile", profileId: "", expected: common.DefaultProfileId},
		{name: "assigned profile is used as-is", profileId: "work", expected: "work"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.expected, Workspace{ProfileId: tc.profileId}.EffectiveProfileId())
		})
	}
}

func TestWorkspaceProfileIdJSON(t *testing.T) {
	t.Parallel()

	withProfile, err := json.Marshal(Workspace{Id: "ws-1", ProfileId: "work"})
	assert.NoError(t, err)
	assert.Contains(t, string(withProfile), `"profileId":"work"`)

	withoutProfile, err := json.Marshal(Workspace{Id: "ws-1"})
	assert.NoError(t, err)
	assert.NotContains(t, string(withoutProfile), "profileId")

	var decoded Workspace
	assert.NoError(t, json.Unmarshal(withProfile, &decoded))
	assert.Equal(t, "work", decoded.ProfileId)
}
