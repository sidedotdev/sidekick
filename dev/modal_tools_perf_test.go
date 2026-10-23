package dev

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"testing"
	"time"

	"sidekick/coding"
	"sidekick/coding/lsp"
	"sidekick/common"
	"sidekick/env"

	"github.com/stretchr/testify/require"
)

// Run explicitly with SIDE_MODAL_TOOLS_PERF=true. The fixture is this
// repository's own checkout, exercised with the kinds of searches and symbol
// lookups an agent issues while working on it. A stable sandbox name keeps
// provisioning (including the one-time Go/gopls install) out of repeated
// measurements; its idle watchdog handles shutdown.
func TestModalToolsPerformance(t *testing.T) {
	if os.Getenv("SIDE_MODAL_TOOLS_PERF") != "true" {
		t.Skip("set SIDE_MODAL_TOOLS_PERF=true to measure real Modal tools")
	}
	require.False(t, common.IsActiveEnvNonLocal(), "run from a host with Modal credentials")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()

	localDir := devToolsRepoRoot(t)
	base := setupModalSandbox(t, ctx, "sidekick-tools-perf")
	require.True(t, ensureRipgrep(ctx, base), "ripgrep is required")
	require.True(t, ensureGopls(ctx, base), "gopls is required")
	remote := &env.ModalEnv{
		WorkingDirectory: "/root/sidekick-tools-perf-repo",
		SandboxName:      base.SandboxName,
		SSHHost:          base.SSHHost,
		SSHPort:          base.SSHPort,
		LocalRepoDir:     localDir,
	}
	syncRepoSourceTree(t, ctx, localDir, base, remote.WorkingDirectory)
	local, err := env.NewLocalEnv(ctx, env.LocalEnvParams{RepoDir: localDir})
	require.NoError(t, err)

	ca := &coding.CodingActivities{
		LSPActivities: lsp.NewLSPActivities(func(language string) lsp.LSPClient {
			return &lsp.Jsonrpc2LSPClient{LanguageName: language}
		}),
	}
	t.Cleanup(func() { require.NoError(t, ca.LSPActivities.Close()) })

	searches := []SingleSearchParams{
		{PathGlob: "coding/lsp/*.go", SearchTerm: "func NewLSPActivities"},
		{PathGlob: "coding/lsp/core/*.go", SearchTerm: "type Sessions struct"},
		{PathGlob: "env/*.go", SearchTerm: "WalkEntryWithContent"},
		{PathGlob: "**/*.go", SearchTerm: "agentSourceFiles"},
		{PathGlob: "repotools/*.go", SearchTerm: "IncludeRelatedSymbols"},
	}
	definitions := []coding.FileSymDefRequest{
		{FilePath: "coding/lsp/core/sessions.go", Symbols: []coding.RequestedSymbol{{Name: "Sessions"}, {Name: "Acquire"}}},
		{FilePath: "repotools/search.go", Symbols: []coding.RequestedSymbol{{Name: "searchContext"}, {Name: "executeMainSearch"}}},
		{FilePath: "sideagent/server.go", Symbols: []coding.RequestedSymbol{{Name: "server"}, {Name: "execute"}}},
		{FilePath: "env/environment.go", Symbols: []coding.RequestedSymbol{{Name: "Env"}}},
	}
	// Resolving a symbol from a file that only references it goes through the
	// language server rather than tree-sitter alone.
	lspDefinitions := []coding.FileSymDefRequest{
		{FilePath: "sideagent/symbol_definitions.go", Symbols: []coding.RequestedSymbol{
			{Name: "SymbolDefinitions", ReferenceLine: "tool := repotools.SymbolDefinitions{"},
		}},
	}
	relatedDefinitions := []coding.FileSymDefRequest{
		{FilePath: "common/agent_binary.go", Symbols: []coding.RequestedSymbol{{Name: "agentSourceFiles"}}},
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
				// Transport floor: one round trip with no repository work.
				{"noop-command", func(t *testing.T) {
					out, err := target.env.RunCommand(ctx, env.EnvRunCommandInput{Command: "true"})
					require.NoError(t, err)
					require.Zero(t, out.ExitStatus)
				}},
				// Tool-RPC floor: the definitions path with nothing to resolve.
				{"definitions-empty", func(t *testing.T) {
					result, err := ca.BulkGetSymbolDefinitions(ctx, coding.DirectorySymDefRequest{EnvContainer: container})
					require.NoError(t, err)
					require.Empty(t, result.Failures)
				}},
				{"bulk-search", func(t *testing.T) {
					result, err := BulkSearchRepositoryActivity(ctx, BulkSearchRepositoryActivityInput{
						EnvContainer: container,
						Params:       BulkSearchRepositoryParams{ContextLines: 2, Searches: searches},
					})
					require.NoError(t, err)
					require.Contains(t, result.Result, "func NewLSPActivities(")
					require.Contains(t, result.Result, "type Sessions struct")
					require.Contains(t, result.Result, "var agentSourceFiles")
				}},
				{"definitions", func(t *testing.T) {
					result, err := ca.BulkGetSymbolDefinitions(ctx, coding.DirectorySymDefRequest{
						EnvContainer: container, Requests: definitions,
					})
					require.NoError(t, err)
					require.Empty(t, result.Failures)
					require.Contains(t, result.SymbolDefinitions, "func (s *Sessions) Acquire(")
					require.Contains(t, result.SymbolDefinitions, "type Env interface")
				}},
				{"definitions-via-lsp", func(t *testing.T) {
					result, err := ca.BulkGetSymbolDefinitions(ctx, coding.DirectorySymDefRequest{
						EnvContainer: container, Requests: lspDefinitions,
					})
					require.NoError(t, err)
					require.Empty(t, result.Failures)
					require.Contains(t, result.SymbolDefinitions, "type SymbolDefinitions struct")
				}},
				{"definitions-with-related", func(t *testing.T) {
					result, err := ca.BulkGetSymbolDefinitions(ctx, coding.DirectorySymDefRequest{
						EnvContainer: container, Requests: relatedDefinitions, IncludeRelatedSymbols: true,
					})
					require.NoError(t, err)
					require.Empty(t, result.Failures)
					require.Contains(t, result.SymbolDefinitions, "agentSourceFiles is referenced")
				}},
				{"missing-symbol", func(t *testing.T) {
					result, err := ca.BulkGetSymbolDefinitions(ctx, coding.DirectorySymDefRequest{
						EnvContainer: container,
						Requests: []coding.FileSymDefRequest{{
							FilePath: "dev/search_repository.go", Symbols: []coding.RequestedSymbol{{Name: "NewLSPActivities"}},
						}},
					})
					require.NoError(t, err)
					require.Contains(t, result.Failures, "coding/lsp/find_references_activity.go")
				}},
			}
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					start := time.Now()
					tc.run(t)
					cold := time.Since(start)
					durations := make([]time.Duration, 5)
					for i := range durations {
						start := time.Now()
						tc.run(t)
						durations[i] = time.Since(start)
					}
					sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
					t.Logf("cold=%s warm: min=%s median=%s max=%s", cold, durations[0], durations[2], durations[4])
				})
			}
		})
	}
}

