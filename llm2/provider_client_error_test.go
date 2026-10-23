package llm2

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sidekick/common"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/aws/aws-sdk-go-v2/aws"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	smithyhttp "github.com/aws/smithy-go/transport/http"
	"github.com/openai/openai-go/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
	"google.golang.org/genai"
)

func assertProviderClientError(t *testing.T, err error, provider string, status int) {
	t.Helper()
	require.Error(t, err)
	var appErr *temporal.ApplicationError
	require.True(t, errors.As(err, &appErr), "expected temporal application error, got %T: %v", err, err)
	assert.True(t, appErr.NonRetryable(), "client errors must not be retried by temporal")
	assert.Equal(t, common.ProviderClientErrorType, appErr.Type())
	assert.Contains(t, err.Error(), fmt.Sprintf("%s rejected the request with HTTP %d", provider, status))
}

// assertRetryable checks err is left for Temporal's activity retry policy: not
// a non-retryable application error of any kind.
func assertRetryable(t *testing.T, err error) {
	t.Helper()
	require.Error(t, err)
	var appErr *temporal.ApplicationError
	if errors.As(err, &appErr) {
		assert.False(t, appErr.NonRetryable(), "expected retryable error, got non-retryable %s: %v", appErr.Type(), err)
	}
}

func newBedrockHTTPError(status int) error {
	return &awshttp.ResponseError{ResponseError: &smithyhttp.ResponseError{
		Response: &smithyhttp.Response{Response: &http.Response{StatusCode: status}},
		Err:      fmt.Errorf("http %d", status),
	}}
}

func TestClassifyHTTPError_ClientErrors(t *testing.T) {
	t.Parallel()

	newOpenAIError := func(status int) error {
		return &openai.Error{StatusCode: status, Response: &http.Response{StatusCode: status, Header: http.Header{}}}
	}
	newAnthropicError := func(status int) error {
		return &anthropic.Error{StatusCode: status, Response: &http.Response{StatusCode: status, Header: http.Header{}}}
	}

	tests := []struct {
		name       string
		err        error
		wantType   string
		wantStatus int
	}{
		{name: "anthropic 400", err: newAnthropicError(http.StatusBadRequest), wantType: common.ProviderClientErrorType, wantStatus: 400},
		{name: "anthropic 403", err: newAnthropicError(http.StatusForbidden), wantType: common.ProviderClientErrorType, wantStatus: 403},
		{name: "anthropic 413", err: fmt.Errorf("wrapped: %w", newAnthropicError(http.StatusRequestEntityTooLarge)), wantType: common.ProviderClientErrorType, wantStatus: 413},
		{name: "openai 404", err: newOpenAIError(http.StatusNotFound), wantType: common.ProviderClientErrorType, wantStatus: 404},
		{name: "openai 422", err: newOpenAIError(http.StatusUnprocessableEntity), wantType: common.ProviderClientErrorType, wantStatus: 422},
		{name: "google 400 value", err: genai.APIError{Code: http.StatusBadRequest, Message: "bad request"}, wantType: common.ProviderClientErrorType, wantStatus: 400},
		{name: "google 404 pointer", err: &genai.APIError{Code: http.StatusNotFound}, wantType: common.ProviderClientErrorType, wantStatus: 404},
		{name: "anthropic 401 stays auth", err: newAnthropicError(http.StatusUnauthorized), wantType: common.ProviderAuthErrorType},
		{name: "google invalid key stays auth", err: genai.APIError{Code: http.StatusBadRequest, Details: []map[string]any{{"reason": googleAPIKeyInvalidReason}}}, wantType: common.ProviderAuthErrorType},
		{name: "openai 429 stays rate limit", err: newOpenAIError(http.StatusTooManyRequests), wantType: common.ProviderRateLimitErrorType},
		{name: "openai 408 stays retryable", err: newOpenAIError(http.StatusRequestTimeout)},
		{name: "anthropic 529 stays retryable", err: newAnthropicError(529)},
		{name: "openai 500 stays retryable", err: newOpenAIError(http.StatusInternalServerError)},
		{name: "google 503 stays retryable", err: genai.APIError{Code: http.StatusServiceUnavailable}},
		{name: "network error stays retryable", err: fmt.Errorf("dial: %w", context.DeadlineExceeded)},
		{name: "plain error stays retryable", err: errors.New("400 Bad Request")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			reported := fmt.Errorf("reported: %w", tt.err)
			auth := providerRequestAuth{provider: "testprovider", authType: common.ProviderAuthTypeAPI}

			got := auth.classifyHTTPError(tt.err, reported)
			assert.ErrorIs(t, got, reported, "reported error must be preserved as the cause")

			switch tt.wantType {
			case common.ProviderClientErrorType:
				assertProviderClientError(t, got, "testprovider", tt.wantStatus)
			case common.ProviderAuthErrorType:
				_, ok := common.AsProviderAuthError(got)
				assert.True(t, ok, "expected auth error, got: %v", got)
			case common.ProviderRateLimitErrorType:
				_, ok := common.AsProviderRateLimitError(got)
				assert.True(t, ok, "expected rate limit error, got: %v", got)
			default:
				assertRetryable(t, got)
				assert.Equal(t, reported, got)
			}
		})
	}
}

