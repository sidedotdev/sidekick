package secret_manager

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLocalConfigUsesProviderNames(t *testing.T) {
	tests := []struct {
		name      string
		providers string
		secret    string
		want      string
	}{
		{
			name:      "unnamed builtin infers its name",
			providers: "providers:\n  - type: cohere\n    key: inferred-key\n",
			secret:    "COHERE_API_KEY",
			want:      "inferred-key",
		},
		{
			name:      "explicit name selects credentials",
			providers: "providers:\n  - name: ranking-service\n    type: cohere\n    key: named-key\n",
			secret:    "RANKING_SERVICE_API_KEY",
			want:      "named-key",
		},
		{
			name:      "explicit name replaces inferred cohere name",
			providers: "providers:\n  - name: ranking-service\n    type: cohere\n    key: named-key\n",
			secret:    "COHERE_API_KEY",
		},
		{
			name:      "explicit name replaces inferred openai name",
			providers: "providers:\n  - name: private-openai\n    type: openai\n    key: named-key\n",
			secret:    "OPENAI_API_KEY",
		},
		{
			name:      "explicit name replaces inferred anthropic name",
			providers: "providers:\n  - name: private-anthropic\n    type: anthropic\n    key: named-key\n",
			secret:    "ANTHROPIC_API_KEY",
		},
		{
			name:      "name determines identity independently of type",
			providers: "providers:\n  - name: openai\n    type: openai_compatible\n    key: proxy-key\n",
			secret:    "OPENAI_API_KEY",
			want:      "proxy-key",
		},
		{
			name: "same type does not make different names ambiguous",
			providers: `providers:
  - name: cohere
    type: cohere
    key: default-key
  - name: ranking-service
    type: cohere
    key: named-key
`,
			secret: "COHERE_API_KEY",
			want:   "default-key",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			writeCohereLocalConfig(t, tt.providers)
			key, err := (LocalConfigSecretManager{}).GetSecret(tt.secret)
			if tt.want == "" {
				require.ErrorIs(t, err, ErrSecretNotFound)
				assert.Empty(t, key)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.want, key)
			}
		})
	}
}
