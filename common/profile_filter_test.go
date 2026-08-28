package common

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func profileFilterProviders() []ModelProviderPublicConfig {
	return []ModelProviderPublicConfig{
		{Name: "openai", Type: "openai"},
		{Name: "openai-work", Type: "openai", Profiles: &[]string{"Work"}},
		{Name: "unscoped", Type: "openai", Profiles: &[]string{}},
		{Name: "shared", Type: "openai", Profiles: &[]string{"default", "work"}},
	}
}

func TestProvidersForProfile(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		profileId string
		expected  []string
	}{
		{name: "unset profile id means default", profileId: "", expected: []string{"openai", "shared"}},
		{name: "default profile", profileId: DefaultProfileId, expected: []string{"openai", "shared"}},
		{name: "non-default profile matches case-insensitively", profileId: "work", expected: []string{"openai-work", "shared"}},
		{name: "undeclared profile matches nothing", profileId: "personal", expected: []string{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			names := []string{}
			for _, provider := range ProvidersForProfile(profileFilterProviders(), tt.profileId) {
				names = append(names, provider.Name)
			}
			assert.Equal(t, tt.expected, names)
		})
	}

	t.Run("nil providers stay nil", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, ProvidersForProfile(nil, "work"))
	})
}

func TestModelsForProfile(t *testing.T) {
	t.Parallel()

	models := []ModelConfig{
		{Provider: "openai", Model: "default-model"},
		{Provider: "openai-work", Model: "work-model"},
		{Provider: "unscoped", Model: "unscoped-model"},
		{Provider: "shared", Model: "shared-model"},
		{Provider: "anthropic", Model: "builtin-model"},
	}

	tests := []struct {
		name      string
		profileId string
		expected  []string
	}{
		{
			name:      "default profile keeps unassociated providers and built-ins",
			profileId: DefaultProfileId,
			expected:  []string{"default-model", "shared-model", "builtin-model"},
		},
		{
			name:      "non-default profile keeps associated providers and built-ins",
			profileId: "work",
			expected:  []string{"work-model", "shared-model", "builtin-model"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			filteredModels := []string{}
			for _, model := range ModelsForProfile(models, profileFilterProviders(), tt.profileId) {
				filteredModels = append(filteredModels, model.Model)
			}
			assert.Equal(t, tt.expected, filteredModels)
		})
	}

	t.Run("nil models stay nil", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, ModelsForProfile(nil, profileFilterProviders(), "work"))
	})
}

func TestModelsForProfileMatchesNamelessProvidersByType(t *testing.T) {
	t.Parallel()

	providers := []ModelProviderPublicConfig{{Type: "anthropic", Profiles: &[]string{"work"}}}
	models := []ModelConfig{{Provider: "anthropic", Model: "claude"}}

	assert.Empty(t, ModelsForProfile(models, providers, DefaultProfileId), "a built-in provider configured without a name is selected by its type")
	assert.Equal(t, models, ModelsForProfile(models, providers, "work"))
}

func TestLLMAndEmbeddingConfigForProfile(t *testing.T) {
	t.Parallel()

	llmConfig := LLMConfig{
		Defaults: []ModelConfig{
			{Provider: "openai", Model: "default-model"},
			{Provider: "openai-work", Model: "work-model"},
		},
		UseCaseConfigs: map[string][]ModelConfig{
			CodingKey:   {{Provider: "openai-work", Model: "work-coding"}},
			PlanningKey: {{Provider: "openai", Model: "default-planning"}},
		},
	}
	embeddingConfig := EmbeddingConfig{
		Defaults: []ModelConfig{
			{Provider: "unscoped", Model: "unscoped-embed"},
			{Provider: "openai-work", Model: "work-embed"},
		},
		UseCaseConfigs: map[string][]ModelConfig{
			"summarization": {{Provider: "unscoped", Model: "unscoped-embed"}},
		},
	}

	filteredLLM := LLMConfigForProfile(llmConfig, profileFilterProviders(), "work")
	assert.Equal(t, []ModelConfig{{Provider: "openai-work", Model: "work-model"}}, filteredLLM.Defaults)
	assert.Equal(t, map[string][]ModelConfig{
		CodingKey:   {{Provider: "openai-work", Model: "work-coding"}},
		PlanningKey: {},
	}, filteredLLM.UseCaseConfigs, "use cases without available models are kept empty rather than falling back to the defaults")

	filteredEmbedding := EmbeddingConfigForProfile(embeddingConfig, profileFilterProviders(), "work")
	assert.Equal(t, []ModelConfig{{Provider: "openai-work", Model: "work-embed"}}, filteredEmbedding.Defaults)
	assert.Equal(t, map[string][]ModelConfig{"summarization": {}}, filteredEmbedding.UseCaseConfigs, "an explicitly empty profile association is never selected")

	unfilteredForDefault := LLMConfigForProfile(llmConfig, nil, DefaultProfileId)
	assert.Equal(t, llmConfig.Defaults, unfilteredForDefault.Defaults, "models keep their provider when nothing is declared")
}
