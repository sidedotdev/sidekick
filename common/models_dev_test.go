package common

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestGetModelMetadata(t *testing.T) {
	ClearModelsCache()
	t.Cleanup(ClearModelsCache)
	tempDir := t.TempDir()
	t.Setenv("SIDE_CACHE_HOME", tempDir)

	cachePath := filepath.Join(tempDir, modelsDevFilename)

	sampleData := modelsDevData{
		"openai": ProviderInfo{
			Models: map[string]ModelInfo{
				"gpt-5":         {Reasoning: true},
				"gpt-3.5-turbo": {Reasoning: false},
			},
		},
		"anthropic": ProviderInfo{
			Models: map[string]ModelInfo{
				"claude-3-opus": {Reasoning: false},
			},
		},
	}

	data, err := json.Marshal(sampleData)
	if err != nil {
		t.Fatalf("failed to marshal sample data: %v", err)
	}

	if err := os.WriteFile(cachePath, data, 0644); err != nil {
		t.Fatalf("failed to write cache file: %v", err)
	}

	tests := []struct {
		name     string
		provider string
		model    string
		want     ModelMetadata
	}{
		{
			name:     "known provider and model with reasoning true",
			provider: "openai",
			model:    "gpt-5",
			want:     ModelMetadata{Reasoning: true},
		},
		{
			name:     "known provider and model with reasoning false",
			provider: "openai",
			model:    "gpt-3.5-turbo",
			want:     ModelMetadata{Reasoning: false},
		},
		{
			name:     "case insensitive provider match",
			provider: "OpenAI",
			model:    "gpt-5",
			want:     ModelMetadata{Reasoning: true},
		},
		{
			name:     "unknown provider",
			provider: "unknown",
			model:    "some-model",
			want:     ModelMetadata{Reasoning: false},
		},
		{
			name:     "known provider unknown model",
			provider: "openai",
			model:    "unknown-model",
			want:     ModelMetadata{Reasoning: false},
		},
		{
			name:     "anthropic model without reasoning",
			provider: "anthropic",
			model:    "claude-3-opus",
			want:     ModelMetadata{Reasoning: false},
		},
		{
			name:     "custom provider fallback to model match with reasoning",
			provider: "custom-gateway",
			model:    "gpt-5",
			want:     ModelMetadata{Reasoning: true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetModelMetadata(tt.provider, tt.model)
			if got != tt.want {
				t.Errorf("GetModelMetadata(%q, %q) = %v, want %v", tt.provider, tt.model, got, tt.want)
			}
		})
	}
}

func TestGetModel(t *testing.T) {
	ClearModelsCache()
	t.Cleanup(ClearModelsCache)
	tempDir := t.TempDir()
	t.Setenv("SIDE_CACHE_HOME", tempDir)

	cachePath := filepath.Join(tempDir, modelsDevFilename)

	sampleData := modelsDevData{
		"openai": ProviderInfo{
			Models: map[string]ModelInfo{
				"gpt-5": {
					ID:        "gpt-5",
					Name:      "GPT-5",
					Reasoning: true,
				},
				"gpt-3.5-turbo": {
					ID:        "gpt-3.5-turbo",
					Name:      "GPT-3.5 Turbo",
					Reasoning: false,
				},
			},
		},
		"anthropic": ProviderInfo{
			Models: map[string]ModelInfo{
				"claude-3-opus": {
					ID:        "claude-3-opus",
					Name:      "Claude 3 Opus",
					Reasoning: false,
				},
			},
		},
	}

	data, err := json.Marshal(sampleData)
	if err != nil {
		t.Fatalf("failed to marshal sample data: %v", err)
	}

	if err := os.WriteFile(cachePath, data, 0644); err != nil {
		t.Fatalf("failed to write cache file: %v", err)
	}

	tests := []struct {
		name                string
		provider            string
		model               string
		wantProviderMatched bool
		wantFound           bool
	}{
		{
			name:                "known provider and model with reasoning",
			provider:            "openai",
			model:               "gpt-5",
			wantFound:           true,
			wantProviderMatched: true,
		},
		{
			name:                "case insensitive provider match",
			provider:            "OpenAI",
			model:               "gpt-5",
			wantFound:           true,
			wantProviderMatched: true,
		},
		{
			name:                "unknown provider",
			provider:            "unknown",
			model:               "some-model",
			wantFound:           false,
			wantProviderMatched: false,
		},
		{
			name:                "known provider unknown model",
			provider:            "openai",
			model:               "unknown-model",
			wantFound:           false,
			wantProviderMatched: false,
		},
		{
			name:                "model not in list",
			provider:            "anthropic",
			model:               "nonexistent-model",
			wantFound:           false,
			wantProviderMatched: false,
		},
		{
			name:                "custom provider fallback to model match",
			provider:            "custom-gateway",
			model:               "gpt-5",
			wantFound:           true,
			wantProviderMatched: false,
		},
		{
			name:                "builtin provider fallback",
			provider:            "google",
			model:               "gpt-5",
			wantFound:           true,
			wantProviderMatched: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			modelInfo, providerMatched := GetModel(tt.provider, tt.model)
			if providerMatched != tt.wantProviderMatched {
				t.Errorf("GetModel(%q, %q) provider matched = %v, want %v", tt.provider, tt.model, providerMatched, tt.wantProviderMatched)
			}
			found := modelInfo != nil
			if found != tt.wantFound {
				t.Errorf("GetModel(%q, %q) found = %v, want %v", tt.provider, tt.model, found, tt.wantFound)
			}
		})
	}
}

