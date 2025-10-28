package embedding

import (
	"fmt"
	"math"
	"os"
	"strconv"

	"sidekick/common"

	"github.com/rs/zerolog/log"
)

const (
	// defaultMaxTokens is used if the SIDE_EMBEDDING_DEFAULT_MAX_TOKENS env variable is not set and the
	// model name is not known
	defaultMaxTokens = 2048

	// defaultMaxBatchTokens is used if SIDE_EMBEDDING_MAX_TOKENS_PER_REQUEST env var is not set and
	// both model and provider are unknown
	defaultMaxBatchTokens = 20000

	// defaultMaxBatchSize is used if SIDE_EMBEDDING_MAX_BATCH_SIZE env var is not set and
	// both model and provider are unknown
	defaultMaxBatchSize = 250

	// estimated number of characters per token (conservative under-estimate)
	charsPerToken         = float64(3.5)
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

// modelBatchTokenLimits maps known embedding model names to their maximum batch token limits.
var modelBatchTokenLimits = map[string]int{
	"text-embedding-3-small": 300000,
}

// modelBatchSizeLimits maps known embedding model names to their maximum batch size (number of inputs).
var modelBatchSizeLimits = map[string]int{
	"gemini-embedding-001": 1,
}

// providerBatchTokenLimits maps provider names to their default maximum batch token limits.
var providerBatchTokenLimits = map[string]int{
	string(common.OpenaiChatProvider): 300000,
	string(common.GoogleChatProvider): 20000,
}

// providerBatchSizeLimits maps provider names to their default maximum batch size (number of inputs).
var providerBatchSizeLimits = map[string]int{
	string(common.OpenaiChatProvider): 2048,
	string(common.GoogleChatProvider): 250,
}

// BatchEmbeddingRequests splits a list of inputs into batches that stay under both token and size limits.
// Each batch will not exceed the maximum tokens or maximum number of inputs allowed by the model/provider.
// The original order of inputs is preserved across batches.
func BatchEmbeddingRequests(inputs []string, modelConfig common.ModelConfig) ([][]string, error) {
	maxBatchTokens, err := GetMaxBatchTokens(modelConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to get max batch tokens: %w", err)
	}

	maxBatchSize, err := GetMaxBatchSize(modelConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to get max batch size: %w", err)
	}

	var batches [][]string
	var currentBatch []string
	currentBatchTokens := 0

	for _, input := range inputs {
		inputTokens := int(math.Ceil(float64(len(input)) / charsPerToken))

		// Return an error if a single input exceeds the maximum batch token limit
		if inputTokens > maxBatchTokens {
			return nil, fmt.Errorf("input exceeds maximum batch token limit: %d tokens (max: %d)", inputTokens, maxBatchTokens)
		}
		// Start a new batch if this input would exceed either limit
		if (currentBatchTokens > 0 && currentBatchTokens+inputTokens > maxBatchTokens) ||
			(len(currentBatch)+1 > maxBatchSize) {
			batches = append(batches, currentBatch)
			currentBatch = nil
			currentBatchTokens = 0
		}

		currentBatch = append(currentBatch, input)
		currentBatchTokens += inputTokens
	}

	// Add the final batch if not empty
	if len(currentBatch) > 0 {
		batches = append(batches, currentBatch)
	}

	return batches, nil
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
		// if env var is set but invalid, don't error but warn and fall through to default
		log.Warn().Err(err).Msg("Invalid SIDE_EMBEDDING_DEFAULT_MAX_TOKENS environment variable, ignoring and using built-in default")
	}

	return defaultMaxTokens, nil
}

func GetModelMaxChars(modelConfig common.ModelConfig) (int, error) {
	maxTokens, err := GetModelMaxTokens(modelConfig)
	if err != nil {
		return 0, fmt.Errorf("failed to get model max tokens: %w", err)
	}
	return int(math.Ceil(float64(maxTokens) * charsPerToken)), nil
}

// GetMaxBatchSize determines the maximum number of inputs allowed in a batch request.
// It first checks model-specific limits, then falls back to provider defaults,
// and finally uses a configurable default if both model and provider are unknown.
func GetMaxBatchSize(modelConfig common.ModelConfig) (int, error) {
	modelName := modelConfig.Model
	providerName := modelConfig.Provider

	if modelName == "" {
		switch providerName {
		case string(common.OpenaiChatProvider):
			modelName = OpenaiDefaultModel
		case string(common.GoogleChatProvider):
			modelName = GoogleDefaultModel
		}
	}

	if modelName != "" {
		if limit, found := modelBatchSizeLimits[modelName]; found {
			return limit, nil
		}
	}

	if providerName != "" {
		if limit, found := providerBatchSizeLimits[providerName]; found {
			return limit, nil
		}
	}

	if envVal := os.Getenv("SIDE_EMBEDDING_MAX_BATCH_SIZE"); envVal != "" {
		limit, err := strconv.Atoi(envVal)
		if err == nil && limit > 0 {
			return limit, nil
		}
		if err == nil {
			err = fmt.Errorf("configured batch size limit should be above 0")
		}
		log.Warn().Err(err).Msg("Invalid SIDE_EMBEDDING_MAX_BATCH_SIZE environment variable, ignoring and using built-in default")
	}

	return defaultMaxBatchSize, nil
}

// GetMaxBatchTokens determines the maximum total tokens allowed in a batch request.
// It first checks model-specific limits, then falls back to provider defaults,
// and finally uses a configurable default if both model and provider are unknown.
func GetMaxBatchTokens(modelConfig common.ModelConfig) (int, error) {
	modelName := modelConfig.Model
	providerName := modelConfig.Provider

	if modelName == "" {
		switch providerName {
		case string(common.OpenaiChatProvider):
			modelName = OpenaiDefaultModel
		case string(common.GoogleChatProvider):
			modelName = GoogleDefaultModel
		}
	}

	if modelName != "" {
		if limit, found := modelBatchTokenLimits[modelName]; found {
			return limit, nil
		}
	}

	if providerName != "" {
		if limit, found := providerBatchTokenLimits[providerName]; found {
			return limit, nil
		}
	}

	if envVal := os.Getenv("SIDE_EMBEDDING_MAX_TOKENS_PER_REQUEST"); envVal != "" {
		limit, err := strconv.Atoi(envVal)
		if err == nil && limit > 0 {
			return limit, nil
		}
		if err == nil {
			err = fmt.Errorf("configured batch token limit should be above 0")
		}
		log.Warn().Err(err).Msg("Invalid SIDE_EMBEDDING_MAX_TOKENS_PER_REQUEST environment variable, ignoring and using built-in default")
	}

	return defaultMaxBatchTokens, nil
}
