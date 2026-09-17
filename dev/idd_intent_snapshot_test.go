package dev

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"sidekick/env"

	"github.com/stretchr/testify/require"
)

func TestIddIntentSnapshotDeltas(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "%s", out)
		return string(out)
	}
	git("init")
	git("-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "--allow-empty", "-m", "base")
	require.NoError(t, os.Mkdir(filepath.Join(dir, "intent"), 0755))
	path := filepath.Join(dir, "intent", "feature.md")
	require.NoError(t, os.WriteFile(path, []byte("Staged requirement\n"), 0644))
	git("add", "intent/feature.md")
	require.NoError(t, os.WriteFile(path, []byte("First requirement\n"), 0644))
	container := env.EnvContainer{Env: &env.LocalEnv{WorkingDirectory: dir}}
	indexBefore := git("ls-files", "--stage")
	first, err := captureIntentSnapshotForTest(context.Background(), IddIntentSnapshotInput{
		EnvContainer: container, OriginalBase: "HEAD",
	})
	require.NoError(t, err)
	require.Contains(t, first.Diff, "+First requirement")
	require.Equal(t, indexBefore, git("ls-files", "--stage"))

	unchanged, err := captureIntentSnapshotForTest(context.Background(), IddIntentSnapshotInput{
		EnvContainer: container, Base: first.Tree, OriginalBase: "HEAD",
	})
	require.NoError(t, err)
	require.Empty(t, unchanged.Diff)

	require.NoError(t, os.WriteFile(path, []byte("First requirement\nSecond requirement\n"), 0644))
	second, err := captureIntentSnapshotForTest(context.Background(), IddIntentSnapshotInput{
		EnvContainer: container, Base: first.Tree, OriginalBase: "HEAD",
	})
	require.NoError(t, err)
	require.Contains(t, second.Diff, "+Second requirement")
	require.NotContains(t, second.Diff, "+First requirement")

	require.NoError(t, os.Remove(path))
	deleted, err := captureIntentSnapshotForTest(context.Background(), IddIntentSnapshotInput{
		EnvContainer: container, Base: second.Tree, OriginalBase: "HEAD",
	})
	require.NoError(t, err)
	require.Contains(t, deleted.Diff, "-First requirement")
	require.Contains(t, deleted.Diff, "-Second requirement")
	require.Equal(t, indexBefore, git("ls-files", "--stage"))
}

func TestIddIntentSnapshotRecovery(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		output, err := cmd.CombinedOutput()
		require.NoError(t, err, "%s", output)
	}
	runGit("init")
	runGit("-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "--allow-empty", "-m", "base")
	require.NoError(t, os.Mkdir(filepath.Join(dir, "intent"), 0755))
	path := filepath.Join(dir, "intent", "feature.md")
	require.NoError(t, os.WriteFile(path, []byte("Requirement\n"), 0644))
	container := env.EnvContainer{Env: &env.LocalEnv{WorkingDirectory: dir}}
	input := IddIntentSnapshotInput{
		EnvContainer: container, Base: "missing-snapshot", OriginalBase: "HEAD",
	}
	first, err := captureIntentSnapshotForTest(context.Background(), input)
	require.NoError(t, err)
	require.True(t, first.Full)
	require.Contains(t, first.Diff, "+Requirement")
	retried, err := captureIntentSnapshotForTest(context.Background(), input)
	require.NoError(t, err)
	require.Equal(t, first, retried)

	require.NoError(t, os.Remove(path))
	empty, err := captureIntentSnapshotForTest(context.Background(), input)
	require.NoError(t, err)
	require.True(t, empty.Full)
	require.Empty(t, empty.Diff)

	input.OriginalBase = "missing-original"
	_, err = captureIntentSnapshotForTest(context.Background(), input)
	require.Error(t, err)
}

func captureIntentSnapshotForTest(ctx context.Context, input IddIntentSnapshotInput) (IddIntentSnapshotResult, error) {
	command := iddIntentSnapshotCommand(input)
	output, err := command.EnvContainer.Env.RunCommand(ctx, env.EnvRunCommandInput{
		RelativeWorkingDir: command.RelativeWorkingDir,
		Command:            command.Command,
		Args:               command.Args,
	})
	if err != nil {
		return IddIntentSnapshotResult{}, err
	}
	return parseIddIntentSnapshot(output)
}

func TestParseIddIntentSnapshotRejectsTruncation(t *testing.T) {
	t.Parallel()
	for _, output := range []string{
		"tree\nincremental\noriginal\npartial diff",
		"tree\nincremental\noriginal\npartial diff\nIDD_SNAPSHOT_COMPLETE\n\n\n[... truncated 100 bytes from the middle ...]\n\n",
	} {
		_, err := parseIddIntentSnapshot(env.EnvRunCommandActivityOutput{Stdout: output})
		require.ErrorContains(t, err, "incomplete or truncated")
	}
}
