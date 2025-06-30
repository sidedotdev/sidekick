package embedding

import (
	"fmt"
	"os"
	"strconv"

	"sidekick/common"
)

const (
	// defaultMaxTokensEnvVarName is the environment variable for overriding the default max tokens for unlisted models.
	defaultMaxTokensEnvVarName = "SIDE_EMBEDDING_DEFAULT_MAX_TOKENS"
	// fallbackDefaultMaxTokens is used if the environment variable is not set or invalid for unlisted models.
	fallbackDefaultMaxTokens = 4096

	// providerOpenAI is the normalized string for the OpenAI provider.
	providerOpenAI = "openai"
	// providerGoogle is the normalized string for the Google provider.
	providerGoogle = "google"

	tokenBufferConstant = 500
	// minModelCapacityTokensConstant is the minimum total token capacity a model must have to be considered usable.
	minModelCapacityTokensConstant = 100
	// charsPerTokenConstant is the estimated number of characters per token.
	charsPerTokenConstant = 4
	// defaultGoodChunkCharsConstant is the default preferred character size for a chunk.
	defaultGoodChunkCharsConstant = 3000
)

// modelTokenLimits maps known embedding model names to their maximum input token limits.
var modelTokenLimits = map[string]int{
	"text-embedding-3-small":     8191,
	"text-embedding-004":         2048,
	"gemini-embedding-001":       2048,
	"text-embedding-005":         2048,
	"gemini-embedding-exp-03-07": 8192,
}

// GetModelMaxTokens determines the maximum input tokens for an embedding model.
// It uses the model specified in common.ModelConfig. If the model name is empty,
// it falls back to provider-specific defaults. For models not in the predefined
// map, it uses a default value configurable via an environment variable or a
// hardcoded fallback.
func GetModelMaxTokens(modelConfig common.ModelConfig) (int, error) {
	modelName := modelConfig.Model
	providerName := modelConfig.Provider

	if modelName == "" {
		switch providerName {
		case providerOpenAI:
			modelName = OpenaiDefaultModel
		case providerGoogle:
			modelName = GoogleDefaultModel
		default:
			if providerName == "" {
				return 0, fmt.Errorf("provider name is empty and model name is empty, cannot determine default model")
			}
			return 0, fmt.Errorf("cannot determine default model for provider '%s' when model name is empty", providerName)
		}
	}

	if limit, found := modelTokenLimits[modelName]; found {
		return limit, nil
	}

	if envVal := os.Getenv(defaultMaxTokensEnvVarName); envVal != "" {
		if limit, err := strconv.Atoi(envVal); err == nil && limit > 0 {
			return limit, nil
		}
		// If env var is set but invalid, fall through to default (as per plan: "silently ignore and use fallback")
	}

	return fallbackDefaultMaxTokens, nil
}

// CalculateEmbeddingCharLimits calculates the recommended good and maximum character limits for embedding chunks.
// It derives these limits based on the model's maximum token capacity, a safety token buffer,
// a minimum required model token capacity, and an estimated characters-per-token ratio.
func CalculateEmbeddingCharLimits(modelConfig common.ModelConfig) (goodChunkChars int, maxChunkChars int, err error) {

	maxTokens, err := GetModelMaxTokens(modelConfig)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to get model max tokens: %w", err)
	}

	if maxTokens < minModelCapacityTokensConstant {
		return 0, 0, fmt.Errorf("model's max token capacity %d is less than the minimum required %d", maxTokens, minModelCapacityTokensConstant)
	}

	payloadTokens := maxTokens - tokenBufferConstant
	if payloadTokens < 1 {
		return 0, 0, fmt.Errorf("model's token capacity %d after subtracting buffer %d results in %d tokens, which is less than 1", maxTokens, tokenBufferConstant, payloadTokens)
	}

	maxChunkChars = payloadTokens * charsPerTokenConstant

	goodChunkChars = defaultGoodChunkCharsConstant
	if maxChunkChars < goodChunkChars {
		goodChunkChars = maxChunkChars
	}

	return goodChunkChars, maxChunkChars, nil
}
