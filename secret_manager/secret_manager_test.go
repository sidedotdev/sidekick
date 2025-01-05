package secret_manager

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
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

func TestMockSecretManager_SetSecret(t *testing.T) {
	m := &MockSecretManager{}
	err := m.SetSecret("test-key", "test-secret")
	assert.NoError(t, err)

	secret, err := m.GetSecret("test-key")
	assert.NoError(t, err)
	assert.Equal(t, "test-secret", secret)
}

func TestKeyringSecretManager_SetSecret(t *testing.T) {
	k := &KeyringSecretManager{}
	err := k.SetSecret("test-key", "test-secret")
	assert.NoError(t, err)

	secret, err := k.GetSecret("test-key")
	assert.NoError(t, err)
	assert.Equal(t, "test-secret", secret)
}

func TestEnvSecretManager_SetSecret(t *testing.T) {
	e := &EnvSecretManager{}
	err := e.SetSecret("test-key", "test-secret")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "cannot set secrets in environment secret manager")
}
