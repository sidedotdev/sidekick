package common

const (
	// DefaultContextLimitTokens is the fallback context limit when models.dev lookup fails
	DefaultContextLimitTokens = 100000
	// CharsPerToken is the conservative estimate for token-to-char conversion
	CharsPerToken = 2.5
)

// GetModelContextLimit returns the context limit in tokens for a given model.
// Falls back to DefaultContextLimitTokens if the model is not found in models.dev.
func GetModelContextLimit(provider, model string) int {
	modelInfo, _ := GetModel(provider, model)
	if modelInfo != nil && modelInfo.Limit.Context > 0 {
		return modelInfo.Limit.Context
	}
	return DefaultContextLimitTokens
}
