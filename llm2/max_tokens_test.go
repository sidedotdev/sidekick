package llm2

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sidekick/common"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/document"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const outputLimitFixture = `{
	"anthropic": {"models": {
		"claude-sonnet-4-5": {"limit": {"output": 64000}},
		"claude-no-output": {"limit": {"context": 200000}}
	}},
	"amazon-bedrock": {"models": {
		"us.anthropic.claude-sonnet-4-5-20250929-v1:0": {"limit": {"output": 64000}},
		"eu.anthropic.claude-opus-4-5-20251101-v1:0": {"limit": {"output": 64000}}
	}}
}`

func TestAnthropicOutputLimits(t *testing.T) {
	setupModelsDevFixture(t, outputLimitFixture)
	for _, tt := range []struct {
		name       string
		model      string
		limit      int
		effort     string
		compatible bool
		want       int
	}{
		{name: "model metadata", model: "claude-sonnet-4-5", want: 64000},
		{name: "explicit", model: "claude-sonnet-4-5", limit: 30000, want: 30000},
		{name: "capped explicit", model: "claude-sonnet-4-5", limit: 100000, want: 64000},
		{name: "unknown", model: "unknown"},
		{name: "nonpositive", model: "unknown", limit: -1},
		{name: "missing output", model: "claude-no-output"},
		{name: "compatible unknown", model: "unknown", compatible: true},
		{name: "compatible explicit", model: "unknown", compatible: true, limit: 1234, want: 1234},
		{name: "known thinking", model: "claude-sonnet-4-5", effort: "high", want: 64000},
		{name: "unknown thinking", model: "claude-unknown", effort: "high"},
		{name: "explicit thinking", model: "claude-unknown", effort: "high", limit: 8000, want: 21000},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var body map[string]any
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
				http.Error(w, `{"type":"error","error":{"type":"invalid_request_error","message":"endpoint rejection"}}`, http.StatusBadRequest)
			}))
			defer server.Close()

			request := anthropicCompatibleTestRequest(t)
			request.Options.ModelConfig = common.ModelConfig{
				Provider: "anthropic", Model: tt.model, ReasoningEffort: tt.effort,
			}
			request.Options.MaxTokens = tt.limit
			request.SecretManager = anthropicAuthTestSecretManager{
				secrets: map[string]string{"ANTHROPIC_API_KEY": "test-key"},
			}
			provider := AnthropicProvider{
				BaseURL: server.URL, AuthType: common.ProviderAuthTypeAPI,
				AnthropicCompatible: tt.compatible,
			}
			_, err := provider.Stream(context.Background(), request, make(chan Event, 1))
			require.ErrorContains(t, err, "endpoint rejection")
			require.NotNil(t, body, "request must reach the endpoint")
			if tt.want > 0 {
				assert.Equal(t, float64(tt.want), body["max_tokens"])
			} else {
				assert.NotContains(t, body, "max_tokens")
			}
			if tt.effort != "" {
				assert.Equal(t, map[string]any{
					"type": "enabled", "budget_tokens": float64(20000), "display": "summarized",
				}, body["thinking"])
			}
		})
	}
}

func TestBedrockOutputLimits(t *testing.T) {
	setupModelsDevFixture(t, outputLimitFixture)
	for _, tt := range []struct {
		name   string
		model  string
		effort string
		limit  int
		want   int
	}{
		{name: "known thinking", model: "us.anthropic.claude-sonnet-4-5-20250929-v1:0", effort: "high", want: 64000},
		{name: "unknown thinking", model: "claude-unknown", effort: "high"},
		{name: "nonpositive thinking", model: "claude-unknown", effort: "high", limit: -1},
		{name: "explicit thinking", model: "claude-unknown", effort: "high", limit: 8000, want: 21000},
		{name: "large explicit thinking", model: "claude-unknown", effort: "high", limit: 40000, want: 40000},
		{name: "ordinary unchanged", model: "us.anthropic.claude-sonnet-4-5-20250929-v1:0"},
		{name: "adaptive unchanged", model: "claude-opus-4-6"},
		{name: "non claude unchanged", model: "other"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			options := Options{
				ModelConfig: common.ModelConfig{Provider: "bedrock", Model: tt.model, ReasoningEffort: tt.effort},
				MaxTokens:   tt.limit,
			}
			fields, limit, _ := bedrockAnthropicRequestFields(options, tt.model)
			config := bedrockInferenceConfig(options, limit)
			if tt.want > 0 {
				require.NotNil(t, config)
				require.NotNil(t, config.MaxTokens)
				assert.EqualValues(t, tt.want, *config.MaxTokens)
			} else {
				assert.Nil(t, config)
			}
			if tt.effort != "" {
				assert.Equal(t, map[string]any{
					"type": "enabled", "budget_tokens": 20000,
				}, fields["thinking"])
			}

			var body map[string]any
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("X-Amzn-Errortype", "ValidationException")
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"message":"endpoint rejection"}`))
			}))
			defer server.Close()
			client := bedrockruntime.NewFromConfig(aws.Config{
				Region:      "us-east-1",
				Credentials: credentials.NewStaticCredentialsProvider("test", "test", ""),
			}, func(options *bedrockruntime.Options) {
				options.BaseEndpoint = aws.String(server.URL)
			})
			input := &bedrockruntime.ConverseStreamInput{
				ModelId: aws.String(tt.model), InferenceConfig: config,
			}
			if fields != nil {
				input.AdditionalModelRequestFields = document.NewLazyDocument(fields)
			}
			_, err := client.ConverseStream(context.Background(), input)
			require.ErrorContains(t, err, "endpoint rejection")
			require.NotNil(t, body)
			if tt.want > 0 {
				require.Contains(t, body, "inferenceConfig")
				assert.Equal(t, float64(tt.want), body["inferenceConfig"].(map[string]any)["maxTokens"])
			} else {
				assert.NotContains(t, body, "inferenceConfig")
			}
		})
	}
}
