package dev

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"sidekick/coding"
	"sidekick/coding/tree_sitter"
	"sidekick/common"
	"sidekick/env"

	"github.com/stretchr/testify/require"
)

// Run explicitly with SIDE_MODAL_TOOLS_PERF=true. A stable sandbox name keeps
// provisioning out of repeated measurements; its idle watchdog handles shutdown.
func TestModalToolsPerformance(t *testing.T) {
	if os.Getenv("SIDE_MODAL_TOOLS_PERF") != "true" {
		t.Skip("set SIDE_MODAL_TOOLS_PERF=true to measure real Modal tools")
	}
	require.False(t, common.IsActiveEnvNonLocal(), "run from a host with Modal credentials")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	base := setupModalSandbox(t, ctx, "sidekick-tools-perf")
	require.True(t, ensureRipgrep(ctx, base), "ripgrep is required")
	remoteDir := initContainerGitRepo(t, ctx, base, "/root/sidekick-tools-perf-repo")
	remote := &env.ModalEnv{
		WorkingDirectory: remoteDir,
		SandboxName:      base.SandboxName,
		SSHHost:          base.SSHHost,
		SSHPort:          base.SSHPort,
	}
	localDir := t.TempDir()
	output, err := exec.CommandContext(ctx, "git", "-C", localDir, "init").CombinedOutput()
	require.NoError(t, err, "%s", output)
	remote.LocalRepoDir = localDir
	local, err := env.NewLocalEnv(ctx, env.LocalEnvParams{RepoDir: localDir})
	require.NoError(t, err)

	for i := range 100 {
		name := fmt.Sprintf("file%03d.go", i)
		content := []byte(fmt.Sprintf("package fixture\n\nfunc Symbol%03d() string { return \"needle%03d\" }\n", i, i))
		require.NoError(t, os.WriteFile(filepath.Join(localDir, name), content, 0644))
	}
	for _, args := range [][]string{
		{"add", "."},
		{"-c", "user.name=Test", "-c", "user.email=test@test.com", "commit", "-m", "fixture"},
		{"bundle", "create", "fixture.bundle", "--all"},
	} {
		output, err := exec.CommandContext(ctx, "git", append([]string{"-C", localDir}, args...)...).CombinedOutput()
		require.NoError(t, err, "%s", output)
	}
	bundle, err := os.ReadFile(filepath.Join(localDir, "fixture.bundle"))
	require.NoError(t, err)
	require.NoError(t, os.Remove(filepath.Join(localDir, "fixture.bundle")))
	require.NoError(t, remote.WriteFile(ctx, "fixture.bundle", bundle, 0644))
	out, err := remote.RunCommand(ctx, env.EnvRunCommandInput{
		Command: "sh", Args: []string{"-c", "git fetch ./fixture.bundle HEAD && git reset --hard FETCH_HEAD && rm fixture.bundle"},
	})
	require.NoError(t, err)
	require.Zero(t, out.ExitStatus, "%s", out.Stderr)

	ca := &coding.CodingActivities{TreeSitterActivities: &tree_sitter.TreeSitterActivities{}}
	requests := make([]coding.FileSymDefRequest, 10)
	searches := make([]SingleSearchParams, 10)
	for i := range requests {
		requests[i] = coding.FileSymDefRequest{
			FilePath: fmt.Sprintf("file%03d.go", i),
			Symbols:  []coding.RequestedSymbol{{Name: fmt.Sprintf("Symbol%03d", i)}},
		}
		searches[i] = SingleSearchParams{PathGlob: "*.go", SearchTerm: fmt.Sprintf("needle%03d", i)}
	}

	for _, target := range []struct {
		name string
		env  env.Env
	}{{"local", local}, {"modal", remote}} {
		t.Run(target.name, func(t *testing.T) {
			container := env.EnvContainer{Env: target.env}
			cases := []struct {
				name string
				run  func(t *testing.T)
			}{
				{"definitions", func(t *testing.T) {
					result, err := ca.BulkGetSymbolDefinitions(ctx, coding.DirectorySymDefRequest{
						EnvContainer: container, Requests: requests,
					})
					require.NoError(t, err)
					require.Empty(t, result.Failures)
					for i := range requests {
						require.Contains(t, result.SymbolDefinitions, fmt.Sprintf("func Symbol%03d()", i))
					}
				}},
				{"missing-symbol", func(t *testing.T) {
					result, err := ca.BulkGetSymbolDefinitions(ctx, coding.DirectorySymDefRequest{
						EnvContainer: container,
						Requests: []coding.FileSymDefRequest{{
							FilePath: "file000.go", Symbols: []coding.RequestedSymbol{{Name: "Symbol099"}},
						}},
					})
					require.NoError(t, err)
					require.Contains(t, result.Failures, "file099.go")
				}},
				{"bulk-search", func(t *testing.T) {
					result, err := BulkSearchRepositoryActivity(ctx, BulkSearchRepositoryActivityInput{
						EnvContainer: container,
						Params:       BulkSearchRepositoryParams{ContextLines: 2, Searches: searches},
					})
					require.NoError(t, err)
					for i := range searches {
						require.Contains(t, result.Result, fmt.Sprintf("func Symbol%03d() string { return \"needle%03d\" }", i, i))
					}
				}},
			}
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					tc.run(t)
					durations := make([]time.Duration, 5)
					for i := range durations {
						start := time.Now()
						tc.run(t)
						durations[i] = time.Since(start)
					}
					sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
					t.Logf("warm latency: min=%s median=%s max=%s", durations[0], durations[2], durations[4])
				})
			}
		})
	}
}
