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
