package dev

import (
	"sidekick/common"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewTempLocalExecContextScopesSecretsToProfile(t *testing.T) {
	t.Setenv("SIDE_WORK_PROFILE_SCOPED_API_KEY", "work-key")

	eCtx, err := newTempLocalExecContext(nil, "workspace-id", t.TempDir(), "work", nil, common.LLMConfig{}, common.EmbeddingConfig{})
	require.NoError(t, err)

	assert.Equal(t, "work", eCtx.ProfileId)

	secret, err := eCtx.Secrets.GetSecret("PROFILE_SCOPED_API_KEY")
	require.NoError(t, err)
	assert.Equal(t, "work-key", secret)
}
