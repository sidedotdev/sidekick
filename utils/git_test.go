package utils

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGetRepositoryPaths(t *testing.T) {
	// Create a temporary directory for test repository
	tmpDir, err := os.MkdirTemp("", "git-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	// Initialize git repository
	err = os.MkdirAll(tmpDir, 0755)
	require.NoError(t, err)
	runGitCommand(t, tmpDir, "init")

	// Create and change to a subdirectory
	subDir := filepath.Join(tmpDir, "subdir")
	err = os.MkdirAll(subDir, 0755)
	require.NoError(t, err)

	// Test from repository root - should only return one path since current dir and repo root are the same
	paths, err := GetRepositoryPaths(context.Background(), tmpDir)
	require.NoError(t, err)
	require.Len(t, paths, 1) // Current dir and repo root are the same
	// On macOS, /var is symlinked to /private/var, so we check if the actual path
	// is a suffix of the expected path to handle this filesystem quirk
	if !strings.HasSuffix(paths[0], tmpDir) {
		t.Errorf("expected path %q should be a suffix of actual path %q", tmpDir, paths[0])
	}

	// Test from subdirectory
	paths, err = GetRepositoryPaths(context.Background(), subDir)
	require.NoError(t, err)
	require.Len(t, paths, 2)
	// On macOS, /var is symlinked to /private/var, so we check if the actual paths
	// are suffixes of the expected paths to handle this filesystem quirk
	if !strings.HasSuffix(paths[0], subDir) {
		t.Errorf("expected subdir path %q should be a suffix of actual path %q", subDir, paths[0])
	}
	if !strings.HasSuffix(paths[1], tmpDir) {
		t.Errorf("expected root path %q should be a suffix of actual path %q", tmpDir, paths[1])
	}

	// Test error case: non-git directory
	nonGitDir, err := os.MkdirTemp("", "non-git-*")
	require.NoError(t, err)
	defer os.RemoveAll(nonGitDir)

	_, err = GetRepositoryPaths(context.Background(), nonGitDir)
	require.Error(t, err)
}

func runGitCommand(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	err := cmd.Run()
	require.NoError(t, err)
}