func TestClassifyBedrockError_ClientErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		err        error
		wantClient bool
		wantStatus int
	}{
		{name: "http 400", err: newBedrockHTTPError(http.StatusBadRequest), wantClient: true, wantStatus: 400},
		{name: "http 403", err: fmt.Errorf("bedrock: ConverseStream: %w", newBedrockHTTPError(http.StatusForbidden)), wantClient: true, wantStatus: 403},
		{name: "mid-stream validation", err: fmt.Errorf("bedrock: stream: %w", &types.ValidationException{Message: aws.String("bad input")}), wantClient: true, wantStatus: 400},
		{name: "http 408 stays retryable", err: newBedrockHTTPError(http.StatusRequestTimeout)},
		{name: "http 500 stays retryable", err: newBedrockHTTPError(http.StatusInternalServerError)},
		{name: "mid-stream model stream error stays retryable", err: &types.ModelStreamErrorException{Message: aws.String("stream broke")}},
		{name: "network error stays retryable", err: fmt.Errorf("bedrock: stream: %w", errors.New("connection reset"))},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := classifyBedrockError(tt.err)
			if tt.wantClient {
				assertProviderClientError(t, got, bedrockProviderName, tt.wantStatus)
				assert.ErrorIs(t, got, tt.err)
				return
			}
			assertRetryable(t, got)
			assert.Equal(t, tt.err, got)
		})
	}

	rateLimited := classifyBedrockError(newBedrockHTTPError(http.StatusTooManyRequests))
	_, ok := common.AsProviderRateLimitError(rateLimited)
	assert.True(t, ok, "429 must stay a rate limit, got: %v", rateLimited)
}

const providerClientErrorTestActivityName = "ProviderClientErrorTestActivity"

func providerClientErrorTestWorkflow(ctx workflow.Context) error {
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: time.Minute,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 3, InitialInterval: time.Millisecond},
	})
	return workflow.ExecuteActivity(ctx, providerClientErrorTestActivityName).Get(ctx, nil)
}

// Temporal must not spend activity retries on classified 4xx rejections, while
// statuses left unclassified still follow the activity's retry policy.
func TestClassifiedClientError_SkipsActivityRetries(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		status       int
		wantAttempts int32
	}{
		{name: "400 fails once", status: http.StatusBadRequest, wantAttempts: 1},
		{name: "408 is retried", status: http.StatusRequestTimeout, wantAttempts: 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			auth := providerRequestAuth{provider: "openai", authType: common.ProviderAuthTypeAPI}
			var attempts int32

			var suite testsuite.WorkflowTestSuite
			env := suite.NewTestWorkflowEnvironment()
			env.RegisterWorkflow(providerClientErrorTestWorkflow)
			env.RegisterActivityWithOptions(func(context.Context) error {
				atomic.AddInt32(&attempts, 1)
				cause := &openai.Error{StatusCode: tt.status, Response: &http.Response{StatusCode: tt.status, Header: http.Header{}}}
				return auth.classifyHTTPError(cause, fmt.Errorf("http %d", tt.status))
			}, activity.RegisterOptions{Name: providerClientErrorTestActivityName})

			env.ExecuteWorkflow(providerClientErrorTestWorkflow)

			require.True(t, env.IsWorkflowCompleted())
			require.Error(t, env.GetWorkflowError())
			assert.Equal(t, tt.wantAttempts, atomic.LoadInt32(&attempts))
		})
	}
}

func TestAnthropicProviderStream_BadRequestIsNonRetryable(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"type":"error","error":{"type":"invalid_request_error","message":"max_tokens: must be greater than 0"}}`))
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	provider := AnthropicProvider{BaseURL: server.URL, AuthType: common.ProviderAuthTypeAPI}
	request := providerAuthTestRequest("anthropic", "claude-fable-5", map[string]string{"ANTHROPIC_API_KEY": "test-api-key"})

	_, err := provider.Stream(ctx, request, make(chan Event, 1))
	assertProviderClientError(t, err, "anthropic", http.StatusBadRequest)
	assert.Contains(t, err.Error(), "max_tokens: must be greater than 0")
	_, isRateLimit := common.AsProviderRateLimitError(err)
	assert.False(t, isRateLimit)
	_, isAuth := common.AsProviderAuthError(err)
	assert.False(t, isAuth)
}
