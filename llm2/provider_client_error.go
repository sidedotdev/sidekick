package llm2

import (
	"errors"
	"net/http"

	"github.com/anthropics/anthropic-sdk-go"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"github.com/openai/openai-go/v3"
	"google.golang.org/genai"
)

// providerClientErrorStatus reports the HTTP status when err is a 4xx
// rejection other than 408 or 429, which a quick retry or a wait may fix.
// Auth rejections are expected to have been classified already.
func providerClientErrorStatus(err error) (int, bool) {
	status, ok := providerHTTPStatus(err)
	if !ok {
		return 0, false
	}
	return clientErrorStatus(status)
}

// bedrockClientErrorStatus is the Bedrock equivalent of
// providerClientErrorStatus. Exceptions delivered mid-stream carry no HTTP
// response; validation is the only 4xx the stream reports besides throttling.
func bedrockClientErrorStatus(err error) (int, bool) {
	var responseErr *awshttp.ResponseError
	if errors.As(err, &responseErr) {
		return clientErrorStatus(responseErr.HTTPStatusCode())
	}
	var validation *types.ValidationException
	if errors.As(err, &validation) {
		return http.StatusBadRequest, true
	}
	return 0, false
}

// clientErrorStatus passes through 4xx statuses other than request timeouts
// and rate limits, which may clear on their own.
func clientErrorStatus(status int) (int, bool) {
	if status < 400 || status >= 500 {
		return 0, false
	}
	if status == http.StatusRequestTimeout || status == http.StatusTooManyRequests {
		return 0, false
	}
	return status, true
}

func providerHTTPStatus(err error) (int, bool) {
	var anthropicErr *anthropic.Error
	if errors.As(err, &anthropicErr) {
		return anthropicErr.StatusCode, true
	}
	var openaiErr *openai.Error
	if errors.As(err, &openaiErr) {
		return openaiErr.StatusCode, true
	}
	var googleErr genai.APIError
	if errors.As(err, &googleErr) {
		return googleErr.Code, true
	}
	var googleErrPtr *genai.APIError
	if errors.As(err, &googleErrPtr) && googleErrPtr != nil {
		return googleErrPtr.Code, true
	}
	return 0, false
}
