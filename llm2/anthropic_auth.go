package llm2

import (
	"fmt"
	"sidekick/common"
	"sidekick/llm"
	"sidekick/secret_manager"
)

func anthropicCredentialsForRequest(secretManager secret_manager.SecretManager, providerName string, authType common.ProviderAuthType) (*llm.OAuthCredentials, string, bool, error) {
	switch common.NormalizeProviderAuthType(string(authType)) {
	case common.ProviderAuthTypeAPI:
		token, err := secretManager.GetSecret(fmt.Sprintf("%s_API_KEY", providerName))
		if err != nil {
			return nil, "", false, err
		}
		return nil, token, false, nil
	case common.ProviderAuthTypeSubscription:
		oauthCreds, useOAuth, err := llm.GetAnthropicOAuthCredentials(secretManager)
		if err != nil {
			return nil, "", false, fmt.Errorf("failed to get Anthropic OAuth credentials: %w", err)
		}
		if !useOAuth || oauthCreds == nil {
			return nil, "", false, fmt.Errorf("Anthropic subscription auth requires OAuth credentials")
		}
		return oauthCreds, "", true, nil
	case common.ProviderAuthTypeAny:
		oauthCreds, useOAuth, err := llm.GetAnthropicOAuthCredentials(secretManager)
		if err != nil {
			return nil, "", false, fmt.Errorf("failed to get Anthropic OAuth credentials: %w", err)
		}
		if useOAuth && oauthCreds != nil {
			return oauthCreds, "", true, nil
		}

		token, err := secretManager.GetSecret(fmt.Sprintf("%s_API_KEY", providerName))
		if err != nil {
			return nil, "", false, err
		}
		return nil, token, false, nil
	default:
		return nil, "", false, fmt.Errorf("unsupported Anthropic auth type: %s", authType)
	}
}
