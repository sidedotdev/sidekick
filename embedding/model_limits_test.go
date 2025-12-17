package embedding

import (
	"math"
	"testing"

	"sidekick/common"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBatchEmbeddingRequests(t *testing.T) {
	tests := []struct {
		name        string
		inputs      []string
		modelConfig common.ModelConfig
		wantBatches int
		wantErr     bool
	}{
		{
			name:        "empty input list",
			inputs:      []string{},
			modelConfig: common.ModelConfig{},
			wantBatches: 0,
			wantErr:     false,
		},
		{
			name:        "single small input",
			inputs:      []string{"small text"},
			modelConfig: common.ModelConfig{},
			wantBatches: 1,
			wantErr:     false,
		},
		{
			name: "known model with high limit",
			inputs: []string{
				"a" + string(make([]byte, int(8000*charsPerToken))),
				"b" + string(make([]byte, int(8000*charsPerToken))),
			},
			modelConfig: common.ModelConfig{
				Model: "text-embedding-3-small",
			},
			wantBatches: 1,
			wantErr:     false,
		},
		{
			name: "known provider default limit",
			inputs: []string{
				"a" + string(make([]byte, int(15000*charsPerToken))),
				"b" + string(make([]byte, int(15000*charsPerToken))),
			},
			modelConfig: common.ModelConfig{
				Provider: string(common.GoogleChatProvider),
			},
			wantBatches: 2,
			wantErr:     false,
		},
		{
			name: "fallback default limit",
			inputs: []string{
				"a" + string(make([]byte, int(15000*charsPerToken))),
				"b" + string(make([]byte, int(15000*charsPerToken))),
			},
			modelConfig: common.ModelConfig{},
			wantBatches: 2,
			wantErr:     false,
		},
		{
			name:   "gemini model batch size limit of 100",
			inputs: []string{"small1", "small2", "small3"},
			modelConfig: common.ModelConfig{
				Model: "gemini-embedding-001",
			},
			wantBatches: 1,
			wantErr:     false,
		},
		{
			name: "gemini model batch size limit exceeded",
			inputs: func() []string {
				inputs := make([]string, 150)
				for i := range inputs {
					inputs[i] = "small"
				}
				return inputs
			}(),
			modelConfig: common.ModelConfig{
				Model: "gemini-embedding-001",
			},
			wantBatches: 2,
			wantErr:     false,
		},
		{
			name: "google provider batch size limit",
			inputs: func() []string {
				inputs := make([]string, 300)
				for i := range inputs {
					inputs[i] = "small"
				}
				return inputs
			}(),
			modelConfig: common.ModelConfig{
				Provider: string(common.GoogleChatProvider),
			},
			wantBatches: 3,
			wantErr:     false,
		},
		{
			name: "openai provider batch size limit",
			inputs: func() []string {
				inputs := make([]string, 3000)
				for i := range inputs {
					inputs[i] = "small"
				}
				return inputs
			}(),
			modelConfig: common.ModelConfig{
				Provider: string(common.OpenaiChatProvider),
			},
			wantBatches: 2,
			wantErr:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			batches, err := BatchEmbeddingRequests(tt.inputs, tt.modelConfig)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantBatches, len(batches))

			// Verify all inputs are present and in order
			if len(tt.inputs) > 0 {
				var allOutputs []string
				for _, batch := range batches {
					allOutputs = append(allOutputs, batch...)
				}
				assert.Equal(t, tt.inputs, allOutputs)
			}

			// Verify each batch is under the token limit
			maxTokens, err := GetMaxBatchTokens(tt.modelConfig)
			require.NoError(t, err)

			for _, batch := range batches {
				batchTokens := 0
				for _, input := range batch {
					batchTokens += int(math.Ceil(float64(len(input)) / charsPerToken))
				}
				assert.LessOrEqual(t, batchTokens, maxTokens)
			}
		})
	}
}