func TestGetModel_BedrockAlias(t *testing.T) {
	ClearModelsCache()
	t.Cleanup(ClearModelsCache)
	tempDir := t.TempDir()
	t.Setenv("SIDE_CACHE_HOME", tempDir)

	cachePath := filepath.Join(tempDir, modelsDevFilename)

	sampleData := modelsDevData{
		"amazon-bedrock": ProviderInfo{
			Models: map[string]ModelInfo{
				"google.gemma-3-27b-it": {
					ID:   "google.gemma-3-27b-it",
					Name: "Gemma 3 27B",
				},
				"global.anthropic.claude-haiku-4-5-20251001-v1:0": {
					ID:        "global.anthropic.claude-haiku-4-5-20251001-v1:0",
					Name:      "Claude Haiku 4.5",
					Reasoning: true,
				},
			},
		},
	}

	data, err := json.Marshal(sampleData)
	if err != nil {
		t.Fatalf("failed to marshal sample data: %v", err)
	}

	if err := os.WriteFile(cachePath, data, 0644); err != nil {
		t.Fatalf("failed to write cache file: %v", err)
	}

	tests := []struct {
		name     string
		provider string
		model    string
	}{
		{
			name:     "bedrock alias resolves gemma",
			provider: "bedrock",
			model:    "google.gemma-3-27b-it",
		},
		{
			name:     "bedrock alias resolves claude haiku",
			provider: "bedrock",
			model:    "global.anthropic.claude-haiku-4-5-20251001-v1:0",
		},
		{
			name:     "bedrock alias is case insensitive",
			provider: "Bedrock",
			model:    "google.gemma-3-27b-it",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			modelInfo, providerMatched := GetModel(tt.provider, tt.model)
			if !providerMatched {
				t.Errorf("GetModel(%q, %q) provider matched = false, want true", tt.provider, tt.model)
			}
			if modelInfo == nil {
				t.Fatalf("GetModel(%q, %q) returned nil model info, want non-nil", tt.provider, tt.model)
			}
			if modelInfo.ID != tt.model {
				t.Errorf("GetModel(%q, %q) model ID = %q, want %q", tt.provider, tt.model, modelInfo.ID, tt.model)
			}
		})
	}
}

func TestLoadModelsDev_CacheFreshness(t *testing.T) {
	ClearModelsCache()
	t.Cleanup(ClearModelsCache)
	tempDir := t.TempDir()
	t.Setenv("SIDE_CACHE_HOME", tempDir)

	cachePath := filepath.Join(tempDir, modelsDevFilename)

	sampleData := modelsDevData{
		"test": ProviderInfo{
			Models: map[string]ModelInfo{
				"test-model": {Reasoning: true},
			},
		},
	}

	data, err := json.Marshal(sampleData)
	if err != nil {
		t.Fatalf("failed to marshal sample data: %v", err)
	}

	if err := os.WriteFile(cachePath, data, 0644); err != nil {
		t.Fatalf("failed to write cache file: %v", err)
	}

	result, err := LoadModelsDev()
	if err != nil {
		t.Fatalf("loadModelsDev() failed: %v", err)
	}

	if result == nil {
		t.Fatal("loadModelsDev() returned nil data")
	}

	if _, exists := result["test"]; !exists {
		t.Error("expected 'test' provider in loaded data")
	}

	firstLoadTime := cacheLoadedAt

	result2, err := LoadModelsDev()
	if err != nil {
		t.Fatalf("second loadModelsDev() failed: %v", err)
	}

	if result2 == nil {
		t.Fatal("second loadModelsDev() returned nil")
	}

	if cacheLoadedAt != firstLoadTime {
		t.Error("expected cache to be reused without reloading")
	}
}

