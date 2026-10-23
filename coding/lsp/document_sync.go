package lsp

import (
	"context"
	"errors"
	"fmt"
	"time"

	"sidekick/env"
	"sidekick/utils"
)

type SyncDocumentActivityInput struct {
	EnvContainer env.EnvContainer `json:"envContainer"`
	FilePath     string           `json:"filePath"`
	Content      string           `json:"content"`
}

type SyncDocumentActivityOutput struct{}

func (la *LSPActivities) SyncDocumentActivity(ctx context.Context, input SyncDocumentActivityInput) (_ SyncDocumentActivityOutput, resultErr error) {
	if input.EnvContainer.Env == nil {
		return SyncDocumentActivityOutput{}, fmt.Errorf("document sync requires an environment")
	}
	baseDir := input.EnvContainer.Env.GetWorkingDirectory()
	lang := utils.InferLanguageNameFromFilePath(input.FilePath)
	ctx, client, release, err := la.findOrInitClient(ctx, baseDir, lang, &input.EnvContainer)
	if err != nil {
		return SyncDocumentActivityOutput{}, err
	}
	defer release()
	defer func() {
		if resultErr != nil {
			resultErr = errors.Join(resultErr, la.Discard())
		}
	}()

	if err := didOpen(ctx, client, TextDocumentDidOpenActivityInput{
		RepoDir: baseDir, FilePath: input.FilePath, LanguageID: "go",
		Version: 1, Text: input.Content,
	}); err != nil {
		return SyncDocumentActivityOutput{}, err
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		resultErr = errors.Join(resultErr, didClose(cleanupCtx, client, TextDocumentDidCloseActivityInput{
			RepoDir: baseDir, FilePath: input.FilePath,
		}))
	}()
	if err := didChange(ctx, client, TextDocumentDidChangeActivityInput{
		RepoDir: baseDir, FilePath: input.FilePath, Version: 2,
		ContentChanges: []TextDocumentContentChangeEvent{{Text: input.Content}},
	}); err != nil {
		return SyncDocumentActivityOutput{}, err
	}
	return SyncDocumentActivityOutput{}, didSave(ctx, client, TextDocumentDidSaveActivityInput{
		RepoDir: baseDir, FilePath: input.FilePath, Text: &input.Content,
	})
}
