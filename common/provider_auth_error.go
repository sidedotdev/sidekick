package common

import (
	"errors"
	"fmt"

	"go.temporal.io/sdk/temporal"
)

// ProviderAuthErrorType is the temporal.ApplicationError type for failures
// caused by broken or missing provider credentials.
const ProviderAuthErrorType = "provider_auth_error"

type ProviderAuthErrorReason string

const (
	ProviderAuthErrorReasonInvalidOAuth  ProviderAuthErrorReason = "invalid_oauth"
	ProviderAuthErrorReasonMissingAPIKey ProviderAuthErrorReason = "missing_api_key"
	ProviderAuthErrorReasonInvalidAPIKey ProviderAuthErrorReason = "invalid_api_key"
)

type ProviderAuthErrorDetails struct {
	Provider  string                  `json:"provider"`
	ProfileId string                  `json:"profileId"`
	AuthType  ProviderAuthType        `json:"authType"`
	Reason    ProviderAuthErrorReason `json:"reason"`
}

func (d ProviderAuthErrorDetails) describe() string {
	subject := d.Provider
	if NormalizeProfileId(d.ProfileId) != DefaultProfileId {
		subject = fmt.Sprintf("%s (profile %s)", subject, d.ProfileId)
	}
	switch d.Reason {
	case ProviderAuthErrorReasonInvalidOAuth:
		return fmt.Sprintf("OAuth credential for %s is invalid or expired", subject)
	case ProviderAuthErrorReasonMissingAPIKey:
		return fmt.Sprintf("no API key found for %s", subject)
	case ProviderAuthErrorReasonInvalidAPIKey:
		return fmt.Sprintf("API key for %s was rejected", subject)
	default:
		return fmt.Sprintf("authentication failed for %s", subject)
	}
}

// NewProviderAuthError returns a non-retryable application error carrying the
// details, so they survive activity boundaries. It must be returned from
// activities unwrapped: Temporal only preserves the type and details of a
// top-level *temporal.ApplicationError.
func NewProviderAuthError(details ProviderAuthErrorDetails, cause error) error {
	return temporal.NewNonRetryableApplicationError(details.describe(), ProviderAuthErrorType, cause, details)
}

// AsProviderAuthError extracts provider auth error details from anywhere in
// the error chain.
func AsProviderAuthError(err error) (*ProviderAuthErrorDetails, bool) {
	for err != nil {
		var appErr *temporal.ApplicationError
		if !errors.As(err, &appErr) {
			return nil, false
		}
		if appErr.Type() == ProviderAuthErrorType && appErr.HasDetails() {
			var details ProviderAuthErrorDetails
			if appErr.Details(&details) == nil {
				return &details, true
			}
		}
		err = appErr.Unwrap()
	}
	return nil, false
}

// ReattributeProviderAuthError rebuilds a provider auth error at the top of
// the chain for the given provider and auth type, for callers that know which
// configured provider the credential was resolved for. Other errors are
// returned unchanged.
func ReattributeProviderAuthError(err error, provider string, authType ProviderAuthType) error {
	var appErr *temporal.ApplicationError
	if !errors.As(err, &appErr) || appErr.Type() != ProviderAuthErrorType {
		return err
	}
	details, ok := AsProviderAuthError(appErr)
	if !ok {
		return err
	}
	details.Provider = provider
	details.AuthType = authType
	return NewProviderAuthError(*details, appErr.Unwrap())
}
