package dev

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"sidekick/common"
	"sidekick/embedding"
	"sidekick/env"
	"sidekick/persisted_ai"
	"sidekick/secret_manager"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSummarizeDiffActivity_SmallDiff(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()

	smallDiff := `diff --git a/foo.go b/foo.go
--- a/foo.go
+++ b/foo.go
@@ -1,3 +1,4 @@
 package main
+// small change
 func main() {}
`

	input := SummarizeDiffActivityInput{
		GitDiff:        smallDiff,
		ReviewFeedback: "looks good",
		EnvContainer: env.EnvContainer{
			Env: &env.LocalEnv{WorkingDirectory: tempDir},
		},
		ModelConfig: common.ModelConfig{
			Provider: "openai",
			Model:    "text-embedding-3-small",
		},
		SecretManagerContainer: secret_manager.SecretManagerContainer{
			SecretManager: secret_manager.MockSecretManager{},
		},
	}

	result, err := SummarizeDiffActivity(context.Background(), input)
	require.NoError(t, err)
	assert.Equal(t, smallDiff, result, "small diff should be returned unchanged")
}

func TestSummarizeDiffActivity_LargeDiffTruncated(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()

	// Create file content for the diff
	err := os.WriteFile(filepath.Join(tempDir, "largefile.go"), []byte("package main\n"), 0644)
	require.NoError(t, err)

	// Create a diff that exceeds a small budget
	var largeDiff strings.Builder
	largeDiff.WriteString("diff --git a/largefile.go b/largefile.go\n")
	largeDiff.WriteString("--- a/largefile.go\n")
	largeDiff.WriteString("+++ b/largefile.go\n")
	largeDiff.WriteString("@@ -1,1 +1,100 @@\n")
	largeDiff.WriteString(" package main\n")
	for i := 0; i < 100; i++ {
		largeDiff.WriteString("+// added line number ")
		largeDiff.WriteString(string(rune('0' + i/10)))
		largeDiff.WriteString(string(rune('0' + i%10)))
		largeDiff.WriteString(" with some extra content to make it longer\n")
	}

	// Use a small maxChars to force summarization
	maxChars := 500
	diffStr := largeDiff.String()
	require.Greater(t, len(diffStr), maxChars, "test diff must exceed budget")

	// Test the underlying SummarizeDiff directly with a mock embedder
	mockEmbedder := &embedding.MockEmbedder{}
	opts := persisted_ai.DiffSummarizeOptions{
		GitDiff:        diffStr,
		ReviewFeedback: "please review",
		MaxChars:       maxChars,
		ModelConfig: common.ModelConfig{
			Provider: "mock",
			Model:    "mock-model",
		},
		SecretManager: secret_manager.MockSecretManager{},
		Embedder:      mockEmbedder,
		ContentProvider: func(filePath string) (string, error) {
			content, err := os.ReadFile(filepath.Join(tempDir, filePath))
			return string(content), err
		},
	}

	result, err := persisted_ai.SummarizeDiff(context.Background(), opts)
	require.NoError(t, err)
	assert.LessOrEqual(t, len(result), maxChars, "output should respect the character budget")
	assert.NotEqual(t, diffStr, result, "large diff should be summarized, not returned as-is")
}

func TestSummarizeDiffActivity_EmptyDiff(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()

	input := SummarizeDiffActivityInput{
		GitDiff:        "",
		ReviewFeedback: "no changes",
		EnvContainer: env.EnvContainer{
			Env: &env.LocalEnv{WorkingDirectory: tempDir},
		},
		ModelConfig: common.ModelConfig{
			Provider: "openai",
			Model:    "text-embedding-3-small",
		},
		SecretManagerContainer: secret_manager.SecretManagerContainer{
			SecretManager: secret_manager.MockSecretManager{},
		},
	}

	result, err := SummarizeDiffActivity(context.Background(), input)
	require.NoError(t, err)
	assert.Equal(t, "", result, "empty diff should return empty string")
}

func TestSummarizeDiffActivity_BudgetCalculation(t *testing.T) {
	t.Parallel()

	// Verify that the hard-coded budget constant is set correctly
	assert.Equal(t, 15000, DiffSummarizeMaxChars, "DiffSummarizeMaxChars should be 15000")
}
