package sideagent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestWorkspaceReadFiles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "empty"), nil, 0644))
	client := startTestChannel(t)
	resp, err := client.Exec(context.Background(), ExecRequest{
		Dir: dir,
		Workspace: &WorkspaceRequest{ReadFiles: []string{
			"a.go", filepath.Join(dir, "a.go"), "empty", "missing", ".",
		}},
	})
	require.NoError(t, err)
	require.Empty(t, resp.Error)
	var result WorkspaceResponse
	require.NoError(t, json.Unmarshal(resp.Stdout, &result))
	require.Equal(t, []byte("package a\n"), result.Files["a.go"])
	require.Equal(t, result.Files["a.go"], result.Files[filepath.Join(dir, "a.go")])
	_, ok := result.Files["empty"]
	require.True(t, ok)
	require.NotContains(t, result.Files, "missing")
	require.NotContains(t, result.Files, ".")
}

func TestWorkspaceSearch(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	client := startTestChannel(t)
	resp, err := client.Exec(context.Background(), ExecRequest{
		Dir: dir,
		Workspace: &WorkspaceRequest{Search: &WorkspaceSearch{
			ListCommand:   "printf 'src/a b.go\\nsrc/c.txt\\n'",
			SearchCommand: "printf '%s\\n'",
			PathGlob:      "*.go",
		}},
	})
	require.NoError(t, err)
	require.Empty(t, resp.Error)
	var result WorkspaceResponse
	require.NoError(t, json.Unmarshal(resp.Stdout, &result))
	require.Equal(t, []string{"src/a b.go", "src/c.txt"}, result.Search.AllFiles)
	require.Equal(t, []string{"src/a b.go"}, result.Search.MatchedFiles)
	require.Equal(t, "src/a b.go\n", string(result.Search.Output.Stdout))
}

func TestWorkspaceSearchCancellation(t *testing.T) {
	t.Parallel()
	client := startTestChannel(t)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := client.Exec(ctx, ExecRequest{Workspace: &WorkspaceRequest{Search: &WorkspaceSearch{
		ListCommand: "sleep 30; printf 'a.go\\n'", SearchCommand: "sleep 30", PathGlob: "*.go",
	}}})
	require.ErrorIs(t, err, context.DeadlineExceeded)
	resp, err := client.Exec(context.Background(), ExecRequest{Argv: []string{"true"}})
	require.NoError(t, err)
	require.Zero(t, resp.ExitStatus)
}

func TestWorkspacePrepareSearch(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".sideignore"), []byte("vendor\n"), 0644))
	client := startTestChannel(t)
	for range 2 {
		resp, err := client.Exec(context.Background(), ExecRequest{
			Dir: dir,
			Workspace: &WorkspaceRequest{PrepareSearch: &WorkspaceSearchSetup{
				IgnorePath: ".side/tmp/core_ignore", IgnoreContent: ".git\n",
			}},
		})
		require.NoError(t, err)
		require.Empty(t, resp.Error)
		var result WorkspaceResponse
		require.NoError(t, json.Unmarshal(resp.Stdout, &result))
		require.True(t, result.PreparedSearch.SideIgnoreExists)
		require.False(t, result.PreparedSearch.GitIgnoreExists)
		data, err := os.ReadFile(filepath.Join(dir, ".side/tmp/core_ignore"))
		require.NoError(t, err)
		require.Equal(t, ".git\n", string(data))
	}
}

func TestWorkspaceSealed(t *testing.T) {
	t.Parallel()
	seal := testSeal(t)
	mustSeal(t, seal, "workspace operations must not bypass admission")
	client := startTestChannelWithSeal(t, seal)
	dir := t.TempDir()
	resp, err := client.Exec(context.Background(), ExecRequest{
		Dir: dir,
		Workspace: &WorkspaceRequest{PrepareSearch: &WorkspaceSearchSetup{
			IgnorePath: ".side/tmp/ignore", IgnoreContent: ".git\n",
		}},
	})
	require.NoError(t, err)
	require.True(t, resp.Sealed)
	_, err = os.Stat(filepath.Join(dir, ".side"))
	require.True(t, os.IsNotExist(err))
}

