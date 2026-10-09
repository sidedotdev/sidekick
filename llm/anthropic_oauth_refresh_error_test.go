package llm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sidekick/common"
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

// Not parallel: overrides the default HTTP transport.
func TestGetAnthropicOAuthCredentials_RefreshErrorClassification(t *testing.T) {
	expired, err := json.Marshal(OAuthCredentials{
		AccessToken:  "old-access",
		RefreshToken: "old-refresh",
		ExpiresAt:    time.Now().Add(-time.Hour).Unix(),
	})
	require.NoError(t, err)

	transportErr := errors.New("connection reset")
	tests := []struct {
		name         string
		status       int
		body         string
		transportErr error
		readErr      error
		wantAuth     bool
	}{
		{
			name:         "request timeout",
			transportErr: context.DeadlineExceeded,
		},
		{
			name:         "request canceled",
			transportErr: context.Canceled,
		},
		{
			name:         "transport failure",
			transportErr: transportErr,
		},
		{
			name:    "response read timeout",
			status:  http.StatusOK,
			readErr: context.DeadlineExceeded,
		},
		{
			name:    "truncated response",
			status:  http.StatusOK,
			readErr: io.ErrUnexpectedEOF,
		},
		{
			name:   "rate limited",
			status: http.StatusTooManyRequests,
			body:   `{"error":"rate_limit_exceeded"}`,
		},
		{
			name:   "server error",
			status: http.StatusInternalServerError,
			body:   `{"error":"server_error"}`,
		},
		{
			name:   "service unavailable",
			status: http.StatusServiceUnavailable,
			body:   "Service unavailable",
		},
		{
			name:   "server error with auth error body",
			status: http.StatusInternalServerError,
			body:   `{"error":"invalid_grant"}`,
		},
		{
			name:   "invalid request",
			status: http.StatusBadRequest,
			body:   `{"error":"invalid_request"}`,
		},
		{
			name:   "unrecognized bad request",
			status: http.StatusBadRequest,
			body:   "Bad request",
		},
		{
			name:   "malformed refresh response",
			status: http.StatusOK,
			body:   "not-json",
		},
		{
			name:     "refresh token rejected",
			status:   http.StatusBadRequest,
			body:     `{"error":"invalid_grant","error_description":"Refresh token not found or invalid"}`,
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

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			originalTransport := http.DefaultTransport
			http.DefaultTransport = anthropicRefreshTestTransport(func(req *http.Request) (*http.Response, error) {
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

			creds, useOAuth, err := GetAnthropicOAuthCredentials(profileMapSecretManager{
				secrets:   map[string]string{AnthropicOAuthSecretName: string(expired)},
				profileId: "work",
			})
			require.Error(t, err)
			assert.Nil(t, creds)
			assert.False(t, useOAuth)
			assert.ErrorContains(t, err, "failed to refresh OAuth token")
			if tt.transportErr != nil {
				assert.ErrorIs(t, err, tt.transportErr)
			}
			if tt.readErr != nil {
				assert.ErrorIs(t, err, tt.readErr)
			}

			details, isAuth := common.AsProviderAuthError(err)
			assert.Equal(t, tt.wantAuth, isAuth, "error: %v", err)
			var appErr *temporal.ApplicationError
			if tt.wantAuth {
				require.NotNil(t, details)
				assert.Equal(t, common.ProviderAuthErrorDetails{
					Provider:  "anthropic",
					ProfileId: "work",
					AuthType:  common.ProviderAuthTypeSubscription,
					Reason:    common.ProviderAuthErrorReasonInvalidOAuth,
				}, *details)
				require.ErrorAs(t, err, &appErr)
				assert.True(t, appErr.NonRetryable())
			} else if errors.As(err, &appErr) {
				assert.False(t, appErr.NonRetryable())
			}
		})
	}
}
