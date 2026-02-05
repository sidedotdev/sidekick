package common

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetModelContextLimit(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		provider string
		model    string
		want     int
	}{
		{
			name:     "unknown model uses default",
			provider: "unknown",
			model:    "unknown-model",
			want:     DefaultContextLimitTokens,
		},
		{
			name:     "empty provider and model uses default",
			provider: "",
			model:    "",
			want:     DefaultContextLimitTokens,
		},
		{
			name:     "claude opus 4 5 from anthropic has 200k context",
			provider: "anthropic",
			model:    "claude-opus-4-5-20251101",
			want:     200000,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := GetModelContextLimit(tt.provider, tt.model)
			assert.Equal(t, tt.want, result)
		})
	}
}

func TestGetModelContextLimit_FallbackEnvVar(t *testing.T) {
	t.Setenv("SIDE_FALLBACK_MAX_TOKENS", "50000")
	result := GetModelContextLimit("unknown", "unknown-model")
	assert.Equal(t, 50000, result)
}

func TestGetModelContextLimit_InvalidEnvVar(t *testing.T) {
	t.Setenv("SIDE_FALLBACK_MAX_TOKENS", "not-a-number")
	result := GetModelContextLimit("unknown", "unknown-model")
	assert.Equal(t, DefaultContextLimitTokens, result)
}

func TestMaxCharsForModel(t *testing.T) {
	t.Parallel()

	result := MaxCharsForModel("unknown", "unknown-model", 15000)
	totalChars := int(float64(DefaultContextLimitTokens) * CharsPerToken)
	expected := totalChars - 15000
	assert.Equal(t, expected, result)
}
