package llm2

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sidekick/common"
	"sidekick/llm"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/temporal"
)

type anthropicRefreshTestTransport func(*http.Request) (*http.Response, error)

func (f anthropicRefreshTestTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type anthropicRefreshTimeoutError struct{}

func (*anthropicRefreshTimeoutError) Error() string   { return "refresh request timed out" }
func (*anthropicRefreshTimeoutError) Timeout() bool   { return true }
func (*anthropicRefreshTimeoutError) Temporary() bool { return true }

// Not parallel: the shared OAuth loader uses the default HTTP transport.
func TestAnthropicProvider_RefreshErrorClassification(t *testing.T) {
	expired, err := json.Marshal(llm.OAuthCredentials{
		AccessToken:  "old-access",
		RefreshToken: "old-refresh",
		ExpiresAt:    time.Now().Add(-time.Hour).Unix(),
	})
	require.NoError(t, err)

	tests := []struct {
		name         string
		transportErr error
		readErr      error
		status       int
		body         string
		wantAuth     bool
	}{
		{
			name:         "request deadline exceeded",
			transportErr: context.DeadlineExceeded,
		},
		{
			name:         "request timeout without context deadline",
			transportErr: &anthropicRefreshTimeoutError{},
		},
		{
			name:         "request canceled",
			transportErr: context.Canceled,
		},
		{
			name:    "response read deadline exceeded",
			status:  http.StatusOK,
			readErr: context.DeadlineExceeded,
		},
		{
			name:    "response read timeout without context deadline",
			status:  http.StatusOK,
			readErr: &anthropicRefreshTimeoutError{},
		},
		{
			name:    "response read canceled",
			status:  http.StatusOK,
			readErr: context.Canceled,
		},
		{
			name:     "refresh token rejected",
			status:   http.StatusBadRequest,
			body:     `{"error":"invalid_grant"}`,
			wantAuth: true,
		},
		{
			name:     "unauthorized",
			status:   http.StatusUnauthorized,
			body:     "Unauthorized",
			wantAuth: true,
		},
		{
			name:     "forbidden",
			status:   http.StatusForbidden,
			body:     "Forbidden",
			wantAuth: true,
		},
	}

	for _, authType := range []common.ProviderAuthType{
		common.ProviderAuthTypeSubscription,
		common.ProviderAuthTypeAny,
	} {
		t.Run(string(authType), func(t *testing.T) {
			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					requestCount := 0
					originalTransport := http.DefaultTransport
					http.DefaultTransport = anthropicRefreshTestTransport(func(req *http.Request) (*http.Response, error) {
						requestCount++
						if req.URL.Path != "/v1/oauth/token" {
							t.Errorf("unexpected request after refresh failure: %s", req.URL)
							return nil, errors.New("unexpected model request")
						}
						if tt.transportErr != nil {
							return nil, tt.transportErr
						}
						var body io.Reader = strings.NewReader(tt.body)
						if tt.readErr != nil {
							body = iotest.ErrReader(tt.readErr)
						}
						return &http.Response{
							StatusCode: tt.status,
							Header:     make(http.Header),
							Body:       io.NopCloser(body),
							Request:    req,
						}, nil
					})
					t.Cleanup(func() { http.DefaultTransport = originalTransport })

					provider := AnthropicProvider{AuthType: authType}
					request := providerAuthTestRequest("claude", "claude-opus-4-5", map[string]string{
						llm.AnthropicOAuthSecretName: string(expired),
						"CLAUDE_API_KEY":             "fallback-api-key",
					})
					response, err := provider.Stream(context.Background(), request, make(chan Event, 100))
					require.Error(t, err)
					assert.Nil(t, response)
					assert.Equal(t, 1, requestCount)
					assert.ErrorContains(t, err, "failed to refresh OAuth token")
					if tt.transportErr != nil {
						assert.ErrorIs(t, err, tt.transportErr)
					}
					if tt.readErr != nil {
						assert.ErrorIs(t, err, tt.readErr)
					}

					var appErr *temporal.ApplicationError
					if tt.wantAuth {
						assertProviderAuthError(t, err, &common.ProviderAuthErrorDetails{
							Provider:  "claude",
							ProfileId: common.DefaultProfileId,
							AuthType:  authType,
							Reason:    common.ProviderAuthErrorReasonInvalidOAuth,
						})
						require.ErrorAs(t, err, &appErr)
						assert.True(t, appErr.NonRetryable())
					} else {
						assertProviderAuthError(t, err, nil)
						assert.False(t, errors.As(err, &appErr), "unexpected application error: %v", err)
					}
				})
			}
		})
	}
}
