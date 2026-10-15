package testfixture

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDirectGitPath(t *testing.T) {
	for _, mode := range []string{"explicit executable", "explicit symlink", "missing git", "macOS launcher"} {
		t.Run(mode, func(t *testing.T) {
			if mode == "macOS launcher" && runtime.GOOS != "darwin" {
				t.Skip("macOS developer-tools launcher")
			}

			binDir := t.TempDir()
			gitPath := filepath.Join(binDir, "git")
			switch mode {
			case "explicit executable":
				require.NoError(t, os.WriteFile(gitPath, []byte("#!/bin/sh\nexit 87\n"), 0755))
			case "explicit symlink":
				target := filepath.Join(t.TempDir(), "custom-git")
				require.NoError(t, os.WriteFile(target, []byte("#!/bin/sh\nexit 87\n"), 0755))
				require.NoError(t, os.Symlink(target, gitPath))
			case "macOS launcher":
				require.NoError(t, os.Symlink("/usr/bin/git", gitPath))
			}
			t.Setenv("PATH", binDir)
			destination := t.TempDir()
			path, err := DirectGitPath(destination)
			require.NoError(t, err)

			if mode != "macOS launcher" {
				require.Equal(t, binDir, path)
				entries, err := os.ReadDir(destination)
				require.NoError(t, err)
				require.Empty(t, entries)
				return
			}

			require.Equal(t, []string{destination, binDir}, filepath.SplitList(path))
			directGit := filepath.Join(destination, "git")
			resolved, err := filepath.EvalSymlinks(directGit)
			require.NoError(t, err)
			require.NotEqual(t, "/usr/bin/git", resolved)
			output, err := exec.Command(directGit, "--version").CombinedOutput()
			require.NoError(t, err, "%s", output)
			require.True(t, strings.HasPrefix(string(output), "git version "))
		})
	}
}
