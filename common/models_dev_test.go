package common

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSupportsReasoning(t *testing.T) {
	tempDir := t.TempDir()
	cachePath := filepath.Join(tempDir, modelsDevFilename)

	sampleData := modelsDevData{
		"openai": providerInfo{
			Models: map[string]modelInfo{
				"gpt-4o":        {Reasoning: true},
				"gpt-3.5-turbo": {Reasoning: false},
			},
		},
		"anthropic": providerInfo{
			Models: map[string]modelInfo{
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

	testCachePath = cachePath
	defer func() {
		testCachePath = ""
		cachedModelsData = nil
		cacheLoadedAt = time.Time{}
	}()

	tests := []struct {
		name     string
		provider string
		model    string
		want     bool
	}{
		{
			name:     "known provider and model with reasoning true",
			provider: "openai",
			model:    "gpt-4o",
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
			model:    "gpt-4o",
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SupportsReasoning(tt.provider, tt.model)
			if got != tt.want {
				t.Errorf("SupportsReasoning(%q, %q) = %v, want %v", tt.provider, tt.model, got, tt.want)
			}
		})
	}
}

func TestLoadModelsDev_CacheFreshness(t *testing.T) {
	tempDir := t.TempDir()
	cachePath := filepath.Join(tempDir, modelsDevFilename)

	sampleData := modelsDevData{
		"test": providerInfo{
			Models: map[string]modelInfo{
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

	testCachePath = cachePath
	defer func() {
		testCachePath = ""
		cachedModelsData = nil
		cacheLoadedAt = time.Time{}
	}()

	result, err := loadModelsDev()
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

	result2, err := loadModelsDev()
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
