package common

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestModelSupportsReasoning(t *testing.T) {
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
		want     bool
	}{
		{
			name:     "known provider and model with reasoning true",
			provider: "openai",
			model:    "gpt-5",
			want:     true,
		},
		{
			name:     "known provider and model with reasoning false",
			provider: "openai",
			model:    "gpt-3.5-turbo",
			want:     false,
		},
		{
			name:     "case insensitive provider match",
			provider: "OpenAI",
			model:    "gpt-5",
			want:     true,
		},
		{
			name:     "unknown provider",
			provider: "unknown",
			model:    "some-model",
			want:     false,
		},
		{
			name:     "known provider unknown model",
			provider: "openai",
			model:    "unknown-model",
			want:     false,
		},
		{
			name:     "anthropic model without reasoning",
			provider: "anthropic",
			model:    "claude-3-opus",
			want:     false,
		},
		{
			name:     "custom provider fallback to model match with reasoning",
			provider: "custom-gateway",
			model:    "gpt-5",
			want:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ModelSupportsReasoning(tt.provider, tt.model)
			if got != tt.want {
				t.Errorf("ModelSupportsReasoning(%q, %q) = %v, want %v", tt.provider, tt.model, got, tt.want)
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
