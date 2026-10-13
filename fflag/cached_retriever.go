package fflag

import (
	"bytes"
	"context"
	_ "embed"
	"os"
	"path/filepath"

	"sidekick/common"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/thomaspoignant/go-feature-flag/retriever/httpretriever"
)

//go:embed flags.yml
var defaultFlags []byte

// CachedRetriever falls back to a persistent cache and embedded defaults when HTTP fails.
type CachedRetriever struct {
	httpretriever.Retriever
}

func (r *CachedRetriever) Retrieve(ctx context.Context) ([]byte, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	logger := log.Ctx(ctx)
	if logger.GetLevel() == zerolog.Disabled {
		logger = &log.Logger
	}

	data, httpErr := r.Retriever.Retrieve(ctx)
	if httpErr != nil {
		logger.Warn().Err(httpErr).Str("operation", "http").Msg("Unable to retrieve feature flags over HTTP")
	}

	cacheDir, err := common.GetSidekickCacheHome()
	if err != nil {
		logger.Warn().Err(err).Str("operation", "cache-directory").Msg("Unable to access feature flag cache directory")
		if httpErr == nil {
			return data, nil
		}
		return bytes.Clone(defaultFlags), nil
	}

	cachePath := filepath.Join(cacheDir, "flags.yml")
	if httpErr == nil {
		if err := os.WriteFile(cachePath, data, 0600); err != nil {
			logger.Warn().Err(err).Str("operation", "cache-write").Str("path", cachePath).Msg("Unable to write feature flag cache")
		}
		return data, nil
	}

	cached, err := os.ReadFile(cachePath)
	if err != nil {
		logger.Warn().Err(err).Str("operation", "cache-read").Str("path", cachePath).Msg("Unable to read feature flag cache")
		return bytes.Clone(defaultFlags), nil
	}
	return cached, nil
}