// ensureGopls installs Go and gopls into the sandbox once; both persist with
// the sandbox so later runs only pay the lookup.
func ensureGopls(ctx context.Context, e env.Env) bool {
	script := strings.Join([]string{
		"if command -v gopls >/dev/null 2>&1; then exit 0; fi",
		"export PATH=$PATH:/usr/local/go/bin",
		"if ! command -v go >/dev/null 2>&1; then",
		"  arch=$(uname -m); case $arch in x86_64) arch=amd64;; aarch64) arch=arm64;; esac",
		"  version=$(curl -fsSL 'https://go.dev/VERSION?m=text' | head -n1)",
		"  curl -fsSL \"https://go.dev/dl/${version}.linux-${arch}.tar.gz\" | tar -C /usr/local -xz",
		"fi",
		"GOBIN=/usr/local/bin go install golang.org/x/tools/gopls@latest",
		"command -v gopls >/dev/null 2>&1",
	}, "\n")
	out, err := e.RunCommand(ctx, env.EnvRunCommandInput{Command: "bash", Args: []string{"-c", script}})
	return err == nil && out.ExitStatus == 0
}

// syncRepoSourceTree mirrors the local HEAD tree into the sandbox as a
// single-commit git repository. The checkout's history is far too large to
// bundle, and only the working tree matters to the tools; the tree hash marks
// the sandbox copy so unchanged sources are not re-uploaded.
func syncRepoSourceTree(t *testing.T, ctx context.Context, localDir string, base env.Env, remoteDir string) {
	t.Helper()
	treeHash, err := exec.CommandContext(ctx, "git", "-C", localDir, "rev-parse", "HEAD^{tree}").Output()
	require.NoError(t, err)
	marker := remoteDir + "/.side/tmp/perf-tree-hash"
	out, err := base.RunCommand(ctx, env.EnvRunCommandInput{Command: "cat", Args: []string{marker}})
	require.NoError(t, err)
	if out.ExitStatus == 0 && strings.TrimSpace(out.Stdout) == strings.TrimSpace(string(treeHash)) {
		return
	}

	archive, err := exec.CommandContext(ctx, "git", "-C", localDir, "archive", "--format=tar.gz", "HEAD").Output()
	require.NoError(t, err)
	require.NoError(t, base.WriteFile(ctx, "/root/sidekick-tools-perf-src.tar.gz", archive, 0644))
	script := strings.Join([]string{
		"rm -rf " + remoteDir,
		"mkdir -p " + remoteDir,
		"tar -C " + remoteDir + " -xzf /root/sidekick-tools-perf-src.tar.gz",
		"rm /root/sidekick-tools-perf-src.tar.gz",
		"cd " + remoteDir,
		"git init -q",
		"git config user.name Test",
		"git config user.email test@test.com",
		"git add -A",
		"git commit -qm fixture",
		"mkdir -p .side/tmp",
		fmt.Sprintf("printf %%s %s > %s", strings.TrimSpace(string(treeHash)), marker),
	}, " && ")
	out, err = base.RunCommand(ctx, env.EnvRunCommandInput{Command: "bash", Args: []string{"-c", script}})
	require.NoError(t, err)
	require.Zero(t, out.ExitStatus, "%s", out.Stderr)
}
