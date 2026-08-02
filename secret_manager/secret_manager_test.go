package secret_manager

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSecretManagerContainer_MarshalUnmarshal(t *testing.T) {
	tests := []struct {
		name    string
		manager SecretManager
	}{
		{
			name:    "EnvSecretManager",
			manager: &EnvSecretManager{},
		},
		{
			name:    "KeyringtSecretManager",
			manager: &KeyringSecretManager{},
		},
		{
			name:    "MockSecretManager",
			manager: &MockSecretManager{},
		},
		{
			name: "CompositeSecretManager",
			manager: NewCompositeSecretManager([]SecretManager{
				&EnvSecretManager{},
				&KeyringSecretManager{},
			}),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			originalContainer := SecretManagerContainer{SecretManager: tt.manager}

			// Marshal the original SecretManagerContainer
			jsonBytes, err := json.Marshal(originalContainer)
			assert.NoError(t, err)

			// Unmarshal into a new SecretManagerContainer
			var unmarshaledContainer SecretManagerContainer
			err = json.Unmarshal(jsonBytes, &unmarshaledContainer)
			assert.NoError(t, err)

			// Assert that the original and unmarshaled containers are equal
			assert.Equal(t, originalContainer, unmarshaledContainer)
		})
	}
}

type staticSecretManager struct {
	secret string
	err    error
}

func (m staticSecretManager) GetSecret(string) (string, error) {
	return m.secret, m.err
}

func (m staticSecretManager) GetType() SecretManagerType {
	return MockSecretManagerType
}

func TestCompositeSecretManager_GetSecret(t *testing.T) {
	t.Run("later manager can satisfy request after retrieval error", func(t *testing.T) {
		retrievalErr := errors.New("keyring unavailable")
		manager := NewCompositeSecretManager([]SecretManager{
			staticSecretManager{err: retrievalErr},
			staticSecretManager{secret: "value"},
		})

		secret, err := manager.GetSecret("OPENAI_OAUTH")
		require.NoError(t, err)
		assert.Equal(t, "value", secret)
	})

	t.Run("retrieval error is not reported as missing", func(t *testing.T) {
		retrievalErr := errors.New("keyring unavailable")
		manager := NewCompositeSecretManager([]SecretManager{
			staticSecretManager{err: retrievalErr},
			staticSecretManager{err: fmt.Errorf("%w: environment", ErrSecretNotFound)},
		})

		_, err := manager.GetSecret("OPENAI_OAUTH")
		require.Error(t, err)
		assert.ErrorIs(t, err, retrievalErr)
		assert.NotErrorIs(t, err, ErrSecretNotFound)
	})

	t.Run("all missing errors preserve missing status without error logging", func(t *testing.T) {
		var logs bytes.Buffer
		originalLogger := log.Logger
		log.Logger = zerolog.New(&logs)
		t.Cleanup(func() {
			log.Logger = originalLogger
		})

		manager := NewCompositeSecretManager([]SecretManager{
			staticSecretManager{err: fmt.Errorf("%w: keyring", ErrSecretNotFound)},
			staticSecretManager{err: fmt.Errorf("%w: environment", ErrSecretNotFound)},
		})

		_, err := manager.GetSecret("OPENAI_OAUTH")
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrSecretNotFound)
		assert.Empty(t, logs.String())
	})
}
