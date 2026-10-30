package dev

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"sidekick/common"
	"sidekick/llm"
	"sidekick/secret_manager"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/temporal"
)

type toolNameMapperRefreshTransport func(*http.Request) (*http.Response, error)

func (f toolNameMapperRefreshTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

// Not parallel: the shared OAuth loader uses the default HTTP transport.
func TestResolveToolNameMappingActivity_RefreshErrorClassification(t *testing.T) {
	tests := []struct {
		name         string
		transportErr error
		readErr      error
		status       int
		body         string
		wantAuth     bool
	}{
		{name: "deadline", transportErr: context.DeadlineExceeded},
		{name: "canceled", transportErr: context.Canceled},
		{name: "network timeout", transportErr: &net.DNSError{Err: "timeout", IsTimeout: true}},
		{name: "DNS failure", transportErr: &net.DNSError{Err: "no such host", IsNotFound: true}},
		{name: "connection failure", transportErr: errors.New("connection refused")},
		{name: "read deadline", status: http.StatusOK, readErr: context.DeadlineExceeded},
		{name: "truncated response", status: http.StatusOK, readErr: io.ErrUnexpectedEOF},
		{name: "rate limited", status: http.StatusTooManyRequests, body: `{"error":"rate_limit_error"}`},
		{name: "server error", status: http.StatusInternalServerError, body: `{"error":"invalid_grant"}`},
		{name: "gateway unavailable", status: http.StatusBadGateway, body: "Bad Gateway"},
		{name: "invalid request", status: http.StatusBadRequest, body: `{"error":"invalid_request"}`},
		{name: "unknown bad request", status: http.StatusBadRequest, body: "Bad Request"},
		{name: "malformed response", status: http.StatusOK, body: "not-json"},
		{name: "invalid grant", status: http.StatusBadRequest, body: `{"error":"invalid_grant"}`, wantAuth: true},
		{name: "unauthorized", status: http.StatusUnauthorized, body: "Unauthorized", wantAuth: true},
		{name: "forbidden", status: http.StatusForbidden, body: "Forbidden", wantAuth: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			requestCount := 0
			originalTransport := http.DefaultTransport
			http.DefaultTransport = toolNameMapperRefreshTransport(func(req *http.Request) (*http.Response, error) {
				requestCount++
				assert.Equal(t, "/v1/oauth/token", req.URL.Path)
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

			result, err := ResolveToolNameMappingActivity(context.Background(), ResolveToolNameMappingInput{
				Secrets: secret_manager.SecretManagerContainer{
					SecretManager: toolNameMapperAuthTestSecretManager{
						llm.AnthropicOAuthSecretName: `{"access_token":"old-access","refresh_token":"old-refresh","expires_at":1}`,
					},
				},
			})
			require.Error(t, err)
			assert.False(t, result.UseMapping)
			assert.Equal(t, 1, requestCount)
			assert.ErrorContains(t, err, "failed to refresh OAuth token")
			if tt.transportErr != nil {
				assert.ErrorIs(t, err, tt.transportErr)
			}
			if tt.readErr != nil {
				assert.ErrorIs(t, err, tt.readErr)
			}

			details, isAuth := common.AsProviderAuthError(err)
			require.Equal(t, tt.wantAuth, isAuth, "unexpected error classification: %v", err)
			if tt.wantAuth {
				appErr, ok := err.(*temporal.ApplicationError)
				require.True(t, ok, "auth errors must survive the activity boundary")
				assert.True(t, appErr.NonRetryable())
				assert.Equal(t, "anthropic", details.Provider)
				assert.Equal(t, common.ProviderAuthErrorReasonInvalidOAuth, details.Reason)
			} else {
				var appErr *temporal.ApplicationError
				assert.False(t, errors.As(err, &appErr))
				assert.NotContains(t, err.Error(), "invalid or expired")
			}
		})
	}
}
