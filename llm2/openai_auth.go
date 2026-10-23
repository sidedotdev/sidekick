package llm2

import (
	"errors"
	"fmt"
	"sidekick/common"
	"sidekick/openai_oauth"
	"sidekick/secret_manager"
)

const openAIChatGPTResponsesBaseURL = "https://chatgpt.com/backend-api/codex"

type openAIRequestCredentials struct {
	token     string
	accountID string
	useOAuth  bool
	authType  common.ProviderAuthType
}

// openAICredentialsForRequest resolves credentials for the configured provider
// name (not normalized); credential problems surface as provider auth errors
// attributed to that provider.
func openAICredentialsForRequest(
	secretManager secret_manager.SecretManager,
	provider string,
	authType common.ProviderAuthType,
) (openAIRequestCredentials, error) {
	oauthSecretName := openai_oauth.SecretName
	normalizedAuthType := common.NormalizeProviderAuthType(string(authType))

	switch normalizedAuthType {
	case common.ProviderAuthTypeAPI:
		token, err := providerAPIKey(secretManager, provider, normalizedAuthType)
		if err != nil {
			return openAIRequestCredentials{}, err
		}
		return openAIRequestCredentials{
			token:    token,
			authType: common.ProviderAuthTypeAPI,
		}, nil
	case common.ProviderAuthTypeSubscription:
		credentials, err := getOpenAIOAuthCredentials(secretManager, oauthSecretName, provider, normalizedAuthType)
		if err != nil {
			if errors.Is(err, secret_manager.ErrSecretNotFound) {
				return openAIRequestCredentials{}, newCredentialAuthError(secretManager, provider, normalizedAuthType,
					common.ProviderAuthErrorReasonInvalidOAuth,
					fmt.Errorf("OpenAI subscription auth requires OAuth credentials: %w", err))
			}
			return openAIRequestCredentials{}, err
		}
		return credentials, nil
	case common.ProviderAuthTypeAny:
		credentials, err := getOpenAIOAuthCredentials(secretManager, oauthSecretName, provider, normalizedAuthType)
		if err == nil {
			return credentials, nil
		}
		if !errors.Is(err, secret_manager.ErrSecretNotFound) {
			return openAIRequestCredentials{}, err
		}

		token, err := providerAPIKey(secretManager, provider, normalizedAuthType)
		if err != nil {
			return openAIRequestCredentials{}, err
		}
		return openAIRequestCredentials{
			token:    token,
			authType: common.ProviderAuthTypeAPI,
		}, nil
	default:
		return openAIRequestCredentials{}, fmt.Errorf("unsupported OpenAI auth type: %s", authType)
	}
}

func getOpenAIOAuthCredentials(
	secretManager secret_manager.SecretManager,
	secretName string,
	provider string,
	authType common.ProviderAuthType,
) (openAIRequestCredentials, error) {
	rawCredentials, err := secretManager.GetSecret(secretName)
	if err != nil {
		return openAIRequestCredentials{}, common.ReattributeProviderAuthError(err, provider, authType)
	}
	if rawCredentials == "" {
		return openAIRequestCredentials{}, newCredentialAuthError(secretManager, provider, authType,
			common.ProviderAuthErrorReasonInvalidOAuth, errors.New("OpenAI OAuth secret is empty"))
	}

	credentials, err := openai_oauth.ParseCredentials(rawCredentials)
	if err != nil {
		return openAIRequestCredentials{}, newCredentialAuthError(secretManager, provider, authType,
			common.ProviderAuthErrorReasonInvalidOAuth, err)
	}

	return openAIRequestCredentials{
		token:     credentials.AccessToken,
		accountID: credentials.AccountID,
		useOAuth:  true,
		authType:  common.ProviderAuthTypeSubscription,
	}, nil
}
