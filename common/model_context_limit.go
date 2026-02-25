package common

import (
	"os"
	"strconv"

	"github.com/rs/zerolog/log"
)

const (
	// DefaultContextLimitTokens is the fallback context limit when models.dev lookup fails
	DefaultContextLimitTokens = 100000
	// CharsPerToken is a conservative estimate for token-to-char conversion
	CharsPerToken = 1.9
)

// GetModelContextLimit returns the context limit in tokens for a given model.
// Falls back to SIDE_FALLBACK_MAX_TOKENS env var if set, then DefaultContextLimitTokens.
func GetModelContextLimit(provider, model string) int {
	modelInfo, _ := GetModel(provider, model)
	if modelInfo != nil && modelInfo.Limit.Context > 0 {
		return modelInfo.Limit.Context
	}
	if envVal := os.Getenv("SIDE_FALLBACK_MAX_TOKENS"); envVal != "" {
		if limit, err := strconv.Atoi(envVal); err == nil && limit > 0 {
			return limit
		}
		log.Warn().Str("SIDE_FALLBACK_MAX_TOKENS", envVal).Msg("invalid SIDE_FALLBACK_MAX_TOKENS value, using default")
	}
	return DefaultContextLimitTokens
}

// MaxCharsForModel returns the maximum character budget for a model's context window.
func MaxCharsForModel(provider, model string) int {
	contextTokens := GetModelContextLimit(provider, model)
	return int(float64(contextTokens) * CharsPerToken)
}

// MaxChars returns the character budget derived from the model's context window.
func (m ModelMetadata) MaxChars() int {
	contextTokens := m.ContextTokens
	if contextTokens <= 0 {
		contextTokens = getContextTokensFallback()
	}
	return int(float64(contextTokens) * CharsPerToken)
}
