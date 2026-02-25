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

	result := MaxCharsForModel("unknown", "unknown-model")
	expected := int(float64(DefaultContextLimitTokens) * CharsPerToken)
	assert.Equal(t, expected, result)
}

func TestModelMetadata_MaxChars(t *testing.T) {
	t.Parallel()

	t.Run("uses context tokens", func(t *testing.T) {
		t.Parallel()
		m := ModelMetadata{ContextTokens: 50000}
		assert.Equal(t, int(float64(50000)*CharsPerToken), m.MaxChars())
	})

	t.Run("falls back when context tokens is zero", func(t *testing.T) {
		t.Parallel()
		m := ModelMetadata{}
		assert.Equal(t, int(float64(DefaultContextLimitTokens)*CharsPerToken), m.MaxChars())
	})
}
