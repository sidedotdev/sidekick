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
	if err != nil {
		errStr := err.Error()
		if strings.Contains(errStr, "429") || strings.Contains(errStr, "RESOURCE_EXHAUSTED") ||
			strings.Contains(errStr, "quota") || strings.Contains(errStr, "401") ||
			strings.Contains(errStr, "authentication") {
			t.Skipf("Skipping due to API quota/auth issue: %v", err)
		}
	}
	require.NoError(t, err)
	assert.Len(t, results, 2)
	assert.Greater(t, len(results[0]), 0)
	assert.Greater(t, len(results[1]), 0)
}
