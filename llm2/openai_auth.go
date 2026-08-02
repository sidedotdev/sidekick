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

func openAICredentialsForRequest(
	secretManager secret_manager.SecretManager,
	providerName string,
	authType common.ProviderAuthType,
) (openAIRequestCredentials, error) {
	apiKeySecretName := fmt.Sprintf("%s_API_KEY", providerName)
	oauthSecretName := openai_oauth.SecretName

	switch common.NormalizeProviderAuthType(string(authType)) {
	case common.ProviderAuthTypeAPI:
		token, err := secretManager.GetSecret(apiKeySecretName)
		if err != nil {
			return openAIRequestCredentials{}, err
		}
		return openAIRequestCredentials{
			token:    token,
			authType: common.ProviderAuthTypeAPI,
		}, nil
	case common.ProviderAuthTypeSubscription:
		credentials, err := getOpenAIOAuthCredentials(secretManager, oauthSecretName)
		if err != nil {
			if errors.Is(err, secret_manager.ErrSecretNotFound) {
				return openAIRequestCredentials{}, fmt.Errorf("OpenAI subscription auth requires OAuth credentials: %w", err)
			}
			return openAIRequestCredentials{}, err
		}
		return credentials, nil
	case common.ProviderAuthTypeAny:
		credentials, err := getOpenAIOAuthCredentials(secretManager, oauthSecretName)
		if err == nil {
			return credentials, nil
		}
		if !errors.Is(err, secret_manager.ErrSecretNotFound) {
			return openAIRequestCredentials{}, err
		}

		token, err := secretManager.GetSecret(apiKeySecretName)
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
) (openAIRequestCredentials, error) {
	rawCredentials, err := secretManager.GetSecret(secretName)
	if err != nil {
		return openAIRequestCredentials{}, err
	}
	if rawCredentials == "" {
		return openAIRequestCredentials{}, fmt.Errorf("OpenAI OAuth secret is empty")
	}

	credentials, err := openai_oauth.ParseCredentials(rawCredentials)
	if err != nil {
		return openAIRequestCredentials{}, err
	}

	return openAIRequestCredentials{
		token:     credentials.AccessToken,
		accountID: credentials.AccountID,
		useOAuth:  true,
		authType:  common.ProviderAuthTypeSubscription,
	}, nil
}