func TestWorkspaceReadBudget(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	file, err := os.Create(filepath.Join(dir, "large"))
	require.NoError(t, err)
	require.NoError(t, file.Truncate(workspaceReadBudget+1))
	require.NoError(t, file.Close())
	require.NoError(t, os.WriteFile(filepath.Join(dir, "small"), []byte("ok"), 0644))
	client := startTestChannel(t)
	resp, err := client.Exec(context.Background(), ExecRequest{
		Dir: dir, Workspace: &WorkspaceRequest{ReadFiles: []string{"large", "small"}},
	})
	require.NoError(t, err)
	require.Empty(t, resp.Error)
	var result WorkspaceResponse
	require.NoError(t, json.Unmarshal(resp.Stdout, &result))
	require.NotContains(t, result.Files, "large")
	require.Equal(t, []byte("ok"), result.Files["small"])
}

func TestWorkspaceResponseBudget(t *testing.T) {
	t.Parallel()
	client := startTestChannel(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	resp, err := client.Exec(ctx, ExecRequest{Workspace: &WorkspaceRequest{Search: &WorkspaceSearch{
		ListCommand:   "head -c 3500000 /dev/zero | tr '\\000' '\\001'",
		SearchCommand: "true",
		PathGlob:      "*.go",
	}}})
	require.NoError(t, err)
	require.Empty(t, resp.Error)
	var result WorkspaceResponse
	require.NoError(t, json.Unmarshal(resp.Stdout, &result))
	require.True(t, result.Fallback)
	resp, err = client.Exec(ctx, ExecRequest{Argv: []string{"true"}})
	require.NoError(t, err)
	require.Empty(t, resp.Error)
	require.Zero(t, resp.ExitStatus)
}

func TestWorkspaceCancellationDoesNotStartSecondCommand(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	s := &server{
		procs:    make(map[uint64]*os.Process),
		canceled: make(map[uint64]bool),
		done:     make(chan struct{}),
		seal:     testSeal(t),
	}
	defer s.shutdown()
	finished := make(chan ExecResponse, 1)
	go func() {
		finished <- s.execute(ExecRequest{ID: 1, Dir: dir, Workspace: &WorkspaceRequest{Search: &WorkspaceSearch{
			ListCommand:   "printf 'a.go\\n'; touch started; sleep 30",
			SearchCommand: "touch should-not-run",
			PathGlob:      "*.go",
		}}})
	}()
	require.Eventually(t, func() bool {
		_, err := os.Stat(filepath.Join(dir, "started"))
		return err == nil
	}, 5*time.Second, 5*time.Millisecond)
	s.cancel(1)
	select {
	case response := <-finished:
		require.Contains(t, response.Error, "canceled")
	case <-time.After(5 * time.Second):
		t.Fatal("canceled workspace search did not finish")
	}
	require.NoFileExists(t, filepath.Join(dir, "should-not-run"))
}

func TestWorkspaceSearchQuotedFilenames(t *testing.T) {
	t.Parallel()
	client := startTestChannel(t)
	resp, err := client.Exec(context.Background(), ExecRequest{Workspace: &WorkspaceRequest{Search: &WorkspaceSearch{
		ListCommand:   `printf "%s\n" "src/it's.go" 'src/$HOME.go'`,
		SearchCommand: `printf '%s\n'`,
		PathGlob:      "*.go",
	}}})
	require.NoError(t, err)
	require.Empty(t, resp.Error)
	var result WorkspaceResponse
	require.NoError(t, json.Unmarshal(resp.Stdout, &result))
	require.Equal(t, "src/it's.go\nsrc/$HOME.go\n", string(result.Search.Output.Stdout))
}
