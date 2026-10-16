package coding

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"sidekick/coding/tree_sitter"
	"sidekick/env"

	"github.com/stretchr/testify/require"
)

type prefetchTestEnv struct {
	env.Env
	batches atomic.Int32
	reads   atomic.Int32
	fail    bool
	partial bool
}

func (e *prefetchTestEnv) ReadFiles(ctx context.Context, paths []string) (map[string][]byte, error) {
	e.batches.Add(1)
	if e.fail {
		return nil, fmt.Errorf("prefetch unavailable")
	}
	result := make(map[string][]byte)
	for i, path := range paths {
		if e.partial && i > 0 {
			break
		}
		data, err := e.Env.ReadFile(ctx, path)
		if err == nil {
			result[path] = data
		}
	}
	return result, nil
}

func (e *prefetchTestEnv) ReadFile(ctx context.Context, path string) ([]byte, error) {
	e.reads.Add(1)
	return e.Env.ReadFile(ctx, path)
}

func TestBulkGetSymbolDefinitionsPrefetch(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		fail    bool
		partial bool
		reads   int32
	}{
		{name: "complete"},
		{name: "partial", partial: true, reads: 1},
		{name: "unavailable", fail: true, reads: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			for _, name := range []string{"a", "b"} {
				require.NoError(t, os.WriteFile(filepath.Join(dir, name+".go"), []byte("package fixture\nfunc "+name+"() {}\n"), 0644))
			}
			local, err := env.NewLocalEnv(context.Background(), env.LocalEnvParams{RepoDir: dir})
			require.NoError(t, err)
			e := &prefetchTestEnv{Env: local, fail: tc.fail, partial: tc.partial}
			ca := &CodingActivities{TreeSitterActivities: &tree_sitter.TreeSitterActivities{}}
			result, err := ca.BulkGetSymbolDefinitions(context.Background(), DirectorySymDefRequest{
				EnvContainer: env.EnvContainer{Env: e},
				Requests: []FileSymDefRequest{
					{FilePath: "a.go", Symbols: []RequestedSymbol{{Name: "a"}}},
					{FilePath: "b.go", Symbols: []RequestedSymbol{{Name: "b"}}},
				},
			})
			require.NoError(t, err)
			require.Empty(t, result.Failures)
			require.Contains(t, result.SymbolDefinitions, "func a()")
			require.Contains(t, result.SymbolDefinitions, "func b()")
			require.EqualValues(t, 1, e.batches.Load())
			require.Equal(t, tc.reads, e.reads.Load())
		})
	}
}
