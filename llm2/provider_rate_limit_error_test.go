package llm2

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sidekick/common"
	"strconv"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	smithyhttp "github.com/aws/smithy-go/transport/http"
	"github.com/openai/openai-go/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/temporal"
	"google.golang.org/genai"
)

func assertProviderRateLimitError(t *testing.T, err error, provider string, wantRetryAfter time.Duration) {
	t.Helper()
	require.Error(t, err)
	var appErr *temporal.ApplicationError
	require.True(t, errors.As(err, &appErr), "expected temporal application error, got %T: %v", err, err)
	assert.True(t, appErr.NonRetryable(), "rate limit errors must not be retried by temporal")
	assert.Equal(t, common.ProviderRateLimitErrorType, appErr.Type())
	details, ok := common.AsProviderRateLimitError(err)
	require.True(t, ok)
	assert.Equal(t, provider, details.Provider)
	assert.Equal(t, wantRetryAfter, details.RetryAfter)
}

func TestAnthropicProviderStream_RateLimit(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		headers        map[string]string
		wantRetryAfter time.Duration
	}{
		{
			name:           "no retry hints",
			wantRetryAfter: 0,
		},
		{
			name:           "retry-after seconds",
			headers:        map[string]string{"Retry-After": "37"},
			wantRetryAfter: 37 * time.Second,
		},
		{
			name: "subscription unified reset epoch seconds",
			headers: map[string]string{
				"anthropic-ratelimit-unified-status": "rejected",
				"anthropic-ratelimit-unified-reset":  "4102444800",
			},
			wantRetryAfter: time.Until(time.Unix(4102444800, 0)),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				for k, v := range tt.headers {
					w.Header().Set(k, v)
				}
				w.WriteHeader(http.StatusTooManyRequests)
				_, _ = w.Write([]byte(`{"type":"error","error":{"type":"rate_limit_error","message":"This request would exceed your account's rate limit. Please try again later."}}`))
			}))
			defer server.Close()

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			provider := AnthropicProvider{
				BaseURL:  server.URL,
				AuthType: common.ProviderAuthTypeAPI,
			}
			request := providerAuthTestRequest("anthropic", "claude-fable-5", map[string]string{"ANTHROPIC_API_KEY": "test-api-key"})

			_, err := provider.Stream(ctx, request, make(chan Event, 1))
			require.Error(t, err)
			assert.Contains(t, err.Error(), "rate_limit_error")

			details, ok := common.AsProviderRateLimitError(err)
			require.True(t, ok, "expected provider rate limit error, got: %v", err)
			assert.Equal(t, "anthropic", details.Provider)
			// Timestamp-derived waits depend on the clock between the test's
			// expectation and the provider's parse, so allow a small tolerance.
			assert.InDelta(t, tt.wantRetryAfter, details.RetryAfter, float64(5*time.Second))
			var appErr *temporal.ApplicationError
			require.True(t, errors.As(err, &appErr))
			assert.True(t, appErr.NonRetryable())
		})
	}
}

func TestAnthropicProviderStream_OverloadedIsNotRateLimit(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(529)
		_, _ = w.Write([]byte(`{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`))
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	provider := AnthropicProvider{BaseURL: server.URL, AuthType: common.ProviderAuthTypeAPI}
	request := providerAuthTestRequest("anthropic", "claude-fable-5", map[string]string{"ANTHROPIC_API_KEY": "test-api-key"})

	_, err := provider.Stream(ctx, request, make(chan Event, 1))
	require.Error(t, err)
	_, ok := common.AsProviderRateLimitError(err)
	assert.False(t, ok, "transient overload must stay retryable")
}

func TestAnthropicRetryAfter(t *testing.T) {
	t.Parallel()

	now := time.Date(2030, 1, 1, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name    string
		headers map[string]string
		want    time.Duration
	}{
		{
			name: "retry-after wins over reset headers",
			headers: map[string]string{
				"Retry-After":                       "37",
				"anthropic-ratelimit-unified-reset": "1893499200",
			},
			want: 37 * time.Second,
		},
		{
			name:    "retry-after http date",
			headers: map[string]string{"Retry-After": now.Add(90 * time.Second).Format(http.TimeFormat)},
			want:    90 * time.Second,
		},
		{
			name:    "retry-after in the past is zero",
			headers: map[string]string{"Retry-After": now.Add(-time.Minute).Format(http.TimeFormat)},
			want:    0,
		},
		{
			name: "unified reset epoch seconds",
			headers: map[string]string{
				"anthropic-ratelimit-unified-status": "rejected",
				"anthropic-ratelimit-unified-reset":  strconv.FormatInt(now.Add(5*time.Hour).Unix(), 10),
			},
			want: 5 * time.Hour,
		},
		{
			name: "latest exhausted api window reset",
			headers: map[string]string{
				"anthropic-ratelimit-requests-remaining":      "0",
				"anthropic-ratelimit-requests-reset":          now.Add(20 * time.Second).Format(time.RFC3339),
				"anthropic-ratelimit-input-tokens-remaining":  "0",
				"anthropic-ratelimit-input-tokens-reset":      now.Add(45 * time.Second).Format(time.RFC3339),
				"anthropic-ratelimit-output-tokens-remaining": "8000",
				"anthropic-ratelimit-output-tokens-reset":     now.Add(10 * time.Minute).Format(time.RFC3339),
			},
			want: 45 * time.Second,
		},
		{
			name: "windows with remaining budget are ignored",
			headers: map[string]string{
				"anthropic-ratelimit-requests-remaining": "5",
				"anthropic-ratelimit-requests-reset":     now.Add(time.Minute).Format(time.RFC3339),
			},
			want: 0,
		},
		{
			name:    "no hints",
			headers: nil,
			want:    0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			header := http.Header{}
			for k, v := range tt.headers {
				header.Set(k, v)
			}
			assert.Equal(t, tt.want, anthropicRetryAfter(header, now))
		})
	}
}