func writeModelsDevFixture(t *testing.T, rawJSON string) {
	t.Helper()
	ClearModelsCache()
	t.Cleanup(ClearModelsCache)
	tempDir := t.TempDir()
	t.Setenv("SIDE_CACHE_HOME", tempDir)
	if err := os.WriteFile(filepath.Join(tempDir, modelsDevFilename), []byte(rawJSON), 0644); err != nil {
		t.Fatalf("failed to write cache file: %v", err)
	}
}

func TestReasoningOptionsParsing(t *testing.T) {
	writeModelsDevFixture(t, `{
		"openai": {"models": {
			"gpt-5.2": {"id": "gpt-5.2", "reasoning": true, "reasoning_options": [{"type": "effort", "values": ["none", "low", "medium", "high", "xhigh"]}]},
			"gpt-6-astra": {"id": "gpt-6-astra", "reasoning": true, "reasoning_options": [{"type": "toggle"}, {"type": "effort", "values": ["low", "high", "ultra", "medium"]}]},
			"o1": {"id": "o1", "reasoning": true, "reasoning_options": [{"type": "toggle"}]},
			"gpt-4o": {"id": "gpt-4o", "reasoning": false, "reasoning_options": []},
			"gpt-weird": {"id": "gpt-weird", "reasoning": true, "reasoning_options": [{"type": "future_kind", "values": ["a"]}]},
			"gpt-empty-effort": {"id": "gpt-empty-effort", "reasoning": true, "reasoning_options": [{"type": "effort", "values": []}]}
		}},
		"google": {"models": {
			"gemini-2.5-pro": {"id": "gemini-2.5-pro", "reasoning": true, "reasoning_options": [{"type": "budget_tokens", "min": 128, "max": 32768}]}
		}},
		"anthropic": {"models": {
			"claude-sonnet-4-5": {"id": "claude-sonnet-4-5", "reasoning": true, "reasoning_options": [{"type": "budget_tokens", "min": 1024}]},
			"claude-3-haiku": {"id": "claude-3-haiku", "reasoning": false}
		}}
	}`)

	tests := []struct {
		name            string
		provider        string
		model           string
		wantOptions     []ReasoningOption
		wantEffortLevel []string
	}{
		{
			name:            "effort",
			provider:        "openai",
			model:           "gpt-5.2",
			wantOptions:     []ReasoningOption{{Type: "effort", Values: []string{"none", "low", "medium", "high", "xhigh"}}},
			wantEffortLevel: []string{"none", "low", "medium", "high", "xhigh"},
		},
		{
			name:     "first effort option preserved verbatim including ultra",
			provider: "openai",
			model:    "gpt-6-astra",
			wantOptions: []ReasoningOption{
				{Type: "toggle"},
				{Type: "effort", Values: []string{"low", "high", "ultra", "medium"}},
			},
			wantEffortLevel: []string{"low", "high", "ultra", "medium"},
		},
		{
			name:        "toggle",
			provider:    "openai",
			model:       "o1",
			wantOptions: []ReasoningOption{{Type: "toggle"}},
		},
		{
			name:        "empty",
			provider:    "openai",
			model:       "gpt-4o",
			wantOptions: nil,
		},
		{
			name:        "unknown type",
			provider:    "openai",
			model:       "gpt-weird",
			wantOptions: []ReasoningOption{{Type: "future_kind", Values: []string{"a"}}},
		},
		{
			name:        "effort with no values",
			provider:    "openai",
			model:       "gpt-empty-effort",
			wantOptions: []ReasoningOption{{Type: "effort"}},
		},
		{
			name:        "budget tokens with min and max",
			provider:    "google",
			model:       "gemini-2.5-pro",
			wantOptions: []ReasoningOption{{Type: "budget_tokens", Min: 128, Max: 32768}},
		},
		{
			name:        "budget tokens with min only",
			provider:    "anthropic",
			model:       "claude-sonnet-4-5",
			wantOptions: []ReasoningOption{{Type: "budget_tokens", Min: 1024}},
		},
		{
			name:        "missing field",
			provider:    "anthropic",
			model:       "claude-3-haiku",
			wantOptions: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info, _ := GetModel(tt.provider, tt.model)
			if info == nil {
				t.Fatalf("GetModel(%q, %q) returned nil", tt.provider, tt.model)
			}
			if len(info.ReasoningOptions) != len(tt.wantOptions) {
				t.Fatalf("ReasoningOptions = %+v, want %+v", info.ReasoningOptions, tt.wantOptions)
			}
			for i, want := range tt.wantOptions {
				got := info.ReasoningOptions[i]
				if got.Type != want.Type || got.Min != want.Min || got.Max != want.Max || !equalStrings(got.Values, want.Values) {
					t.Errorf("ReasoningOptions[%d] = %+v, want %+v", i, got, want)
				}
			}
			levels := info.EffortLevels()
			if !equalStrings(levels, tt.wantEffortLevel) {
				t.Errorf("EffortLevels() = %v, want %v", levels, tt.wantEffortLevel)
			}
			if tt.wantEffortLevel == nil && levels != nil {
				t.Errorf("EffortLevels() = %#v, want nil", levels)
			}
		})
	}

	var missing *ModelInfo
	if levels := missing.EffortLevels(); levels != nil {
		t.Errorf("nil ModelInfo EffortLevels() = %v, want nil", levels)
	}
}

