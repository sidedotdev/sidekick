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
		wantMin  int
	}{
		{
			name:     "unknown model uses default",
			provider: "unknown",
			model:    "unknown-model",
			wantMin:  DefaultContextLimitTokens,
		},
		{
			name:     "empty provider and model uses default",
			provider: "",
			model:    "",
			wantMin:  DefaultContextLimitTokens,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := GetModelContextLimit(tt.provider, tt.model)
			assert.GreaterOrEqual(t, result, tt.wantMin)
		})
	}
}
