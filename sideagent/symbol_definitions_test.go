package sideagent

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"sidekick/repotools"

	"github.com/stretchr/testify/require"
)

func TestSymbolDefinitionsRPC(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skip("gopls is required")
	}
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":   "module example.com/fixture\n\ngo 1.23\n",
		"model.go": "package fixture\n\n// Target is defined here.\nfunc Target() int { return 1 }\n",
		"use.go":   "package fixture\n\nfunc Caller() int { return Target() }\n",
	}
	for name, content := range files {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0600))
	}
	client := startTestChannel(t)

	// Both cases need the sandbox-local language server: go-to-definition
	// from a file that only references the symbol, and find-references for
	// the related-symbols hint.
	for _, tc := range []struct {
		name    string
		request repotools.FileSymDefRequest
		related bool
		want    []string
	}{
		{
			name: "definition resolved from a referencing file",
			request: repotools.FileSymDefRequest{
				FilePath: "use.go",
				Symbols:  []repotools.RequestedSymbol{{Name: "Target", ReferenceLine: "return Target()"}},
			},
			want: []string{"File: model.go", "func Target() int"},
		},
		{
			name: "related symbols from references",
			request: repotools.FileSymDefRequest{
				FilePath: "model.go",
				Symbols:  []repotools.RequestedSymbol{{Name: "Target"}},
			},
			related: true,
			want:    []string{"func Target() int", "Target is referenced in other files", "func Caller() int"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			results, err := client.BulkGetSymbolDefinitions(context.Background(), dir, repotools.DirectorySymDefRequest{
				Requests:              []repotools.FileSymDefRequest{tc.request},
				IncludeRelatedSymbols: tc.related,
			})
			require.NoError(t, err)
			require.Empty(t, results.Failures)
			for _, want := range tc.want {
				require.Contains(t, results.SymbolDefinitions, want)
			}
		})
	}
}
