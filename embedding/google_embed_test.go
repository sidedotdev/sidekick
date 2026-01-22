package embedding

import (
	"context"
	"os"
	"sidekick/common"
	"sidekick/secret_manager"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGoogleEmbedderIntegration(t *testing.T) {
	t.Parallel()
	if os.Getenv("SIDE_INTEGRATION_TEST") != "true" {
		t.Skip("Skipping integration test; SIDE_INTEGRATION_TEST not set")
	}

	ctx := context.Background()
	embedder := GoogleEmbedder{}

	secrets := secret_manager.SecretManagerContainer{
		SecretManager: secret_manager.NewCompositeSecretManager([]secret_manager.SecretManager{
			secret_manager.EnvSecretManager{},
			secret_manager.KeyringSecretManager{},
			secret_manager.LocalConfigSecretManager{},
		}),
	}

	modelConfig := common.ModelConfig{
		Provider: "google",
		Model:    GoogleDefaultModel,
	}

	inputs := []string{"hello world", "test embedding"}
	results, err := embedder.Embed(ctx, modelConfig, secrets, inputs, TaskTypeRetrievalDocument)
	if err != nil && strings.Contains(err.Error(), "RESOURCE_EXHAUSTED") {
		t.Skip("Skipping test due to Google API quota exhaustion (429)")
	}
	require.NoError(t, err)
	assert.Len(t, results, 2)
	assert.Greater(t, len(results[0]), 0)
	assert.Greater(t, len(results[1]), 0)
}
