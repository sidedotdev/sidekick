package secret_manager

import (
	"encoding/json"
	"fmt"
	"os"
	"sidekick/common"
	"strings"

	"github.com/zalando/go-keyring"
)

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
)

type EnvSecretManager struct{}

func (e EnvSecretManager) GetSecret(secretName string) (string, error) {
	secretName = fmt.Sprintf("SIDE_%s", secretName)
	secret := os.Getenv(secretName)
	if secret == "" {
		return "", fmt.Errorf("secret %s not found in environment", secretName)
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
		return "", fmt.Errorf("error retrieving %s from keyring: %w", secretName, err)
	}
	return secret, nil
}

func (k KeyringSecretManager) GetType() SecretManagerType {
	return KeyringSecretManagerType
}

type LocalConfigSecretManager struct{}

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

	return "", fmt.Errorf("secret %s not found in local config", secretName)
}

func (l LocalConfigSecretManager) findProviderKey(config common.LocalConfig, name, providerType string) (string, error) {
	var matches []common.ModelProviderConfig

	for _, provider := range config.Providers {
		if providerType != "" && provider.Type == providerType {
			matches = append(matches, provider)
		} else if name != "" {
			// Convert provider name to match secret name format
			providerNameNormalized := strings.ToUpper(strings.ReplaceAll(strings.ReplaceAll(provider.Name, " ", "_"), "-", "_"))
			if providerNameNormalized == name {
				matches = append(matches, provider)
			}
		}
	}
	if len(matches) == 0 {
		if providerType != "" {
			return "", fmt.Errorf("no provider found with type %s", providerType)
		}
		return "", fmt.Errorf("no provider found with name %s", name)
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
	return "fake secret", nil
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
	default:
		return fmt.Errorf("unknown SecretManager type: %s", v.Type)
	}

	return nil
}
