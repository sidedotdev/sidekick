package env

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSwitchObjectPackExcludesHeadHistory(t *testing.T) {
	t.Parallel()
	_, dir, branch := setupSwitchTestRepo(t)
	for i := 0; i < 20; i++ {
		writeTestFile(t, filepath.Join(dir, "tracked.txt"), fmt.Sprintf("revision %d\n", i))
		runGitIn(t, dir, "commit", "-am", fmt.Sprintf("revision %d", i))
	}
	runGitIn(t, dir, "update-ref", "ORIG_HEAD", "HEAD~1")
	writeTestFile(t, filepath.Join(dir, "tracked.txt"), "staged change\n")
	runGitIn(t, dir, "add", "tracked.txt")

	exported, err := ExportForSwitchActivity(context.Background(), ExportForSwitchInput{
		EnvContainer: EnvContainer{Env: &LocalGitWorktreeEnv{WorkingDirectory: dir}},
		Branch:       branch,
	})
	require.NoError(t, err)
	require.NotEmpty(t, exported.Bundle.WIPArchivePath)
	t.Cleanup(func() { _ = os.Remove(exported.Bundle.WIPArchivePath) })

	file, err := os.Open(exported.Bundle.WIPArchivePath)
	require.NoError(t, err)
	defer file.Close()
	zipped, err := gzip.NewReader(file)
	require.NoError(t, err)
	defer zipped.Close()
	archive := tar.NewReader(zipped)
	for {
		header, err := archive.Next()
		require.NoError(t, err, "archive must contain an object pack")
		if header.Name != "git-state/objects.pack" {
			continue
		}
		var packHeader [12]byte
		_, err = io.ReadFull(archive, packHeader[:])
		require.NoError(t, err)
		require.Equal(t, "PACK", string(packHeader[:4]))
		count := binary.BigEndian.Uint32(packHeader[8:])
		assert.GreaterOrEqual(t, count, uint32(1), "the staged blob must be transferred")
		assert.LessOrEqual(t, count, uint32(2), "a one-file edit must not transfer the existing tree or history")
		break
	}
}
