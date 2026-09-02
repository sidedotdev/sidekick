package sideagent

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCleanupStaleAgentBinaries(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeAged := func(name string, age time.Duration) string {
		p := filepath.Join(dir, name)
		require.NoError(t, os.WriteFile(p, []byte(name), 0700))
		mtime := time.Now().Add(-age)
		require.NoError(t, os.Chtimes(p, mtime, mtime))
		return p
	}
	staleBinary := writeAged("side-agent-oldidentity", 48*time.Hour)
	staleTemp := writeAged("side-agent-otheridentity.tmp-abandoned", 48*time.Hour)
	freshBinary := writeAged("side-agent-newidentity", time.Hour)
	self := writeAged("side-agent-currentidentity", 48*time.Hour)
	unrelated := writeAged("keep.txt", 48*time.Hour)

	cleanupStaleAgentBinaries(dir, "side-agent-currentidentity", 24*time.Hour)

	_, err := os.Stat(staleBinary)
	assert.ErrorIs(t, err, os.ErrNotExist, "stale binaries must be removed")
	_, err = os.Stat(staleTemp)
	assert.ErrorIs(t, err, os.ErrNotExist, "abandoned install temps must be removed")
	for name, p := range map[string]string{"fresh": freshBinary, "self": self, "unrelated": unrelated} {
		_, err := os.Stat(p)
		assert.NoError(t, err, name+" must survive GC")
	}
}
