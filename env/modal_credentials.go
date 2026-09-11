package env

import (
	"os"
	"path/filepath"

	"github.com/pelletier/go-toml/v2"
)

// ModalCredentialsConfigured reports whether local credential resolution yields a
// token pair. It does not authenticate with Modal.
func ModalCredentialsConfigured() (bool, error) {
	configPath := os.Getenv("MODAL_CONFIG_PATH")
	if configPath == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return false, err
		}
		configPath = filepath.Join(home, ".modal.toml")
	}

	type profile struct {
		Active      bool   `toml:"active"`
		TokenID     string `toml:"token_id"`
		TokenSecret string `toml:"token_secret"`
	}
	profiles := map[string]profile{}
	data, err := os.ReadFile(configPath)
	if err != nil && !os.IsNotExist(err) {
		return false, err
	}
	if err == nil {
		if err := toml.Unmarshal(data, &profiles); err != nil {
			return false, err
		}
	}

	name := os.Getenv("MODAL_PROFILE")
	if name == "" {
		for key, p := range profiles {
			if p.Active {
				name = key
				break
			}
		}
	}
	selected := profiles[name]
	id, secret := os.Getenv("MODAL_TOKEN_ID"), os.Getenv("MODAL_TOKEN_SECRET")
	if id == "" {
		id = selected.TokenID
	}
	if secret == "" {
		secret = selected.TokenSecret
	}
	return id != "" && secret != "", nil
}
