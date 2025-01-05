package secret_manager

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/zalando/go-keyring"
)

type SecretManager interface {
	GetSecret(secretName string) (string, error)
	SetSecret(secretName string, secret string) error
	DeleteSecret(secretName string) error
	GetType() SecretManagerType
}

type SecretManagerType string

const (
	EnvSecretManagerType     SecretManagerType = "env"
	MockSecretManagerType    SecretManagerType = "mock"
	KeyringSecretManagerType SecretManagerType = "keyring"
)

type EnvSecretManager struct{}

func (e EnvSecretManager) SetSecret(secretName string, secret string) error {
	return fmt.Errorf("cannot set secrets in environment secret manager - secrets must be set as environment variables")
}

func (e EnvSecretManager) GetSecret(secretName string) (string, error) {
	secretName = fmt.Sprintf("SIDE_%s", secretName)
	secret := os.Getenv(secretName)
	if secret == "" {
		return "", fmt.Errorf("secret %s not found in environment", secretName)
	}
	return secret, nil
}

func (e EnvSecretManager) DeleteSecret(secretName string) error {
	return fmt.Errorf("cannot delete secrets in environment secret manager - secrets must be managed via environment variables")
}

func (e EnvSecretManager) GetType() SecretManagerType {
	return EnvSecretManagerType
}

type KeyringSecretManager struct{}

func (k KeyringSecretManager) SetSecret(secretName string, secret string) error {
	err := keyring.Set("sidekick", secretName, secret)
	if err != nil {
		return fmt.Errorf("error setting %s in keyring: %w", secretName, err)
	}
	return nil
}

func (k KeyringSecretManager) GetSecret(secretName string) (string, error) {
	secret, err := keyring.Get("sidekick", secretName)
	if err != nil {
		return "", fmt.Errorf("error retrieving %s from keyring: %w", secretName, err)
	}
	return secret, nil
}

func (k KeyringSecretManager) DeleteSecret(secretName string) error {
	err := keyring.Delete("sidekick", secretName)
	if err != nil {
		return fmt.Errorf("error deleting %s from keyring: %w", secretName, err)
	}
	return nil
}

func (k KeyringSecretManager) GetType() SecretManagerType {
	return KeyringSecretManagerType
}

type MockSecretManager struct {
	secrets map[string]string
}

func (m MockSecretManager) GetSecret(secretName string) (string, error) {
	if m.secrets == nil {
		return "fake secret", nil
	}
	if secret, ok := m.secrets[secretName]; ok {
		return secret, nil
	}
	return "fake secret", nil
}

func (m *MockSecretManager) SetSecret(secretName string, secret string) error {
	if m.secrets == nil {
		m.secrets = make(map[string]string)
	}
	m.secrets[secretName] = secret
	return nil
}

func (m *MockSecretManager) DeleteSecret(secretName string) error {
	if m.secrets != nil {
		delete(m.secrets, secretName)
	}
	return nil
}

func (m MockSecretManager) GetType() SecretManagerType {
	return MockSecretManagerType
}

// GetSecretManager returns a SecretManager instance of the specified type
func GetSecretManager(smType SecretManagerType) SecretManager {
	switch smType {
	case KeyringSecretManagerType:
		return &KeyringSecretManager{}
	case EnvSecretManagerType:
		return &EnvSecretManager{}
	case MockSecretManagerType:
		return &MockSecretManager{}
	default:
		return &KeyringSecretManager{} // Default to keyring
	}
}

type SecretManagerContainer struct {
	SecretManager
}

func (sc SecretManagerContainer) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Type    string
		Manager SecretManager
	}{
		Type:    string(sc.SecretManager.GetType()),
		Manager: sc.SecretManager,
	})
}

func (sc *SecretManagerContainer) UnmarshalJSON(data []byte) error {
	var v struct {
		Type    string
		Manager json.RawMessage
	}
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}

	switch v.Type {
	case string(EnvSecretManagerType):
		var esm *EnvSecretManager
		if err := json.Unmarshal(v.Manager, &esm); err != nil {
			return err
		}
		sc.SecretManager = esm
	case string(MockSecretManagerType):
		var msm *MockSecretManager
		if err := json.Unmarshal(v.Manager, &msm); err != nil {
			return err
		}
		sc.SecretManager = msm
	case string(KeyringSecretManagerType):
		var ksm *KeyringSecretManager
		if err := json.Unmarshal(v.Manager, &ksm); err != nil {
			return err
		}
		sc.SecretManager = ksm
	default:
		return fmt.Errorf("unknown SecretManager type: %s", v.Type)
	}

	return nil
}
