package common

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
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
	// Option types other than effort, budget_tokens and toggle may appear and
	// are ignored by consumers.
	ReasoningOptions []ReasoningOption `json:"reasoning_options,omitempty"`
	// Passed through unparsed so the frontend can read alternative modes (e.g.
	// service tiers under experimental.modes) without a schema change there
	// ever breaking decoding of the rest of models.dev.
	Experimental json.RawMessage `json:"experimental,omitempty"`
}

const reasoningOptionTypeEffort = "effort"

// ReasoningOption describes one way a model's reasoning can be controlled, as
// published by models.dev: "effort" (discrete Values), "budget_tokens"
// (Min/Max token budget) or "toggle" (on/off).
type ReasoningOption struct {
	Type   string   `json:"type"`
	Values []string `json:"values,omitempty"`
	Min    int      `json:"min,omitempty"`
	Max    int      `json:"max,omitempty"`
}

// EffortLevels returns the model's effort ladder in models.dev's declared
// order, which is treated as ascending effort. Returns nil when the model has
// no effort-type reasoning option.
func (m *ModelInfo) EffortLevels() []string {
	if m == nil {
		return nil
	}
	for _, option := range m.ReasoningOptions {
		if option.Type != reasoningOptionTypeEffort {
			continue
		}
		if len(option.Values) == 0 {
			return nil
		}
		return option.Values
	}
	return nil
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

// modelsDevProviderAliases maps sidekick provider names to the corresponding
// provider key used in the models.dev dataset, when they differ.
var modelsDevProviderAliases = map[string]string{
	"bedrock": "amazon-bedrock",
}

// returns model info from models.dev, and whether the provider matched the
// requested provider (if not, but model info was returned, it means the model
// exists in a different provider)
func GetModel(provider string, model string) (*ModelInfo, bool) {
	data, err := LoadModelsDev()
	if err != nil {
		return nil, false
	}

	if providerData, ok := findModelsDevProvider(data, provider); ok {
		if modelData, exists := providerData.Models[model]; exists {
			return &modelData, true
		}
		return nil, false
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

func findModelsDevProvider(data modelsDevData, provider string) (ProviderInfo, bool) {
	providerLower := strings.ToLower(provider)
	if aliased, ok := modelsDevProviderAliases[providerLower]; ok {
		providerLower = strings.ToLower(aliased)
	}
	for providerKey, providerData := range data {
		if strings.ToLower(providerKey) == providerLower {
			return providerData, true
		}
	}
	return ProviderInfo{}, false
}

// DefaultEffortLadder is used when neither the model nor any model of its
// family publishes an effort ladder in models.dev.
var DefaultEffortLadder = []string{"low", "medium", "high", "xhigh", "max"}

// GetEffortLadder returns the ascending effort ladder for a model, never nil.
// Lookup order: the model's own effort levels, then those of the latest
// same-family model on the same models.dev provider, then DefaultEffortLadder.
// The returned slice is shared and must not be modified.
func GetEffortLadder(provider, model string) []string {
	if info, _ := GetModel(provider, model); info.EffortLevels() != nil {
		return info.EffortLevels()
	}
	if ladder := latestFamilyEffortLevels(provider, model); ladder != nil {
		return ladder
	}
	return DefaultEffortLadder
}

func latestFamilyEffortLevels(provider, model string) []string {
	family := modelFamily(model)
	if family == "" {
		return nil
	}
	data, err := LoadModelsDev()
	if err != nil {
		return nil
	}
	providerData, ok := findModelsDevProvider(data, provider)
	if !ok {
		return nil
	}

	var latest *ModelInfo
	var latestID string
	for id, info := range providerData.Models {
		if info.EffortLevels() == nil || modelFamily(id) != family {
			continue
		}
		if latest != nil {
			if info.ReleaseDate != latest.ReleaseDate {
				if info.ReleaseDate < latest.ReleaseDate {
					continue
				}
			} else if info.LastUpdated != latest.LastUpdated {
				if info.LastUpdated < latest.LastUpdated {
					continue
				}
			} else if id < latestID {
				continue
			}
		}
		latest, latestID = &info, id
	}
	return latest.EffortLevels()
}

// modelFamily returns the leading alphabetic token of a model id, e.g. "gpt"
// for "gpt-5.2", "o" for "o4-mini" and "claude" for
// "us.anthropic.claude-sonnet-4-5". Purely alphabetic dot-separated prefixes
// (Bedrock region/vendor) are stripped first.
func modelFamily(id string) string {
	id = strings.ToLower(id)
	isNotLetter := func(r rune) bool { return r < 'a' || r > 'z' }
	for {
		dot := strings.IndexByte(id, '.')
		if dot < 0 || strings.IndexFunc(id[:dot], isNotLetter) >= 0 {
			break
		}
		id = id[dot+1:]
	}
	if end := strings.IndexFunc(id, isNotLetter); end >= 0 {
		return id[:end]
	}
	return id
}

type ModelMetadata struct {
	Reasoning     bool
	ContextTokens int
}

func GetModelMetadata(provider string, model string) ModelMetadata {
	modelInfo, _ := GetModel(provider, model)
	if modelInfo == nil {
		return ModelMetadata{}
	}
	return ModelMetadata{
		Reasoning:     modelInfo.Reasoning,
		ContextTokens: modelInfo.Limit.Context,
	}
}

func getContextTokensFallback() int {
	if envVal := os.Getenv("SIDE_FALLBACK_MAX_TOKENS"); envVal != "" {
		if limit, err := strconv.Atoi(envVal); err == nil && limit > 0 {
			return limit
		}
	}
	return DefaultContextLimitTokens
}

func ClearModelsCache() {
	cacheLoadMutex.Lock()
	defer cacheLoadMutex.Unlock()
	cachedModelsData = nil
	cacheLoadedAt = time.Time{}
}
