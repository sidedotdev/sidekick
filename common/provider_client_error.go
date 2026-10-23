package common

import (
	"fmt"

	"go.temporal.io/sdk/temporal"
)

// ProviderClientErrorType is the temporal.ApplicationError type for requests a
// model provider rejected with a 4xx status that is neither an auth failure
// nor a rate limit. Such rejections are deterministic for a given request, so
// retrying the activity unchanged cannot succeed; the user decides whether to
// retry.
const ProviderClientErrorType = "provider_client_error"

// NewProviderClientError returns a non-retryable application error wrapping
// cause. Like provider auth errors, it must be returned from activities
// unwrapped.
func NewProviderClientError(provider string, statusCode int, cause error) error {
	return temporal.NewNonRetryableApplicationError(
		fmt.Sprintf("%s rejected the request with HTTP %d", provider, statusCode),
		ProviderClientErrorType, cause)
}
