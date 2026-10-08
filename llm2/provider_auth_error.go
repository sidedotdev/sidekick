package llm2

import (
	"errors"
	"net/http"
	"sidekick/common"
	"sidekick/secret_manager"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/openai/openai-go/v3"
	"google.golang.org/genai"
)

// Gemini rejects invalid API keys with HTTP 400 rather than 401, identifying
// them via this google.rpc.ErrorInfo reason.
const googleAPIKeyInvalidReason = "API_KEY_INVALID"

// providerRequestAuth identifies the credential used for a provider request so
// authentication rejections can be attributed to it.
type providerRequestAuth struct {
	provider  string
	profileId string
	authType  common.ProviderAuthType
	useOAuth  bool
}

func newProviderRequestAuth(secretManager secret_manager.SecretManager, provider string, authType common.ProviderAuthType, useOAuth bool) providerRequestAuth {
	return providerRequestAuth{
		provider:  provider,
		profileId: secret_manager.ProfileIdOf(secretManager),
		authType:  common.NormalizeProviderAuthType(string(authType)),
		useOAuth:  useOAuth,
	}
}

// classifyHTTPError returns reportedErr as a provider auth error when err is a
// provider authentication rejection, otherwise reportedErr unchanged.
// reportedErr lets callers keep formatting that may drop the SDK error type.
func (a providerRequestAuth) classifyHTTPError(err, reportedErr error) error {
	if !isProviderAuthRejection(err) {
		return reportedErr
	}
	reason := common.ProviderAuthErrorReasonInvalidAPIKey
	if a.useOAuth {
		reason = common.ProviderAuthErrorReasonInvalidOAuth
	}
	return common.NewProviderAuthError(common.ProviderAuthErrorDetails{
		Provider:  a.provider,
		ProfileId: a.profileId,
		AuthType:  a.authType,
		Reason:    reason,
	}, reportedErr)
}

func isProviderAuthRejection(err error) bool {
	var anthropicErr *anthropic.Error
	if errors.As(err, &anthropicErr) {
		return anthropicErr.StatusCode == http.StatusUnauthorized
	}
	var openaiErr *openai.Error
	if errors.As(err, &openaiErr) {
		return openaiErr.StatusCode == http.StatusUnauthorized
	}
	var googleErr genai.APIError
	if errors.As(err, &googleErr) {
		return isGoogleAuthRejection(googleErr)
	}
	var googleErrPtr *genai.APIError
	if errors.As(err, &googleErrPtr) && googleErrPtr != nil {
		return isGoogleAuthRejection(*googleErrPtr)
	}
	return false
}

func isGoogleAuthRejection(apiErr genai.APIError) bool {
	if apiErr.Code == http.StatusUnauthorized {
		return true
	}
	for _, detail := range apiErr.Details {
		if reason, _ := detail["reason"].(string); reason == googleAPIKeyInvalidReason {
			return true
		}
	}
	return false
}

func newCredentialAuthError(secretManager secret_manager.SecretManager, provider string, authType common.ProviderAuthType, reason common.ProviderAuthErrorReason, cause error) error {
	return common.NewProviderAuthError(common.ProviderAuthErrorDetails{
		Provider:  provider,
		ProfileId: secret_manager.ProfileIdOf(secretManager),
		AuthType:  common.NormalizeProviderAuthType(string(authType)),
		Reason:    reason,
	}, cause)
}

// apiKeyLookupError reports a missing API key as a provider auth error and
// passes other secret lookup failures through.
func apiKeyLookupError(secretManager secret_manager.SecretManager, provider string, authType common.ProviderAuthType, err error) error {
	if errors.Is(err, secret_manager.ErrSecretNotFound) {
		return newCredentialAuthError(secretManager, provider, authType, common.ProviderAuthErrorReasonMissingAPIKey, err)
	}
	return err
}

func providerAPIKey(secretManager secret_manager.SecretManager, provider string, authType common.ProviderAuthType) (string, error) {
	secretName := common.ModelConfig{Provider: provider}.NormalizedProviderName() + "_API_KEY"
	token, err := secretManager.GetSecret(secretName)
	if err != nil {
		return "", apiKeyLookupError(secretManager, provider, authType, err)
	}
	return token, nil
}