func TestGetEffortLadder(t *testing.T) {
	writeModelsDevFixture(t, `{
		"openai": {"models": {
			"gpt-5.2": {"id": "gpt-5.2", "release_date": "2025-12-11", "reasoning_options": [{"type": "effort", "values": ["none", "low", "medium", "high", "xhigh"]}]},
			"gpt-5.5": {"id": "gpt-5.5", "release_date": "2026-04-01", "last_updated": "2026-04-10", "reasoning_options": [{"type": "effort", "values": ["none", "low", "medium", "high", "xhigh", "max"]}]},
			"gpt-5.5-pro": {"id": "gpt-5.5-pro", "release_date": "2026-04-01", "last_updated": "2026-04-02", "reasoning_options": [{"type": "effort", "values": ["medium", "high", "xhigh"]}]},
			"gpt-image-2": {"id": "gpt-image-2", "release_date": "2026-09-01"},
			"o1": {"id": "o1", "release_date": "2024-12-05", "reasoning_options": [{"type": "effort", "values": ["low", "high"]}]},
			"o3": {"id": "o3", "release_date": "2025-04-16", "reasoning_options": [{"type": "effort", "values": ["low", "medium", "high"]}]}
		}},
		"anthropic": {"models": {
			"claude-sonnet-4-5": {"id": "claude-sonnet-4-5", "release_date": "2025-09-29", "reasoning_options": [{"type": "budget_tokens", "min": 1024}]},
			"claude-opus-4-7": {"id": "claude-opus-4-7", "release_date": "2026-03-01", "reasoning_options": [{"type": "effort", "values": ["low", "medium", "high", "xhigh", "max"]}]},
			"claude-haiku-5": {"id": "claude-haiku-5", "release_date": "2026-08-01", "reasoning_options": [{"type": "budget_tokens", "min": 1024}]}
		}},
		"amazon-bedrock": {"models": {
			"us.anthropic.claude-opus-4-7-v1:0": {"id": "us.anthropic.claude-opus-4-7-v1:0", "release_date": "2026-03-01", "reasoning_options": [{"type": "effort", "values": ["low", "medium", "high", "max"]}]},
			"global.anthropic.claude-haiku-4-5-20251001-v1:0": {"id": "global.anthropic.claude-haiku-4-5-20251001-v1:0", "release_date": "2025-10-01", "reasoning_options": [{"type": "budget_tokens", "min": 1024}]},
			"meta.llama4-maverick-17b-instruct-v1:0": {"id": "meta.llama4-maverick-17b-instruct-v1:0", "release_date": "2025-04-05"}
		}},
		"google": {"models": {
			"gemini-3.1-pro-preview": {"id": "gemini-3.1-pro-preview", "release_date": "2026-02-19", "reasoning_options": [{"type": "effort", "values": ["minimal", "low", "medium", "high"]}]},
			"gemini-2.5-pro": {"id": "gemini-2.5-pro", "release_date": "2025-03-20", "reasoning_options": [{"type": "budget_tokens", "min": 128, "max": 32768}]}
		}}
	}`)

	gpt52 := []string{"none", "low", "medium", "high", "xhigh"}
	gpt55 := []string{"none", "low", "medium", "high", "xhigh", "max"}
	opus47 := []string{"low", "medium", "high", "xhigh", "max"}
	bedrockOpus47 := []string{"low", "medium", "high", "max"}
	gemini31 := []string{"minimal", "low", "medium", "high"}

	tests := []struct {
		name     string
		provider string
		model    string
		want     []string
	}{
		{name: "exact model", provider: "openai", model: "gpt-5.2", want: gpt52},
		{name: "exact model older than family latest", provider: "openai", model: "o1", want: []string{"low", "high"}},
		{name: "unknown future gpt uses latest gpt", provider: "openai", model: "gpt-7", want: gpt55},
		{name: "release date tie broken by last_updated", provider: "openai", model: "gpt-6-astra", want: gpt55},
		{name: "case insensitive", provider: "OpenAI", model: "GPT-7", want: gpt55},
		{name: "o family", provider: "openai", model: "o4-mini", want: []string{"low", "medium", "high"}},
		{name: "existing model without ladder uses family", provider: "openai", model: "gpt-image-2", want: gpt55},
		{name: "unknown future claude", provider: "anthropic", model: "claude-opus-5", want: opus47},
		{name: "budget-only claude uses family", provider: "anthropic", model: "claude-sonnet-4-5", want: opus47},
		{name: "bedrock prefixed id via alias", provider: "bedrock", model: "us.anthropic.claude-sonnet-5-v1:0", want: bedrockOpus47},
		{name: "bedrock family without ladder", provider: "bedrock", model: "meta.llama5-v1:0", want: DefaultEffortLadder},
		{name: "unknown future gemini", provider: "google", model: "gemini-4-pro", want: gemini31},
		{name: "unknown family", provider: "openai", model: "some-new-model", want: DefaultEffortLadder},
		{name: "unknown provider", provider: "custom-gateway", model: "gpt-7", want: DefaultEffortLadder},
		{name: "id without alphabetic family", provider: "openai", model: "42-model", want: DefaultEffortLadder},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetEffortLadder(tt.provider, tt.model)
			if !equalStrings(got, tt.want) {
				t.Errorf("GetEffortLadder(%q, %q) = %v, want %v", tt.provider, tt.model, got, tt.want)
			}
		})
	}
}

