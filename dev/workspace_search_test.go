package dev

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"

	"sidekick/env"
	"sidekick/sideagent"

	"github.com/stretchr/testify/require"
)

type workspaceSearchTestEnv struct {
	env.Env
	client        *sideagent.Client
	prepares      atomic.Int32
	searches      atomic.Int32
	forceFallback bool
}

func (e *workspaceSearchTestEnv) ExecuteWorkspace(ctx context.Context, request sideagent.WorkspaceRequest) (sideagent.WorkspaceResponse, error) {
	if request.PrepareSearch != nil {
		e.prepares.Add(1)
	}
	if request.Search != nil {
		e.searches.Add(1)
		if e.forceFallback {
			return sideagent.WorkspaceResponse{Fallback: true}, nil
		}
	}
	response, err := e.client.Exec(ctx, sideagent.ExecRequest{Dir: e.GetWorkingDirectory(), Workspace: &request})
	if err != nil {
		return sideagent.WorkspaceResponse{}, err
	}
	if response.Error != "" {
		return sideagent.WorkspaceResponse{}, fmt.Errorf("%s", response.Error)
	}
	var result sideagent.WorkspaceResponse
	err = json.Unmarshal(response.Stdout, &result)
	return result, err
}

func newWorkspaceSearchTestEnv(t *testing.T) *workspaceSearchTestEnv {
	t.Helper()
	if _, err := exec.LookPath("rg"); err != nil {
		t.Skip("ripgrep is required")
	}
	dir := t.TempDir()
	for path, content := range map[string]string{
		"a.go":              "package fixture\nfunc Alpha() string { return \"Needle literal[abc\" }\n",
		"src/a b.go":        "package fixture\nfunc Beta() string { return \"needle\" }\n",
		"ignored/hidden.go": "package fixture\nfunc Hidden() string { return \"needle\" }\n",
		"other.txt":         "unrelated\n",
		".gitignore":        "ignored/\n",
		".sideignore":       "other.txt\n",
	} {
		require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(dir, path)), 0755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, path), []byte(content), 0644))
	}
	output, err := exec.Command("git", "-C", dir, "init").CombinedOutput()
	require.NoError(t, err, "%s", output)
	local, err := env.NewLocalEnv(context.Background(), env.LocalEnvParams{RepoDir: dir})
	require.NoError(t, err)
	clientConn, serverConn := net.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer serverConn.Close()
		_ = sideagent.Serve(serverConn, serverConn)
	}()
	client := sideagent.NewClient(clientConn, clientConn)
	t.Cleanup(func() {
		client.Close()
		clientConn.Close()
		<-done
	})
	return &workspaceSearchTestEnv{Env: local, client: client}
}

func TestWorkspaceBulkSearchParity(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		searches []SingleSearchParams
	}{
		{"ordered batch", []SingleSearchParams{{PathGlob: "*.go", SearchTerm: "Alpha"}, {PathGlob: "**/*.go", SearchTerm: "Beta"}}},
		{"case retry", []SingleSearchParams{{PathGlob: "*.go", SearchTerm: "NEEDLE"}}},
		{"literal retry", []SingleSearchParams{{PathGlob: "*.go", SearchTerm: "literal[abc"}}},
		{"explicit ignored", []SingleSearchParams{{PathGlob: "ignored/*.go", SearchTerm: "needle"}}},
		{"global hint", []SingleSearchParams{{PathGlob: "*.txt", SearchTerm: "Alpha"}}},
		{"missing glob", []SingleSearchParams{{PathGlob: "absent/*.go", SearchTerm: "Alpha"}}},
		{"symbol hint", []SingleSearchParams{{PathGlob: "a.go", SearchTerm: "no-such-symbol"}}},
		{"empty term", []SingleSearchParams{{PathGlob: "a.go", SearchTerm: ""}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			e := newWorkspaceSearchTestEnv(t)
			ctx := context.Background()
			params := BulkSearchRepositoryParams{ContextLines: 2, Searches: tc.searches}
			expected, err := bulkSearchRepository(activitySearchRunner{ctx: ctx}, env.EnvContainer{Env: e.Env}, params)
			require.NoError(t, err)
			actual, err := BulkSearchRepositoryActivity(ctx, BulkSearchRepositoryActivityInput{
				EnvContainer: env.EnvContainer{Env: e}, Params: params,
			})
			require.NoError(t, err)
			require.Equal(t, expected, actual.Result)
			require.EqualValues(t, 1, e.prepares.Load())
			require.Positive(t, e.searches.Load())
		})
	}
}

func TestWorkspaceSearchRefreshesIgnoreFiles(t *testing.T) {
	t.Parallel()
	e := newWorkspaceSearchTestEnv(t)
	ctx := context.Background()
	input := BulkSearchRepositoryActivityInput{
		EnvContainer: env.EnvContainer{Env: e},
		Params: BulkSearchRepositoryParams{Searches: []SingleSearchParams{
			{PathGlob: "*.go", SearchTerm: "Alpha"},
		}},
	}
	first, err := BulkSearchRepositoryActivity(ctx, input)
	require.NoError(t, err)
	require.Contains(t, first.Result, "func Alpha")
	require.NoError(t, e.WriteFile(ctx, ".sideignore", []byte("a.go\n"), 0644))
	expected, err := bulkSearchRepository(activitySearchRunner{ctx: ctx}, env.EnvContainer{Env: e.Env}, input.Params)
	require.NoError(t, err)
	second, err := BulkSearchRepositoryActivity(ctx, input)
	require.NoError(t, err)
	require.Equal(t, expected, second.Result)
	require.EqualValues(t, 2, e.prepares.Load())
}

func TestWorkspaceSearchFallback(t *testing.T) {
	t.Parallel()
	e := newWorkspaceSearchTestEnv(t)
	e.forceFallback = true
	ctx := context.Background()
	input := BulkSearchRepositoryActivityInput{
		EnvContainer: env.EnvContainer{Env: e},
		Params: BulkSearchRepositoryParams{Searches: []SingleSearchParams{
			{PathGlob: "*.go", SearchTerm: "Alpha"},
		}},
	}
	expected, err := bulkSearchRepository(activitySearchRunner{ctx: ctx}, env.EnvContainer{Env: e.Env}, input.Params)
	require.NoError(t, err)
	actual, err := BulkSearchRepositoryActivity(ctx, input)
	require.NoError(t, err)
	require.Equal(t, expected, actual.Result)
	require.EqualValues(t, 1, e.searches.Load())
}
