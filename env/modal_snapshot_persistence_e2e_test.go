package env

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"sidekick/common"

	"github.com/segmentio/ksuid"
	"github.com/stretchr/testify/require"
)

func TestModalStagedSnapshotPersistenceIntegration(t *testing.T) {
	if os.Getenv("SIDE_E2E_TEST") != "true" {
		t.Skip("requires SIDE_E2E_TEST=true and Modal credentials")
	}
	if common.IsActiveEnvNonLocal() {
		t.Skip("Modal credentials are unavailable in non-local environments")
	}
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()

	client, err := getModalClient()
	require.NoError(t, err)
	name := E2ESandboxName("side-e2e-persist-" + strings.ToLower(ksuid.New().String()[:10]))
	config := common.ModalEnvConfig{
		Image:                 "debian:bookworm-slim",
		CPU:                   0.25,
		Memory:                512,
		IdleSeconds:           3600,
		ActiveSnapshotSeconds: -1,
	}
	created, err := modalCreateSandbox(ctx, ModalCreateSandboxInput{Name: name, Config: config})
	require.NoError(t, err)
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cleanupCancel()
		_, err := DeleteSandboxActivity(cleanupCtx, DeleteSandboxInput{EnvType: EnvTypeModal, SandboxName: name})
		require.NoError(t, err)
	})
	e := &ModalEnv{
		SandboxName:      name,
		SSHHost:          created.SSHHost,
		SSHPort:          created.SSHPort,
		WorkingDirectory: "/root",
	}
	run := func(script string) EnvRunCommandOutput {
		t.Helper()
		out, err := e.RunCommand(ctx, EnvRunCommandInput{Command: "sh", Args: []string{"-c", script}})
		require.NoError(t, err)
		require.Zero(t, out.ExitStatus, "%s%s", out.Stdout, out.Stderr)
		return out
	}
	checkpoint := func() *modalSnapshotRecord {
		t.Helper()
		sb, err := findModalSandbox(ctx, client, name)
		require.NoError(t, err)
		require.NotNil(t, sb)
		stdout, stderr, exit, err := modalExecCapture(ctx, sb, "/usr/local/bin/sidekick-snapshot snapshot")
		require.NoError(t, err)
		require.Zero(t, exit, "%s%s", stdout, stderr)
		record, err := modalLatestSnapshot(ctx, client, name)
		require.NoError(t, err)
		require.NotNil(t, record)
		return record
	}
	terminate := func() {
		t.Helper()
		sb, err := findModalSandbox(ctx, client, name)
		require.NoError(t, err)
		require.NotNil(t, sb)
		t.Logf("terminating %s without a shutdown snapshot", sb.SandboxID)
		_, err = sb.Terminate(ctx, nil)
		require.NoError(t, err)
		require.NoError(t, waitForModalSandboxGone(ctx, client, name))
	}

	run("git init -q persistence && cd persistence && printf 'A\\n' > fixture && git add fixture")
	a := checkpoint()
	run("cd persistence && printf 'B\\n' > fixture && git add fixture")
	require.Equal(t, "B\nB\n", run("cd persistence && cat fixture && git show :fixture").Stdout)

	terminate()
	restoredA := run("cd persistence && cat fixture && git show :fixture")
	require.Equal(t, "A\nA\n", restoredA.Stdout, "uncheckpointed work and index must both revert in this control")
	require.Contains(t, restoredA.Stderr, a.ImageId)
	require.Contains(t, restoredA.Stderr, "lost")
	t.Logf("confirmed rollback of staged B to checkpoint A (%s), with caller warning", a.ImageId)

	run("cd persistence && printf 'B\\n' > fixture && git add fixture")
	b := checkpoint()
	require.NotEqual(t, a.ImageId, b.ImageId)
	terminate()
	restoredB := run("cd persistence && cat fixture && git show :fixture")
	require.Equal(t, "B\nB\n", restoredB.Stdout, "a fresh checkpoint must preserve both the file and index")
	require.Contains(t, restoredB.Stderr, b.ImageId)
	t.Logf("confirmed preservation of staged B through checkpoint %s", b.ImageId)

	events, err := modalSnapshotEvents(ctx, client, name, 100)
	require.NoError(t, err)
	require.NotEmpty(t, events)
	t.Logf("durable guard events:\n%s", strings.Join(events, "\n"))
}
