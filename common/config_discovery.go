package common

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/knadh/koanf/parsers/json"
	"github.com/knadh/koanf/parsers/toml"
	"github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/v2"
)

// ConfigDiscoveryResult holds the result of discovering config files
type ConfigDiscoveryResult struct {
	// ChosenPath is the path that should be used (highest precedence among existing files)
	ChosenPath string
	// AllFound contains all config files that were found (for warning purposes)
	AllFound []string
}

// DiscoverConfigFile searches for config files from the candidate list in order of precedence.
// It returns the first existing file as the chosen path, along with all found files.
// If no files exist, ChosenPath will be empty.
func DiscoverConfigFile(dir string, candidates []string) ConfigDiscoveryResult {
	result := ConfigDiscoveryResult{}

	for _, candidate := range candidates {
		path := filepath.Join(dir, candidate)
		if _, err := os.Stat(path); err == nil {
			result.AllFound = append(result.AllFound, path)
			if result.ChosenPath == "" {
				result.ChosenPath = path
			}
		}
	}

	return result
}

// GetParserForExtension returns the appropriate koanf parser based on file extension.
// Supported extensions: .yml, .yaml, .toml, .json
// Returns nil for unsupported extensions.
func GetParserForExtension(path string) koanf.Parser {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".yml", ".yaml":
		return yaml.Parser()
	case ".toml":
		return toml.Parser()
	case ".json":
		return json.Parser()
	default:
		return nil
	}
}
