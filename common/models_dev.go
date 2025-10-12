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
	modelsDevCacheTTL = 24 * time.Hour
	modelsDevFilename = "models.dev.json"
	httpTimeout       = 30 * time.Second
)

type modelInfo struct {
	Reasoning bool `json:"reasoning"`
}

type providerInfo struct {
	Models map[string]modelInfo `json:"models"`
}

type modelsDevData map[string]providerInfo

var (
	cachedModelsData modelsDevData
	cacheLoadMutex   sync.Mutex
	cacheLoadedAt    time.Time
	testCachePath    string
)

func getModelsDevCachePath() (string, error) {
	if testCachePath != "" {
		return testCachePath, nil
	}
	cacheHome, err := GetSidekickCacheHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(cacheHome, modelsDevFilename), nil
}

func loadModelsDev() (modelsDevData, error) {
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
		log.Warn().Err(err).Msg("failed to write models.dev cache")
	}

	return data, nil
}

func SupportsReasoning(provider string, model string) bool {
	data, err := loadModelsDev()
	if err != nil {
		return false
	}

	providerLower := strings.ToLower(provider)
	for providerKey, providerData := range data {
		if strings.ToLower(providerKey) == providerLower {
			if modelData, exists := providerData.Models[model]; exists {
				return modelData.Reasoning
			}
			return false
		}
	}

	return false
}

func SetTestCachePath(path string) {
	cacheLoadMutex.Lock()
	defer cacheLoadMutex.Unlock()
	testCachePath = path
}

func ClearTestCache() {
	cacheLoadMutex.Lock()
	defer cacheLoadMutex.Unlock()
	testCachePath = ""
	cachedModelsData = nil
	cacheLoadedAt = time.Time{}
}
