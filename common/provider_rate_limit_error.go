package common

import (
	"errors"
	"fmt"
	"time"

	"go.temporal.io/sdk/temporal"
)

// ProviderRateLimitErrorType is the temporal.ApplicationError type for
// requests a model provider rejected because a rate, usage or spend limit was
// exceeded. Such limits rarely clear within an activity retry window, so the
// error is non-retryable at the activity level and the workflow decides when
// to try again.
const ProviderRateLimitErrorType = "provider_rate_limit_error"

type ProviderRateLimitErrorDetails struct {
	Provider string `json:"provider"`
	// RetryAfter is the wait the provider asked for before retrying, as
	// reported via response headers or error details. Zero when unknown.
	RetryAfter time.Duration `json:"retryAfter"`
}

func (d ProviderRateLimitErrorDetails) describe() string {
	if d.RetryAfter > 0 {
		return fmt.Sprintf("%s rate limit exceeded, retry after %s", d.Provider, d.RetryAfter.Round(time.Second))
	}
	return fmt.Sprintf("%s rate limit exceeded", d.Provider)
}

// NewProviderRateLimitError returns a non-retryable application error carrying
// the details, so they survive activity boundaries. Like provider auth errors,
// it must be returned from activities unwrapped.
func NewProviderRateLimitError(details ProviderRateLimitErrorDetails, cause error) error {
	return temporal.NewNonRetryableApplicationError(details.describe(), ProviderRateLimitErrorType, cause, details)
}

// AsProviderRateLimitError extracts provider rate limit error details from
// anywhere in the error chain.
func AsProviderRateLimitError(err error) (*ProviderRateLimitErrorDetails, bool) {
	for err != nil {
		var appErr *temporal.ApplicationError
		if !errors.As(err, &appErr) {
			return nil, false
		}
		if appErr.Type() == ProviderRateLimitErrorType && appErr.HasDetails() {
			var details ProviderRateLimitErrorDetails
			if appErr.Details(&details) == nil {
				return &details, true
			}
		}
		err = appErr.Unwrap()
	}
	return nil, false
}
