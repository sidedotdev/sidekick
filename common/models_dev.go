package common

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
)

const (
	modelsDevURL      = "https://models.dev/api.json"
	modelsDevCacheTTL = 2 * time.Hour
	modelsDevFilename = "models.dev.json"
	httpTimeout       = 30 * time.Second
)

type Modalities struct {
	Input  []string `json:"input"`
	Output []string `json:"output"`
}

type Cost struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cache_read,omitempty"`
	CacheWrite float64 `json:"cache_write,omitempty"`
}

type Limit struct {
	Context int `json:"context"`
	Output  int `json:"output"`
}

type ModelInfo struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Attachment  bool       `json:"attachment,omitempty"`
	Reasoning   bool       `json:"reasoning"`
	Temperature bool       `json:"temperature,omitempty"`
	ToolCall    bool       `json:"tool_call,omitempty"`
	Knowledge   string     `json:"knowledge,omitempty"`
	ReleaseDate string     `json:"release_date,omitempty"`
	LastUpdated string     `json:"last_updated,omitempty"`
	Modalities  Modalities `json:"modalities,omitempty"`
	OpenWeights bool       `json:"open_weights,omitempty"`
	Cost        Cost       `json:"cost,omitempty"`
	Limit       Limit      `json:"limit,omitempty"`
}

type ProviderInfo struct {
	Models map[string]ModelInfo `json:"models"`
}

type modelsDevData map[string]ProviderInfo

var (
	cachedModelsData modelsDevData
	cacheLoadMutex   sync.Mutex
	cacheLoadedAt    time.Time
)

func getModelsDevCachePath() (string, error) {
	cacheHome, err := GetSidekickCacheHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(cacheHome, modelsDevFilename), nil
}

func LoadModelsDev() (modelsDevData, error) {
	cacheLoadMutex.Lock()
	defer cacheLoadMutex.Unlock()

	if cachedModelsData != nil && time.Since(cacheLoadedAt) < modelsDevCacheTTL {
		return cachedModelsData, nil
	}

	cachePath, err := getModelsDevCachePath()
	if err != nil {
		log.Error().Err(err).Msg("failed to get models.dev cache path")
		return nil, err
	}

	info, err := os.Stat(cachePath)
	cacheExists := err == nil
	cacheIsFresh := cacheExists && time.Since(info.ModTime()) < modelsDevCacheTTL

	if cacheIsFresh {
		data, err := readCacheFile(cachePath)
		if err == nil {
			cachedModelsData = data
			cacheLoadedAt = time.Now()
			return data, nil
		}
		log.Warn().Err(err).Msg("failed to read fresh cache, will try to download")
	}

	data, err := downloadModelsDev(cachePath)
	if err != nil {
		if cacheExists {
			log.Error().Err(err).Msg("failed to download models.dev, using stale cache")
			staleData, readErr := readCacheFile(cachePath)
			if readErr != nil {
				log.Error().Err(readErr).Msg("failed to read stale cache")
				return nil, readErr
			}
			cachedModelsData = staleData
			cacheLoadedAt = time.Now()
			return staleData, nil
		}
		log.Error().Err(err).Msg("failed to download models.dev and no cache exists")
		return nil, err
	}

	cachedModelsData = data
	cacheLoadedAt = time.Now()
	return data, nil
}

func readCacheFile(path string) (modelsDevData, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open cache file: %w", err)
	}
	defer file.Close()

	var data modelsDevData
	decoder := json.NewDecoder(file)
	if err := decoder.Decode(&data); err != nil {
		return nil, fmt.Errorf("failed to decode cache file: %w", err)
	}

	return data, nil
}

func downloadModelsDev(cachePath string) (modelsDevData, error) {
	client := &http.Client{Timeout: httpTimeout}
	resp, err := client.Get(modelsDevURL)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch models.dev: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("models.dev returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read models.dev response: %w", err)
	}

	var data modelsDevData
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, fmt.Errorf("failed to parse models.dev JSON: %w", err)
	}

	if err := os.WriteFile(cachePath, body, 0644); err != nil {
		log.Error().Err(err).Msg("failed to write models.dev cache")
	}

	return data, nil
}

// returns model info from models.dev, and whether the provider matched the
// requested provider (if not, but model info was returned, it means the model
// exists in a different provider)
func GetModel(provider string, model string) (*ModelInfo, bool) {
	data, err := LoadModelsDev()
	if err != nil {
		return nil, false
	}

	providerLower := strings.ToLower(provider)
	for providerKey, providerData := range data {
		if strings.ToLower(providerKey) == providerLower {
			if modelData, exists := providerData.Models[model]; exists {
				return &modelData, true
			}
			return nil, false
		}
	}

	for providerKey, providerData := range data {
		if modelData, exists := providerData.Models[model]; exists {
			log.Debug().
				Str("requestedProvider", provider).
				Str("matchedProvider", providerKey).
				Str("model", model).
				Msg("provider not found, matched model in different provider")
			return &modelData, false
		}
	}

	return nil, false
}

func ModelSupportsReasoning(provider string, model string) bool {
	modelInfo, _ := GetModel(provider, model)
	if modelInfo == nil {
		return false
	}
	return modelInfo.Reasoning
}

func ClearModelsCache() {
	cacheLoadMutex.Lock()
	defer cacheLoadMutex.Unlock()
	cachedModelsData = nil
	cacheLoadedAt = time.Time{}
}
