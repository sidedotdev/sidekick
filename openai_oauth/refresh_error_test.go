package openai_oauth

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

type refreshTestTransport func(*http.Request) (*http.Response, error)

func (f refreshTestTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

// Not parallel: overrides the package-level HTTP client.
func TestGetAndMaybeRefresh_RefreshErrorClassification(t *testing.T) {
	expired, err := json.Marshal(Credentials{
		AccessToken:  "old-access",
		RefreshToken: "old-refresh",
		ExpiresAt:    time.Now().Add(-time.Hour).Unix(),
		AccountID:    "acct-test",
	})
	require.NoError(t, err)

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
			transportErr: errors.New("connection reset"),
		},
		{
			name:    "response read timeout",
			status:  http.StatusOK,
			readErr: context.DeadlineExceeded,
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
			name:   "malformed refresh response",
			status: http.StatusOK,
			body:   "not-json",
		},
		{
			name:   "incomplete refresh response",
			status: http.StatusOK,
			body:   `{}`,
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

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			originalClient := HTTPClient
			HTTPClient = &http.Client{Transport: refreshTestTransport(func(req *http.Request) (*http.Response, error) {
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
			})}
			t.Cleanup(func() { HTTPClient = originalClient })

			creds, useOAuth, err := GetAndMaybeRefresh(context.Background(), &mockSecretManager{
				secrets: map[string]string{SecretName: string(expired)},
			})
			require.Error(t, err)
			assert.Nil(t, creds)
			assert.False(t, useOAuth)
			assert.ErrorContains(t, err, "failed to refresh OpenAI OAuth token")
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
					Provider:  "openai",
					ProfileId: common.DefaultProfileId,
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
