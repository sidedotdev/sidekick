package diffanalysis

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestInterdiffFuzzTreePreservesIndex(t *testing.T) {
	t.Parallel()

	repo := newInterdiffFuzzRepo(t)
	sentinel := repo.tree(interdiffFuzzTree{
		"sentinel": {mode: "100644", content: "unchanged index\n"},
	})
	fixture := interdiffFuzzTree{
		"empty":                  {mode: "100644"},
		"plain.txt":              {mode: "100644", content: "hello\n"},
		"dir/executable":         {mode: "100755", content: "#!/bin/sh\nexit 0\n"},
		"dir/quoted \"\t\n\x01é": {mode: "100644", content: "binary\x00content"},
		"symlink":                {mode: "120000", content: "plain.txt"},
		"submodule":              {mode: "160000", content: "submodule commit\n"},
	}

	for _, tree := range []interdiffFuzzTree{fixture, {}, fixture} {
		repo.git("", "read-tree", sentinel)
		got := repo.tree(tree)
		require.Equal(t, sentinel, strings.TrimSpace(repo.git("", "write-tree")),
			"constructing a fixture must not overwrite the application oracle's index")

		entries := strings.Split(strings.TrimSuffix(repo.git("", "ls-tree", "-r", "-z", got), "\x00"), "\x00")
		if len(tree) == 0 {
			require.Equal(t, []string{""}, entries)
			continue
		}
		require.Len(t, entries, len(tree))
		for _, entry := range entries {
			header, path, ok := strings.Cut(entry, "\t")
			require.True(t, ok)
			file, ok := tree[path]
			require.True(t, ok, "unexpected path %q", path)

			kind, content := "blob", file.content
			if file.mode == "160000" {
				kind = "commit"
				emptyTree := strings.TrimSpace(repo.git("", "hash-object", "-t", "tree", "--stdin"))
				content = "tree " + emptyTree + "\nauthor Fuzz <fuzz@example.invalid> 0 +0000\ncommitter Fuzz <fuzz@example.invalid> 0 +0000\n\n" + content
			}
			oid := strings.TrimSpace(repo.git(content, "hash-object", "-t", kind, "--stdin"))
			require.Equal(t, fmt.Sprintf("%s %s %s", file.mode, kind, oid), header)
		}
	}
}
