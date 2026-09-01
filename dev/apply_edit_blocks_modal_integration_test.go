package dev

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"testing"
	"time"

	"sidekick/coding/lsp"
	"sidekick/common"
	"sidekick/env"

	"github.com/segmentio/ksuid"
	"github.com/stretchr/testify/require"
)

// TestModalApplyEditBlocksIntegration exercises ApplyEditBlocks end to end on
// a Modal sandbox across the create, update and delete edit types. It requires
// Modal credentials and consumes Modal compute, so it is gated behind
// SIDE_E2E_TEST.
func TestModalApplyEditBlocksIntegration(t *testing.T) {
	if os.Getenv("SIDE_E2E_TEST") != "true" {
		t.Skip("skipping Modal apply edit blocks integration test; SIDE_E2E_TEST not set to true")
	}
	if common.IsActiveEnvNonLocal() {
		t.Skip("skipping Modal apply edit blocks integration test; credentials are unavailable in non-local sidekick environments")
	}

	ctx := context.Background()
	if deadline, ok := t.Deadline(); ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadline(ctx, deadline.Add(-10*time.Second))
		defer cancel()
	}

	baseEnv := setupModalSandbox(t, ctx, modalFixtureSandboxName)
	repoDir := initContainerGitRepo(t, ctx, baseEnv, "/tmp/modal-edit-blocks-repo-"+ksuid.New().String())
	repoEnv := &env.ModalEnv{
		WorkingDirectory: repoDir,
		SandboxName:      modalFixtureSandboxName,
		SSHHost:          baseEnv.SSHHost,
		SSHPort:          baseEnv.SSHPort,
	}
	envContainer := env.EnvContainer{Env: repoEnv}

	devActivities := &DevActivities{
		LSPActivities: &lsp.LSPActivities{
			LSPClientProvider: func(languageName string) lsp.LSPClient {
				return &lsp.Jsonrpc2LSPClient{LanguageName: languageName}
			},
			InitializedClients: map[string]lsp.LSPClient{},
		},
	}

	apply := func(t *testing.T, block EditBlock) ApplyEditBlockReport {
		t.Helper()
		reports, err := devActivities.ApplyEditBlocks(ctx, ApplyEditBlockActivityInput{
			EnvContainer: envContainer,
			EditBlocks:   []EditBlock{block},
		})
		require.NoError(t, err)
		require.Len(t, reports, 1)
		return reports[0]
	}

	filePath := "notes/example.txt"

	report := apply(t, EditBlock{
		EditType:       "create",
		FilePath:       filePath,
		NewLines:       []string{"hello", "world"},
		SequenceNumber: 1,
	})
	require.Empty(t, report.Error)
	require.True(t, report.DidApply)
	content, err := repoEnv.ReadFile(ctx, filePath)
	require.NoError(t, err)
	require.Equal(t, "hello\nworld", string(content))

	report = apply(t, EditBlock{
		EditType:       "update",
		FilePath:       filePath,
		OldLines:       []string{"world"},
		NewLines:       []string{"modal"},
		SequenceNumber: 2,
	})
	require.Empty(t, report.Error)
	require.True(t, report.DidApply)
	content, err = repoEnv.ReadFile(ctx, filePath)
	require.NoError(t, err)
	require.Equal(t, "hello\nmodal", string(content))

	report = apply(t, EditBlock{
		EditType:       "delete",
		FilePath:       filePath,
		SequenceNumber: 3,
	})
	require.Empty(t, report.Error)
	require.True(t, report.DidApply)
	_, err = repoEnv.Stat(ctx, filePath)
	require.Error(t, err)
	require.True(t, errors.Is(err, fs.ErrNotExist))
	require.True(t, os.IsNotExist(err), "remote Stat not-exist errors must satisfy os.IsNotExist, got %v", err)
}