func TestModelFamily(t *testing.T) {
	t.Parallel()
	tests := []struct {
		id   string
		want string
	}{
		{id: "gpt-6-astra", want: "gpt"},
		{id: "gpt-5.2", want: "gpt"},
		{id: "claude-sonnet-5", want: "claude"},
		{id: "gemini-3.1-pro-preview", want: "gemini"},
		{id: "o1", want: "o"},
		{id: "o4-mini", want: "o"},
		{id: "GPT-7", want: "gpt"},
		{id: "us.anthropic.claude-sonnet-4-5-20250929-v1:0", want: "claude"},
		{id: "global.anthropic.claude-haiku-4-5-20251001-v1:0", want: "claude"},
		{id: "42-model", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.id, func(t *testing.T) {
			t.Parallel()
			if got := modelFamily(tt.id); got != tt.want {
				t.Errorf("modelFamily(%q) = %q, want %q", tt.id, got, tt.want)
			}
		})
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestLoadModelsDev_PreservesExperimentalModes(t *testing.T) {
	writeModelsDevFixture(t, `{
		"openai": {"models": {
			"gpt-5.2": {"id": "gpt-5.2", "experimental": {"modes": {
				"flex": {"provider": {"body": {"service_tier": "flex"}}},
				"fast": {"cost": {"input": 12.5, "output": 75}, "provider": {"body": {"service_tier": "priority"}}}
			}}},
			"gpt-4": {"id": "gpt-4"}
		}}
	}`)

	data, err := LoadModelsDev()
	if err != nil {
		t.Fatalf("LoadModelsDev() error = %v", err)
	}

	type served struct {
		Experimental *struct {
			Modes map[string]struct {
				Provider struct {
					Body map[string]any `json:"body"`
				} `json:"provider"`
			} `json:"modes"`
		} `json:"experimental"`
	}
	decode := func(model string) served {
		t.Helper()
		encoded, err := json.Marshal(data["openai"].Models[model])
		if err != nil {
			t.Fatalf("marshal %s: %v", model, err)
		}
		var out served
		if err := json.Unmarshal(encoded, &out); err != nil {
			t.Fatalf("unmarshal %s: %v", model, err)
		}
		return out
	}

	withModes := decode("gpt-5.2")
	if withModes.Experimental == nil {
		t.Fatalf("experimental modes dropped for gpt-5.2")
	}
	for mode, want := range map[string]string{"flex": "flex", "fast": "priority"} {
		if got := withModes.Experimental.Modes[mode].Provider.Body["service_tier"]; got != want {
			t.Errorf("mode %q service_tier = %v, want %q", mode, got, want)
		}
	}

	if withoutModes := decode("gpt-4"); withoutModes.Experimental != nil {
		t.Errorf("expected experimental to be omitted for gpt-4, got %+v", withoutModes.Experimental)
	}
}
