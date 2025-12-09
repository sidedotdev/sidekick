package secret_manager

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sidekick/common"
	"strings"

	"github.com/zalando/go-keyring"
)

// ErrSecretNotFound is returned when a secret is not found in any secret manager
var ErrSecretNotFound = errors.New("secret not found")

type SecretManager interface {
	GetSecret(secretName string) (string, error)
	GetType() SecretManagerType
}

type SecretManagerType string

const (
	EnvSecretManagerType         SecretManagerType = "env"
	MockSecretManagerType        SecretManagerType = "mock"
	KeyringSecretManagerType     SecretManagerType = "keyring"
	LocalConfigSecretManagerType SecretManagerType = "local_config"
	CompositeSecretManagerType   SecretManagerType = "composite"
)

type EnvSecretManager struct{}

func (e EnvSecretManager) GetSecret(secretName string) (string, error) {
	secretName = fmt.Sprintf("SIDE_%s", secretName)
	secret := os.Getenv(secretName)
	if secret == "" {
		return "", fmt.Errorf("%w: %s not found in environment", ErrSecretNotFound, secretName)
	}
	return secret, nil
}

func (e EnvSecretManager) GetType() SecretManagerType {
	return EnvSecretManagerType
}

type KeyringSecretManager struct{}

func (k KeyringSecretManager) GetSecret(secretName string) (string, error) {
	secret, err := keyring.Get("sidekick", secretName)
	if err != nil {
		if errors.Is(err, keyring.ErrNotFound) {
			return "", fmt.Errorf("%w: %s not found in keyring", ErrSecretNotFound, secretName)
		}
		return "", fmt.Errorf("error retrieving %s from keyring: %w", secretName, err)
	}
	return secret, nil
}

func (k KeyringSecretManager) GetType() SecretManagerType {
	return KeyringSecretManagerType
}

type LocalConfigSecretManager struct{}

type CompositeSecretManager struct {
	managers []SecretManager
}

func NewCompositeSecretManager(managers []SecretManager) *CompositeSecretManager {
	return &CompositeSecretManager{
		managers: managers,
	}
}

func (c CompositeSecretManager) GetSecret(secretName string) (string, error) {
	var lastErr error
	for _, manager := range c.managers {
		secret, err := manager.GetSecret(secretName)
		if err == nil {
			return secret, nil
		}
		lastErr = err
	}
	if lastErr != nil {
		return "", fmt.Errorf("secret %s not found in any secret manager: %v", secretName, lastErr)
	}
	return "", fmt.Errorf("no secret managers configured")
}

func (c CompositeSecretManager) MarshalJSON() ([]byte, error) {
	managers := make([]SecretManagerContainer, len(c.managers))
	for i, manager := range c.managers {
		managers[i] = SecretManagerContainer{
			SecretManager: manager,
		}
	}
	return json.Marshal(struct {
		Managers []SecretManagerContainer `json:"managers"`
	}{
		Managers: managers,
	})
}

func (c *CompositeSecretManager) UnmarshalJSON(data []byte) error {
	var container struct {
		Containers []SecretManagerContainer `json:"managers"`
	}
	if err := json.Unmarshal(data, &container); err != nil {
		return err
	}

	c.managers = make([]SecretManager, len(container.Containers))
	for i, container := range container.Containers {
		c.managers[i] = container.SecretManager
	}

	return nil
}

func (c CompositeSecretManager) GetType() SecretManagerType {
	return CompositeSecretManagerType
}

func (l LocalConfigSecretManager) GetType() SecretManagerType {
	return LocalConfigSecretManagerType
}

func (l LocalConfigSecretManager) GetSecret(secretName string) (string, error) {
	// Load the local config
	configPath := common.GetSidekickConfigPath()
	config, err := common.LoadSidekickConfig(configPath)
	if err != nil {
		return "", fmt.Errorf("error loading local config: %w", err)
	}

	// Handle special cases first
	switch secretName {
	case "OPENAI_API_KEY":
		return l.findProviderKey(config, "", "openai")
	case "ANTHROPIC_API_KEY":
		return l.findProviderKey(config, "", "anthropic")
	}

	// For other cases, strip _API_KEY suffix and match against provider names
	if strings.HasSuffix(secretName, "_API_KEY") {
		providerName := strings.TrimSuffix(secretName, "_API_KEY")
		return l.findProviderKey(config, providerName, "")
	}

	return "", fmt.Errorf("%w: %s not found in local config", ErrSecretNotFound, secretName)
}

func (l LocalConfigSecretManager) findProviderKey(config common.LocalConfig, name, providerType string) (string, error) {
	var matches []common.ModelProviderConfig

	for _, provider := range config.Providers {
		if providerType != "" && provider.Type == providerType {
			matches = append(matches, provider)
		} else if name != "" {
			// Convert provider name to match secret name format
			providerNameNormalized := common.ModelConfig{Provider: provider.Name}.NormalizedProviderName()
			if providerNameNormalized == name {
				matches = append(matches, provider)
			}
		}
	}
	if len(matches) == 0 {
		if providerType != "" {
			return "", fmt.Errorf("%w: no provider found with type %s", ErrSecretNotFound, providerType)
		}
		return "", fmt.Errorf("%w: no provider found with name %s", ErrSecretNotFound, name)
	}
	if len(matches) > 1 {
		if providerType != "" {
			return "", fmt.Errorf("multiple providers found with type %s", providerType)
		}
		return "", fmt.Errorf("multiple providers found with name %s", name)
	}
	return matches[0].Key, nil
}

type MockSecretManager struct{}

func (e MockSecretManager) GetSecret(secretName string) (string, error) {
	if strings.HasSuffix(secretName, "_API_KEY") {
		return "fake secret", nil
	}
	return "", fmt.Errorf("%w: %s not found in mock", ErrSecretNotFound, secretName)
}

func (e MockSecretManager) GetType() SecretManagerType {
	return MockSecretManagerType
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
	case string(LocalConfigSecretManagerType):
		var lcm *LocalConfigSecretManager
		if err := json.Unmarshal(v.Manager, &lcm); err != nil {
			return err
		}
		sc.SecretManager = lcm
	case string(CompositeSecretManagerType):
		var csm *CompositeSecretManager
		if err := json.Unmarshal(v.Manager, &csm); err != nil {
			return err
		}
		sc.SecretManager = csm
	default:
		return fmt.Errorf("unknown SecretManager type: %s", v.Type)
	}

	return nil
}
