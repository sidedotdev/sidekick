package llm2

import (
	"errors"
	"net/http"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/openai/openai-go/v3"
	"go.temporal.io/sdk/temporal"
	"google.golang.org/genai"
)

const providerBadRequestErrorType = "provider_bad_request"

// Classification uses the SDK error because formatting reportedErr can discard
// its HTTP status. The application error must remain top-level for Temporal.
func classifyBadRequestError(err, reportedErr error) error {
	if !isProviderBadRequest(err) {
		return reportedErr
	}
	return temporal.NewNonRetryableApplicationError(reportedErr.Error(), providerBadRequestErrorType, reportedErr)
}

func isProviderBadRequest(err error) bool {
	var anthropicErr *anthropic.Error
	if errors.As(err, &anthropicErr) {
		return anthropicErr.StatusCode == http.StatusBadRequest
	}
	var openaiErr *openai.Error
	if errors.As(err, &openaiErr) {
		return openaiErr.StatusCode == http.StatusBadRequest
	}
	var googleErr genai.APIError
	if errors.As(err, &googleErr) {
		return googleErr.Code == http.StatusBadRequest
	}
	var googleErrPtr *genai.APIError
	if errors.As(err, &googleErrPtr) && googleErrPtr != nil {
		return googleErrPtr.Code == http.StatusBadRequest
	}
	var httpErr interface {
		HTTPStatusCode() int
	}
	if errors.As(err, &httpErr) {
		return httpErr.HTTPStatusCode() == http.StatusBadRequest
	}
	return false
}
