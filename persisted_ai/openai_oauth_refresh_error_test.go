package persisted_ai

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sidekick/common"
	"sidekick/llm2"
	"sidekick/openai_oauth"
	"strings"
	"sync/atomic"
	"testing"
	"testing/iotest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/temporal"
)

type openAIRefreshTestTransport func(*http.Request) (*http.Response, error)

func (f openAIRefreshTestTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

// Not parallel: the OAuth loader's HTTP client is shared.
func TestOpenAIResponsesProvider_RefreshErrorClassification(t *testing.T) {
	expired, err := json.Marshal(openai_oauth.Credentials{
		AccessToken:  "old-access",
		RefreshToken: "old-refresh",
		ExpiresAt:    time.Now().Add(-time.Hour).Unix(),
		AccountID:    "account-id",
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
			transportErr: &net.DNSError{Err: "refresh request timed out", IsTimeout: true},
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
			readErr: &net.DNSError{Err: "refresh response timed out", IsTimeout: true},
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
					refreshRequests := 0
					originalClient := openai_oauth.HTTPClient
					openai_oauth.HTTPClient = &http.Client{
						Transport: openAIRefreshTestTransport(func(req *http.Request) (*http.Response, error) {
							refreshRequests++
							assert.Equal(t, "/oauth/token", req.URL.Path)
							assert.Equal(t, http.MethodPost, req.Method)
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
						}),
					}
					t.Cleanup(func() { openai_oauth.HTTPClient = originalClient })

					var modelRequests atomic.Int32
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						modelRequests.Add(1)
						w.Header().Set("Retry-After", "0")
						http.Error(w, "unexpected model request", http.StatusTooManyRequests)
					}))
					t.Cleanup(server.Close)

					provider := llm2.OpenAIResponsesProvider{
						BaseURL:  server.URL,
						AuthType: authType,
					}
					request := llm2.StreamRequest{
						Messages: []llm2.Message{{
							Role:    llm2.RoleUser,
							Content: []llm2.ContentBlock{{Type: llm2.ContentBlockTypeText, Text: "Hello"}},
						}},
						Options: llm2.Options{
							ModelConfig: common.ModelConfig{
								Provider: "openai",
								Model:    "gpt-5-codex",
							},
							MaxTokens: 1024,
						},
						SecretManager: newOpenAIOAuthSecretWrapper(
							context.Background(),
							openAIOAuthWrapperTestSecretManager{
								secrets: map[string]string{
									openai_oauth.SecretName: string(expired),
									"OPENAI_API_KEY":        "fallback-api-key",
								},
							},
						),
					}

					response, err := provider.Stream(context.Background(), request, make(chan llm2.Event, 100))
					require.Error(t, err)
					assert.Nil(t, response)
					assert.Equal(t, 1, refreshRequests)
					assert.Zero(t, modelRequests.Load())
					assert.ErrorContains(t, err, "failed to refresh OpenAI OAuth token")
					if tt.transportErr != nil {
						assert.ErrorIs(t, err, tt.transportErr)
					}
					if tt.readErr != nil {
						assert.ErrorIs(t, err, tt.readErr)
					}

					details, isAuth := common.AsProviderAuthError(err)
					var appErr *temporal.ApplicationError
					if tt.wantAuth {
						require.True(t, isAuth, "expected provider auth error, got: %v", err)
						assert.Equal(t, &common.ProviderAuthErrorDetails{
							Provider:  "openai",
							ProfileId: common.DefaultProfileId,
							AuthType:  authType,
							Reason:    common.ProviderAuthErrorReasonInvalidOAuth,
						}, details)
						require.ErrorAs(t, err, &appErr)
						assert.True(t, appErr.NonRetryable())
					} else {
						assert.False(t, isAuth, "unexpected provider auth error: %v", err)
						assert.False(t, errors.As(err, &appErr), "unexpected application error: %v", err)
					}
				})
			}
		})
	}
}
