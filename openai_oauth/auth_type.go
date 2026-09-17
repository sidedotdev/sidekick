package openai_oauth

import (
	"errors"
	"fmt"

	"sidekick/common"
	"sidekick/secret_manager"
)

// ResolveAuthType picks the effective OpenAI authentication mode for a
// request: "any" selects subscription when OAuth credentials are stored and
// API-key authentication only when lookupOAuth reports
// secret_manager.ErrSecretNotFound. Inference and the credential proxy share
// this rule so a proxy bound at activation matches what a direct request would
// have done. lookupOAuth is only invoked for "any", letting callers reuse the
// loaded credentials instead of reading (and possibly refreshing) them twice.
func ResolveAuthType(authType common.ProviderAuthType, lookupOAuth func() error) (common.ProviderAuthType, error) {
	switch normalized := common.NormalizeProviderAuthType(string(authType)); normalized {
	case common.ProviderAuthTypeAPI, common.ProviderAuthTypeSubscription:
		return normalized, nil
	case common.ProviderAuthTypeAny:
		err := lookupOAuth()
		if err == nil {
			return common.ProviderAuthTypeSubscription, nil
		}
		if errors.Is(err, secret_manager.ErrSecretNotFound) {
			return common.ProviderAuthTypeAPI, nil
		}
		return "", err
	default:
		return "", fmt.Errorf("unsupported OpenAI auth type: %s", authType)
	}
}

// LookupOAuth adapts a secret source for ResolveAuthType without inspecting
// the credentials themselves.
func LookupOAuth(secrets interface{ GetSecret(string) (string, error) }) func() error {
	return func() error {
		_, err := secrets.GetSecret(SecretName)
		return err
	}
}
