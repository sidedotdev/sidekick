package llm2

import (
	"errors"
	"net/http"
	"sidekick/common"
	"strconv"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"github.com/openai/openai-go/v3"
	"google.golang.org/genai"
)

// Anthropic reports per-minute API limits as RFC 3339 reset timestamps per
// dimension, alongside a remaining count identifying the exhausted one.
var anthropicRateLimitDimensions = []string{"requests", "tokens", "input-tokens", "output-tokens"}

// Subscription (OAuth) rate limits use a separate header family whose reset is
// in epoch seconds.
const (
	anthropicUnifiedRateLimitStatusHeader = "anthropic-ratelimit-unified-status"
	anthropicUnifiedRateLimitResetHeader  = "anthropic-ratelimit-unified-reset"
)

// OpenAI reports window resets as Go-style durations, e.g. "6m0s" or "20ms".
var openAIRateLimitResetHeaders = []string{"x-ratelimit-reset-requests", "x-ratelimit-reset-tokens"}

const googleRetryInfoType = "type.googleapis.com/google.rpc.RetryInfo"

// providerRateLimitRetryAfter reports whether err is a provider rate limit
// rejection (HTTP 429) and the wait the provider asked for, zero when it gave
// no usable hint.
func providerRateLimitRetryAfter(err error) (time.Duration, bool) {
	now := time.Now()
	var anthropicErr *anthropic.Error
	if errors.As(err, &anthropicErr) {
		if anthropicErr.StatusCode != http.StatusTooManyRequests {
			return 0, false
		}
		return anthropicRetryAfter(responseHeader(anthropicErr.Response), now), true
	}
	var openaiErr *openai.Error
	if errors.As(err, &openaiErr) {
		if openaiErr.StatusCode != http.StatusTooManyRequests {
			return 0, false
		}
		return openAIRetryAfter(responseHeader(openaiErr.Response), now), true
	}
	var googleErr genai.APIError
	if errors.As(err, &googleErr) {
		return googleRetryAfter(googleErr)
	}
	var googleErrPtr *genai.APIError
	if errors.As(err, &googleErrPtr) && googleErrPtr != nil {
		return googleRetryAfter(*googleErrPtr)
	}
	return 0, isBedrockRateLimitRejection(err)
}

// isBedrockRateLimitRejection covers throttling surfaced either as a modeled
// exception (also mid-stream) or as a raw HTTP 429. Bedrock gives no retry
// hint, and the AWS SDK has already exhausted its own quick retries by the
// time the error reaches us.
func isBedrockRateLimitRejection(err error) bool {
	var throttled *types.ThrottlingException
	if errors.As(err, &throttled) {
		return true
	}
	var responseErr *awshttp.ResponseError
	return errors.As(err, &responseErr) && responseErr.HTTPStatusCode() == http.StatusTooManyRequests
}

// classifyBedrockError returns err as a provider rate limit error when it is a
// Bedrock rate limit rejection, as a non-retryable provider client error for
// any other 4xx rejection except 408, and otherwise err unchanged.
func classifyBedrockError(err error) error {
	if isBedrockRateLimitRejection(err) {
		return common.NewProviderRateLimitError(common.ProviderRateLimitErrorDetails{Provider: bedrockProviderName}, err)
	}
	if status, ok := bedrockClientErrorStatus(err); ok {
		return common.NewProviderClientError(bedrockProviderName, status, err)
	}
	return err
}

func responseHeader(resp *http.Response) http.Header {
	if resp == nil {
		return http.Header{}
	}
	return resp.Header
}

func anthropicRetryAfter(header http.Header, now time.Time) time.Duration {
	if wait, ok := parseRetryAfterHeader(header.Get("Retry-After"), now); ok {
		return wait
	}
	if header.Get(anthropicUnifiedRateLimitStatusHeader) != "" || header.Get(anthropicUnifiedRateLimitResetHeader) != "" {
		if epoch, err := strconv.ParseInt(header.Get(anthropicUnifiedRateLimitResetHeader), 10, 64); err == nil {
			return positiveDuration(time.Unix(epoch, 0).Sub(now))
		}
	}
	var wait time.Duration
	for _, dimension := range anthropicRateLimitDimensions {
		if header.Get("anthropic-ratelimit-"+dimension+"-remaining") != "0" {
			continue
		}
		reset, err := time.Parse(time.RFC3339, header.Get("anthropic-ratelimit-"+dimension+"-reset"))
		if err != nil {
			continue
		}
		wait = max(wait, positiveDuration(reset.Sub(now)))
	}
	return wait
}

func openAIRetryAfter(header http.Header, now time.Time) time.Duration {
	if wait, ok := parseRetryAfterHeader(header.Get("Retry-After"), now); ok {
		return wait
	}
	var wait time.Duration
	for _, name := range openAIRateLimitResetHeaders {
		reset, err := time.ParseDuration(header.Get(name))
		if err != nil {
			continue
		}
		wait = max(wait, positiveDuration(reset))
	}
	return wait
}

func googleRetryAfter(apiErr genai.APIError) (time.Duration, bool) {
	if apiErr.Code != http.StatusTooManyRequests {
		return 0, false
	}
	for _, detail := range apiErr.Details {
		if detail["@type"] != googleRetryInfoType {
			continue
		}
		delay, _ := detail["retryDelay"].(string)
		if wait, err := time.ParseDuration(delay); err == nil {
			return positiveDuration(wait), true
		}
	}
	return 0, true
}

// parseRetryAfterHeader handles both forms allowed by RFC 9110: a delay in
// seconds or an HTTP date.
func parseRetryAfterHeader(value string, now time.Time) (time.Duration, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, false
	}
	if seconds, err := strconv.ParseFloat(value, 64); err == nil {
		return positiveDuration(time.Duration(seconds * float64(time.Second))), true
	}
	if date, err := http.ParseTime(value); err == nil {
		return positiveDuration(date.Sub(now)), true
	}
	return 0, false
}

func positiveDuration(d time.Duration) time.Duration {
	if d < 0 {
		return 0
	}
	return d
}
