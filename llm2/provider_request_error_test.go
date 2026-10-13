package llm2

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/temporal"
)

func TestProviderStream_BadRequestIsNonRetryable(t *testing.T) {
	t.Parallel()

	providers := []struct {
		name     string
		model    string
		secret   string
		provider func(string) Provider
	}{
		{
			name: "openai", model: "gpt-4.1-nano", secret: "OPENAI_API_KEY",
			provider: func(url string) Provider { return OpenAIProvider{BaseURL: url} },
		},
		{
			name: "openai_responses", model: "gpt-4.1-nano", secret: "OPENAI_API_KEY",
			provider: func(url string) Provider { return OpenAIResponsesProvider{BaseURL: url} },
		},
		{
			name: "anthropic", model: "claude-sonnet-4-5", secret: "ANTHROPIC_API_KEY",
			provider: func(url string) Provider { return AnthropicProvider{BaseURL: url} },
		},
	}
	statuses := []int{http.StatusBadRequest, http.StatusTooManyRequests, http.StatusInternalServerError}

	for _, provider := range providers {
		for _, status := range statuses {
			t.Run(fmt.Sprintf("%s/%d", provider.name, status), func(t *testing.T) {
				t.Parallel()

				var requests atomic.Int32
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					requests.Add(1)
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(status)
					_, _ = w.Write([]byte(`{"type":"error","error":{"type":"invalid_request_error","message":"provider diagnostic"}}`))
				}))
				defer server.Close()

				providerName := provider.name
				if providerName == "openai_responses" {
					providerName = "openai"
				}
				request := providerAuthTestRequest(providerName, provider.model, map[string]string{provider.secret: "test-key"})
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()

				response, err := provider.provider(server.URL).Stream(ctx, request, make(chan Event, 100))
				require.Error(t, err)
				assert.Nil(t, response)
				assert.Equal(t, int32(1), requests.Load())
				assert.Contains(t, err.Error(), "provider diagnostic")

				var appErr *temporal.ApplicationError
				if status == http.StatusBadRequest {
					require.ErrorAs(t, err, &appErr)
					assert.True(t, appErr.NonRetryable())
				} else if errors.As(err, &appErr) {
					assert.False(t, appErr.NonRetryable())
				}
			})
		}
	}
}

func TestGoogleProviderStream_BadRequestIsNonRetryable(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"code":400,"message":"Invalid model","status":"INVALID_ARGUMENT"}}`))
	}))
	defer server.Close()
	t.Setenv("GOOGLE_GEMINI_BASE_URL", server.URL)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	response, err := GoogleProvider{}.Stream(ctx,
		providerAuthTestRequest("google", "gemini-3.1-pro-preview", map[string]string{"GOOGLE_API_KEY": "test-key"}),
		make(chan Event, 100),
	)

	require.Error(t, err)
	assert.Nil(t, response)
	assert.Equal(t, int32(1), requests.Load())
	assert.Contains(t, err.Error(), "Invalid model")
	var appErr *temporal.ApplicationError
	require.ErrorAs(t, err, &appErr)
	assert.True(t, appErr.NonRetryable())
}

func TestBedrockProviderStream_BadRequestIsNonRetryable(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Amzn-Errortype", "ValidationException")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"message":"Invalid model"}`))
	}))
	defer server.Close()

	configPath := filepath.Join(t.TempDir(), "config")
	require.NoError(t, os.WriteFile(configPath, []byte("[profile llm2-test]\naws_access_key_id = test-key\naws_secret_access_key = test-secret\n"), 0600))
	t.Setenv("AWS_CONFIG_FILE", configPath)
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(t.TempDir(), "credentials"))
	t.Setenv("AWS_ENDPOINT_URL_BEDROCK_RUNTIME", server.URL)
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	response, err := (BedrockProvider{Profile: "llm2-test", Region: "us-east-1"}).Stream(ctx,
		providerAuthTestRequest("bedrock", "anthropic.claude-haiku-4-5-20251001-v1:0", nil),
		make(chan Event, 100),
	)

	require.Error(t, err)
	assert.Nil(t, response)
	assert.Equal(t, int32(1), requests.Load())
	assert.Contains(t, err.Error(), "Invalid model")
	var appErr *temporal.ApplicationError
	require.ErrorAs(t, err, &appErr)
	assert.True(t, appErr.NonRetryable())
}
