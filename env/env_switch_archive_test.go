package env

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSwitchArchiveExcludesMacOSMetadata(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS extended attributes and BSD tar required")
	}
	t.Setenv("COPYFILE_DISABLE", "")
	require.NoError(t, os.Unsetenv("COPYFILE_DISABLE"))

	_, dir, _ := setupSwitchTestRepo(t)
	file := filepath.Join(dir, "tracked.txt")
	writeTestFile(t, file, "uncommitted")
	out, err := exec.Command("xattr", "-w", "com.sidekick.test", "metadata", file).CombinedOutput()
	require.NoError(t, err, "%s", out)
	out, err = exec.Command("xattr", "-p", "com.sidekick.test", file).CombinedOutput()
	require.NoError(t, err, "%s", out)
	require.Equal(t, "metadata\n", string(out))

	archive, err := captureWIPToLocalFile(context.Background(), &LocalGitWorktreeEnv{WorkingDirectory: dir})
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.Remove(archive) })
	data, err := os.ReadFile(archive)
	require.NoError(t, err)
	gz, err := gzip.NewReader(bytes.NewReader(data))
	require.NoError(t, err)
	defer gz.Close()
	outer := tar.NewReader(gz)
	var changed []byte
	for {
		header, err := outer.Next()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		if header.Name == "changed.tgz" {
			changed, err = io.ReadAll(outer)
			require.NoError(t, err)
		}
	}
	require.NotEmpty(t, changed)
	innerGzip, err := gzip.NewReader(bytes.NewReader(changed))
	require.NoError(t, err)
	defer innerGzip.Close()
	inner := tar.NewReader(innerGzip)
	var names []string
	for {
		header, err := inner.Next()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		names = append(names, header.Name)
		if header.Name == "tracked.txt" {
			content, err := io.ReadAll(inner)
			require.NoError(t, err)
			assert.Equal(t, "uncommitted", string(content))
		}
	}
	assert.Equal(t, []string{"tracked.txt"}, names)
}
