package dev

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"sidekick/common"
	"sidekick/env"
	"sidekick/persisted_ai"
	"sidekick/secret_manager"
)

// DiffSummarizeMaxChars is the maximum character limit for summarized diffs.
// This is a fixed value to ensure deterministic workflow replays.
const DiffSummarizeMaxChars = 15000

// SummarizeDiffActivityInput contains the inputs for SummarizeDiffActivity.
type SummarizeDiffActivityInput struct {
	GitDiff                string
	ReviewFeedback         string
	EnvContainer           env.EnvContainer
	ModelConfig            common.ModelConfig
	SecretManagerContainer secret_manager.SecretManagerContainer
}

// SummarizeDiffActivity summarizes a git diff to fit within the character budget.
// If the diff is small enough, it returns it unchanged. Otherwise, it ranks chunks
// by relevance to the review feedback and truncates to fit.
func SummarizeDiffActivity(ctx context.Context, input SummarizeDiffActivityInput) (string, error) {
	if len(input.GitDiff) <= DiffSummarizeMaxChars {
		return input.GitDiff, nil
	}

	embedder, err := persisted_ai.GetEmbedder(input.ModelConfig)
	if err != nil {
		return "", fmt.Errorf("failed to get embedder: %w", err)
	}

	baseDir := input.EnvContainer.Env.GetWorkingDirectory()
	contentProvider := func(filePath string) (string, error) {
		fullPath := filepath.Join(baseDir, filePath)
		content, err := os.ReadFile(fullPath)
		if err != nil {
			return "", err
		}
		return string(content), nil
	}

	opts := persisted_ai.DiffSummarizeOptions{
		GitDiff:         input.GitDiff,
		ReviewFeedback:  input.ReviewFeedback,
		MaxChars:        DiffSummarizeMaxChars,
		ModelConfig:     input.ModelConfig,
		SecretManager:   input.SecretManagerContainer.SecretManager,
		Embedder:        embedder,
		ContentProvider: contentProvider,
	}

	return persisted_ai.SummarizeDiff(ctx, opts)
}
