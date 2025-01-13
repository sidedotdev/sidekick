package common

type ModelConfig struct {
	// the provider name
	Provider string `koanf:"provider" json:"provider"`
	Model    string `koanf:"model,omitempty" json:"model,omitempty"`
}
