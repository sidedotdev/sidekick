package persisted_ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sidekick/openai_oauth"
	"sidekick/secret_manager"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type openAIOAuthWrapperTestSecretManager struct {
	secrets map[string]string
	errs    map[string]error
}

func (m openAIOAuthWrapperTestSecretManager) GetSecret(secretName string) (string, error) {
	if err := m.errs[secretName]; err != nil {
		return "", err
	}
	if secret, ok := m.secrets[secretName]; ok {
		return secret, nil
	}
	return "", fmt.Errorf("%w: %s", secret_manager.ErrSecretNotFound, secretName)
}

func (m openAIOAuthWrapperTestSecretManager) GetType() secret_manager.SecretManagerType {
	return secret_manager.MockSecretManagerType
}

func TestOpenAIOAuthSecretWrapper(t *testing.T) {
	t.Run("returns valid OAuth credentials", func(t *testing.T) {
		rawCredentials, err := json.Marshal(openai_oauth.Credentials{
			AccessToken:  "access-token",
			RefreshToken: "refresh-token",
			ExpiresAt:    time.Now().Add(time.Hour).Unix(),
			AccountID:    "account-id",
		})
		require.NoError(t, err)

		wrapper := newOpenAIOAuthSecretWrapper(
			context.Background(),
			openAIOAuthWrapperTestSecretManager{
				secrets: map[string]string{
					openai_oauth.SecretName: string(rawCredentials),
				},
			},
		)

		got, err := wrapper.GetSecret(openai_oauth.SecretName)
		require.NoError(t, err)

		var credentials openai_oauth.Credentials
		require.NoError(t, json.Unmarshal([]byte(got), &credentials))
		assert.Equal(t, "access-token", credentials.AccessToken)
		assert.Equal(t, "account-id", credentials.AccountID)
	})

	t.Run("preserves missing status", func(t *testing.T) {
		wrapper := newOpenAIOAuthSecretWrapper(
			context.Background(),
			openAIOAuthWrapperTestSecretManager{},
		)

		_, err := wrapper.GetSecret(openai_oauth.SecretName)
		require.Error(t, err)
		assert.ErrorIs(t, err, secret_manager.ErrSecretNotFound)
	})

	t.Run("propagates retrieval errors", func(t *testing.T) {
		retrievalErr := errors.New("keyring unavailable")
		wrapper := newOpenAIOAuthSecretWrapper(
			context.Background(),
			openAIOAuthWrapperTestSecretManager{
				errs: map[string]error{
					openai_oauth.SecretName: retrievalErr,
				},
			},
		)

		_, err := wrapper.GetSecret(openai_oauth.SecretName)
		require.Error(t, err)
		assert.ErrorIs(t, err, retrievalErr)
	})

	t.Run("passes through unrelated secrets", func(t *testing.T) {
		wrapper := newOpenAIOAuthSecretWrapper(
			context.Background(),
			openAIOAuthWrapperTestSecretManager{
				secrets: map[string]string{
					"OPENAI_API_KEY": "api-key",
				},
			},
		)

		got, err := wrapper.GetSecret("OPENAI_API_KEY")
		require.NoError(t, err)
		assert.Equal(t, "api-key", got)
		assert.Equal(t, secret_manager.MockSecretManagerType, wrapper.GetType())
	})
}