func TestProviderRateLimitRetryAfter(t *testing.T) {
	t.Parallel()

	newOpenAIError := func(status int, headers map[string]string) *openai.Error {
		resp := &http.Response{StatusCode: status, Header: http.Header{}}
		for k, v := range headers {
			resp.Header.Set(k, v)
		}
		return &openai.Error{StatusCode: status, Response: resp}
	}

	tests := []struct {
		name           string
		err            error
		wantRateLimit  bool
		wantRetryAfter time.Duration
	}{
		{
			name: "openai retry-after header",
			err:  newOpenAIError(http.StatusTooManyRequests, map[string]string{"Retry-After": "12"}),

			wantRateLimit:  true,
			wantRetryAfter: 12 * time.Second,
		},
		{
			name: "openai reset durations take the longest",
			err: newOpenAIError(http.StatusTooManyRequests, map[string]string{
				"x-ratelimit-reset-requests": "20ms",
				"x-ratelimit-reset-tokens":   "6m0s",
			}),
			wantRateLimit:  true,
			wantRetryAfter: 6 * time.Minute,
		},
		{
			name:          "openai 429 without hints",
			err:           newOpenAIError(http.StatusTooManyRequests, nil),
			wantRateLimit: true,
		},
		{
			name:          "openai 500 is not a rate limit",
			err:           newOpenAIError(http.StatusInternalServerError, map[string]string{"Retry-After": "12"}),
			wantRateLimit: false,
		},
		{
			name: "google resource exhausted with retry info",
			err: genai.APIError{Code: http.StatusTooManyRequests, Status: "RESOURCE_EXHAUSTED", Details: []map[string]any{
				{"@type": "type.googleapis.com/google.rpc.RetryInfo", "retryDelay": "32s"},
			}},
			wantRateLimit:  true,
			wantRetryAfter: 32 * time.Second,
		},
		{
			name:          "google resource exhausted pointer without retry info",
			err:           &genai.APIError{Code: http.StatusTooManyRequests, Status: "RESOURCE_EXHAUSTED"},
			wantRateLimit: true,
		},
		{
			name:          "google invalid argument is not a rate limit",
			err:           genai.APIError{Code: http.StatusBadRequest, Status: "INVALID_ARGUMENT"},
			wantRateLimit: false,
		},
		{
			name:          "bedrock throttling exception",
			err:           fmt.Errorf("bedrock: stream: %w", &types.ThrottlingException{Message: aws.String("Too many requests, please wait before trying again.")}),
			wantRateLimit: true,
		},
		{
			name: "bedrock http 429",
			err: &awshttp.ResponseError{ResponseError: &smithyhttp.ResponseError{
				Response: &smithyhttp.Response{Response: &http.Response{StatusCode: http.StatusTooManyRequests}},
				Err:      errors.New("too many requests"),
			}},
			wantRateLimit: true,
		},
		{
			name: "bedrock http 400 is not a rate limit",
			err: &awshttp.ResponseError{ResponseError: &smithyhttp.ResponseError{
				Response: &smithyhttp.Response{Response: &http.Response{StatusCode: http.StatusBadRequest}},
				Err:      errors.New("validation"),
			}},
			wantRateLimit: false,
		},
		{
			name:          "plain error mentioning 429",
			err:           errors.New("429 Too Many Requests"),
			wantRateLimit: false,
		},
		{
			name:          "nil",
			err:           nil,
			wantRateLimit: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			retryAfter, ok := providerRateLimitRetryAfter(tt.err)
			assert.Equal(t, tt.wantRateLimit, ok)
			assert.Equal(t, tt.wantRetryAfter, retryAfter)
		})
	}
}

func TestClassifyBedrockError(t *testing.T) {
	t.Parallel()

	throttled := fmt.Errorf("bedrock: stream: %w", &types.ThrottlingException{Message: aws.String("Too many requests")})
	err := classifyBedrockError(throttled)
	assertProviderRateLimitError(t, err, bedrockProviderName, 0)
	assert.ErrorIs(t, err, throttled)

	other := fmt.Errorf("bedrock: stream: %w", errors.New("connection reset"))
	assert.Equal(t, other, classifyBedrockError(other))
	assert.NoError(t, classifyBedrockError(nil))
}

func TestClassifyHTTPError_RateLimitKeepsReportedError(t *testing.T) {
	t.Parallel()

	cause := &openai.Error{
		StatusCode: http.StatusTooManyRequests,
		Response:   &http.Response{StatusCode: http.StatusTooManyRequests, Header: http.Header{"Retry-After": []string{"5"}}},
	}
	reported := errors.New("wrapped: insufficient_quota")
	auth := providerRequestAuth{provider: "openai", authType: common.ProviderAuthTypeAPI}

	err := auth.classifyHTTPError(cause, reported)
	assertProviderRateLimitError(t, err, "openai", 5*time.Second)
	assert.ErrorIs(t, err, reported)
	assert.Contains(t, err.Error(), "insufficient_quota")
}
