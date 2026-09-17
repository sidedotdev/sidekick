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

	var oauthCredentials openAIRequestCredentials
	oauthLoaded := false
	resolved, err := openai_oauth.ResolveAuthType(authType, func() error {
		var lookupErr error
		oauthCredentials, lookupErr = getOpenAIOAuthCredentials(secretManager, oauthSecretName)
		oauthLoaded = lookupErr == nil
		return lookupErr
	})
	if err != nil {
		return openAIRequestCredentials{}, err
	}
	switch resolved {
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
		if oauthLoaded {
			return oauthCredentials, nil
		}
		credentials, err := getOpenAIOAuthCredentials(secretManager, oauthSecretName)
		if err != nil {
			if errors.Is(err, secret_manager.ErrSecretNotFound) {
				return openAIRequestCredentials{}, fmt.Errorf("OpenAI subscription auth requires OAuth credentials: %w", err)
			}
			return openAIRequestCredentials{}, err
		}
		return credentials, nil
	default:
		return openAIRequestCredentials{}, fmt.Errorf("unsupported OpenAI auth type: %s", authType)
	}
}

// openAIRequestAuth resolves the credentials one request authenticates with.
// An auth token configured on a provider is a credential the caller already
// holds for the endpoint that provider points at, e.g. a gateway that injects
// the real provider credentials; no secret is read then, and the configured
// auth type alone decides how the request is shaped. That type must be
// explicit, since there is no credential document to infer it from.
func openAIRequestAuth(
	authToken string,
	secretManager secret_manager.SecretManager,
	providerName string,
	authType common.ProviderAuthType,
) (openAIRequestCredentials, error) {
	if authToken == "" {
		return openAICredentialsForRequest(secretManager, providerName, authType)
	}
	switch normalized := common.NormalizeProviderAuthType(string(authType)); normalized {
	case common.ProviderAuthTypeAPI, common.ProviderAuthTypeSubscription:
		return openAIRequestCredentials{
			token:    authToken,
			useOAuth: normalized == common.ProviderAuthTypeSubscription,
			authType: normalized,
		}, nil
	default:
		return openAIRequestCredentials{}, fmt.Errorf("a configured OpenAI auth token requires auth type %q or %q, got: %s", common.ProviderAuthTypeAPI, common.ProviderAuthTypeSubscription, authType)
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
