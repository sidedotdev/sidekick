package dev

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDevTestsBypassDarwinDeveloperToolsLauncher(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "darwin" {
		t.Skip("macOS developer-tools launcher")
	}

	gitPath, err := exec.LookPath("git")
	require.NoError(t, err)
	gitPath, err = filepath.EvalSymlinks(gitPath)
	require.NoError(t, err)
	require.NotEqual(t, "/usr/bin/git", gitPath)
}
