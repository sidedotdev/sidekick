package llm2

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sidekick/common"
	"sidekick/llm"
	"sidekick/secret_manager"

	"go.temporal.io/sdk/temporal"
)

// anthropicCredentialsForRequest resolves credentials for the configured
// provider name (not normalized); credential problems surface as provider auth
// errors attributed to that provider.
func anthropicCredentialsForRequest(secretManager secret_manager.SecretManager, provider string, authType common.ProviderAuthType) (*llm.OAuthCredentials, string, bool, error) {
	normalizedAuthType := common.NormalizeProviderAuthType(string(authType))
	switch normalizedAuthType {
	case common.ProviderAuthTypeAPI:
		token, err := providerAPIKey(secretManager, provider, normalizedAuthType)
		if err != nil {
			return nil, "", false, err
		}
		return nil, token, false, nil
	case common.ProviderAuthTypeSubscription:
		oauthCreds, useOAuth, err := llm.GetAnthropicOAuthCredentials(secretManager)
		if err != nil {
			return nil, "", false, anthropicOAuthLookupError(err, provider, normalizedAuthType)
		}
		if !useOAuth || oauthCreds == nil {
			return nil, "", false, newCredentialAuthError(secretManager, provider, normalizedAuthType,
				common.ProviderAuthErrorReasonInvalidOAuth,
				errors.New("Anthropic subscription auth requires OAuth credentials"))
		}
		return oauthCreds, "", true, nil
	case common.ProviderAuthTypeAny:
		oauthCreds, useOAuth, err := llm.GetAnthropicOAuthCredentials(secretManager)
		if err != nil {
			return nil, "", false, anthropicOAuthLookupError(err, provider, normalizedAuthType)
		}
		if useOAuth && oauthCreds != nil {
			return oauthCreds, "", true, nil
		}

		token, err := providerAPIKey(secretManager, provider, normalizedAuthType)
		if err != nil {
			return nil, "", false, err
		}
		return nil, token, false, nil
	default:
		return nil, "", false, fmt.Errorf("unsupported Anthropic auth type: %s", authType)
	}
}

func anthropicOAuthLookupError(err error, provider string, authType common.ProviderAuthType) error {
	var appErr *temporal.ApplicationError
	if errors.As(err, &appErr) && appErr.Type() == common.ProviderAuthErrorType {
		var netErr net.Error
		if errors.Is(appErr, context.DeadlineExceeded) || errors.Is(appErr, context.Canceled) ||
			(errors.As(appErr, &netErr) && netErr.Timeout()) {
			// The shared credential loader also wraps interrupted refresh requests
			// as invalid credentials, but these failures do not justify reauthentication.
			err = appErr.Unwrap()
		} else {
			return common.ReattributeProviderAuthError(err, provider, authType)
		}
	}
	return fmt.Errorf("failed to get Anthropic OAuth credentials: %w", err)
}
