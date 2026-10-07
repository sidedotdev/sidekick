package dev

import (
	"context"
	"fmt"
	"sidekick/common"
	"sidekick/llm"
	"sidekick/secret_manager"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/temporal"
)

type toolNameMapperAuthTestSecretManager map[string]string

func (m toolNameMapperAuthTestSecretManager) GetSecret(secretName string) (string, error) {
	if secret, ok := m[secretName]; ok {
		return secret, nil
	}
	return "", fmt.Errorf("%w: %s", secret_manager.ErrSecretNotFound, secretName)
}

func (m toolNameMapperAuthTestSecretManager) GetType() secret_manager.SecretManagerType {
	return secret_manager.MockSecretManagerType
}

func TestResolveToolNameMappingActivity_ReturnsTopLevelProviderAuthError(t *testing.T) {
	t.Parallel()

	_, err := ResolveToolNameMappingActivity(context.Background(), ResolveToolNameMappingInput{
		Secrets: secret_manager.SecretManagerContainer{
			SecretManager: toolNameMapperAuthTestSecretManager{llm.AnthropicOAuthSecretName: "not-json"},
		},
	})

	require.Error(t, err)
	appErr, ok := err.(*temporal.ApplicationError)
	require.True(t, ok, "provider auth error must not be wrapped, got %T", err)
	assert.True(t, appErr.NonRetryable())
	details, ok := common.AsProviderAuthError(err)
	require.True(t, ok)
	assert.Equal(t, common.ProviderAuthErrorReasonInvalidOAuth, details.Reason)
	assert.Equal(t, "anthropic", details.Provider)
}
