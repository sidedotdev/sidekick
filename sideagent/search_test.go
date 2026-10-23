package sideagent

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"sidekick/repotools"

	"github.com/stretchr/testify/require"
)

func TestSearchRPC(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("rg"); err != nil {
		t.Skip("ripgrep is required")
	}
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "example.go"),
		[]byte("package example\nfunc Target() string { return \"needle\" }\n"), 0600))
	client := startTestChannel(t)
	for _, tc := range []struct {
		name string
		term string
		want string
	}{
		{"match", "needle", "func Target()"},
		{"case fallback", "NEEDLE", "func Target()"},
		{"symbol hint", "absent", "Target"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Decode the wire representation so an unsupported operation
			// fails behaviorally rather than at compile time.
			wire, err := json.Marshal(map[string]any{
				"dir": dir,
				"search": repotools.BulkSearchRepositoryParams{
					Searches: []repotools.SingleSearchParams{{PathGlob: "example.go", SearchTerm: tc.term}},
				},
			})
			require.NoError(t, err)
			var request ExecRequest
			require.NoError(t, json.Unmarshal(wire, &request))
			response, err := client.Exec(context.Background(), request)
			require.NoError(t, err)
			require.Empty(t, response.Error)
			require.Zero(t, response.ExitStatus)
			var result string
			require.NoError(t, json.Unmarshal(response.Stdout, &result))
			require.Contains(t, result, tc.want)
		})
	}
}
