package common

// ProvidersForProfile returns the providers associated with the given profile,
// where an empty profile id means the default profile.
func ProvidersForProfile(providers []ModelProviderPublicConfig, profileId string) []ModelProviderPublicConfig {
	if providers == nil {
		return nil
	}
	filtered := make([]ModelProviderPublicConfig, 0, len(providers))
	for _, provider := range providers {
		if provider.MatchesProfile(profileId) {
			filtered = append(filtered, provider)
		}
	}
	return filtered
}

// ModelsForProfile filters model selectors down to those whose provider is
// available within the given profile. Models referencing providers that are not
// declared are retained: built-in providers carry no profile association, and
// their credentials are instead resolved per profile.
func ModelsForProfile(models []ModelConfig, providers []ModelProviderPublicConfig, profileId string) []ModelConfig {
	if models == nil {
		return nil
	}
	filtered := make([]ModelConfig, 0, len(models))
	for _, model := range models {
		if modelProviderAvailable(model, providers, profileId) {
			filtered = append(filtered, model)
		}
	}
	return filtered
}

// LLMConfigForProfile filters the default and use case selectors of an LLM
// config to the models available within the given profile.
func LLMConfigForProfile(config LLMConfig, providers []ModelProviderPublicConfig, profileId string) LLMConfig {
	return LLMConfig{
		Defaults:       ModelsForProfile(config.Defaults, providers, profileId),
		UseCaseConfigs: useCaseConfigsForProfile(config.UseCaseConfigs, providers, profileId),
	}
}

// EmbeddingConfigForProfile filters the default and use case selectors of an
// embedding config to the models available within the given profile.
func EmbeddingConfigForProfile(config EmbeddingConfig, providers []ModelProviderPublicConfig, profileId string) EmbeddingConfig {
	return EmbeddingConfig{
		Defaults:       ModelsForProfile(config.Defaults, providers, profileId),
		UseCaseConfigs: useCaseConfigsForProfile(config.UseCaseConfigs, providers, profileId),
	}
}

// useCaseConfigsForProfile keeps every configured use case, since an explicit
// selector must not silently turn into the profile's defaults when none of its
// models are available.
func useCaseConfigsForProfile(useCaseConfigs map[string][]ModelConfig, providers []ModelProviderPublicConfig, profileId string) map[string][]ModelConfig {
	if useCaseConfigs == nil {
		return nil
	}
	filtered := make(map[string][]ModelConfig, len(useCaseConfigs))
	for useCase, models := range useCaseConfigs {
		filtered[useCase] = ModelsForProfile(models, providers, profileId)
	}
	return filtered
}

func modelProviderAvailable(model ModelConfig, providers []ModelProviderPublicConfig, profileId string) bool {
	declared := false
	for _, provider := range providers {
		if providerSelectorName(provider) != model.NormalizedProviderName() {
			continue
		}
		declared = true
		if provider.MatchesProfile(profileId) {
			return true
		}
	}
	return !declared
}

// providerSelectorName is the normalized name model selectors refer to. Built-in
// providers may be configured without a name, in which case their type is the
// name models select them by.
func providerSelectorName(provider ModelProviderPublicConfig) string {
	name := provider.Name
	if name == "" {
		name = provider.Type
	}
	return ModelConfig{Provider: name}.NormalizedProviderName()
}
