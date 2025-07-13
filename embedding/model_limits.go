package embedding

import (
	"fmt"
	"os"
	"strconv"

	"sidekick/common"

	"github.com/rs/zerolog/log"
)

const (
	// defaultMaxTokens is used if the SIDE_EMBEDDING_DEFAULT_MAX_TOKENS env variable is not set and the
	// model name is not known
	defaultMaxTokens = 2048

	// estimated number of characters per token.
	charsPerToken         = 4
	defaultGoodChunkChars = 3000
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
		case string(common.OpenaiChatProvider):
			modelName = OpenaiDefaultModel
		case string(common.GoogleChatProvider):
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

	if envVal := os.Getenv("SIDE_EMBEDDING_DEFAULT_MAX_TOKENS"); envVal != "" {
		limit, err := strconv.Atoi(envVal)
		if err == nil && limit > 0 {
			return limit, nil
		}
		if err == nil {
			err = fmt.Errorf("configured token limit should be above 0")
		}
		// if env var is set but invalid, fall through to default
		log.Warn().Err(err).Msg("Invalid SIDE_EMBEDDING_DEFAULT_MAX_TOKENS environment variable, ignoring and using built-in default")
	}

	return defaultMaxTokens, nil
}

func GetEmbeddingMaxChars(modelConfig common.ModelConfig) (int, error) {
	maxTokens, err := GetModelMaxTokens(modelConfig)
	if err != nil {
		return 0, fmt.Errorf("failed to get model max tokens: %w", err)
	}
	return maxTokens * charsPerToken, nil
}
