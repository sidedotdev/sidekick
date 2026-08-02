package temporal

import (
	"context"
	"database/sql"
	"net"
	"path/filepath"
	"sidekick/srv/sqlite"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"
)

// freePorts reserves n distinct ephemeral TCP ports by holding all listeners
// open simultaneously before releasing them. The ports are only best-effort
// free once released; that is sufficient for a single boot in a test.
func freePorts(t *testing.T, n int) []int {
	t.Helper()
	ports := make([]int, n)
	listeners := make([]net.Listener, n)
	for i := range ports {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		listeners[i] = l
		ports[i] = l.Addr().(*net.TCPAddr).Port
	}
	for _, l := range listeners {
		l.Close()
	}
	return ports
}

// TestFreshSetup_CreatesTemporalAndStorageDBs boots the embedded Temporal
// server against an empty data home, mimicking the very first `side` startup on
// a new machine that has no existing schema files. It asserts that the on-disk
// Temporal database is created with the current schema and that the sidekick
// storage databases are created alongside it. It intentionally does not create
// any workflows or tasks.
//
// Not parallel: it mutates process-wide env (SIDE_DATA_HOME) and binds real
// ports.
func TestFreshSetup_CreatesTemporalAndStorageDBs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping embedded Temporal server boot in short mode")
	}

	dataHome := t.TempDir()
	t.Setenv("SIDE_DATA_HOME", dataHome)

	// newServerConfig derives service ports as fixed offsets from the base
	// port, which are unlikely to be free next to an ephemeral port, so assign
	// each service its own reserved port instead.
	ports := freePorts(t, 5)
	cfg, err := newServerConfig("127.0.0.1", ports[0])
	require.NoError(t, err)
	cfg.ports.history = ports[1]
	cfg.ports.matching = ports[2]
	cfg.ports.worker = ports[3]
	cfg.ports.metrics = ports[4]

	// A brand-new machine has no Temporal database yet.
	require.NoFileExists(t, cfg.dbFilePath)

	server := startServer(cfg)
	stopped := false
	stop := func() {
		if !stopped {
			stopped = true
			server.Stop()
		}
	}
	t.Cleanup(stop)

	// The embedded SQLite plugin creates the full current schema on a fresh DB.
	require.FileExists(t, cfg.dbFilePath)

	// The sidekick storage databases are created under the same data home on
	// first startup and must be usable. This runs while Temporal is still up,
	// since they are independent database files.
	storage, err := sqlite.NewStorage()
	require.NoError(t, err)
	require.NoError(t, storage.CheckConnection(context.Background()))
	assert.FileExists(t, filepath.Join(dataHome, "sidekick.core.db"))
	assert.FileExists(t, filepath.Join(dataHome, "sidekick.kv.db"))

	// The running server holds an exclusive lock on temporal.db, so stop it
	// before inspecting the on-disk schema.
	stop()

	db, err := sql.Open("sqlite", cfg.dbFilePath+"?_pragma=busy_timeout(5000)")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	for _, table := range []string{
		"executions",
		"current_executions",
		"chasm_node_maps",
		"tasks_v2",
		"current_chasm_executions",
		"executions_visibility",
	} {
		assert.Truef(t, hasTable(db, table), "expected table %q to exist after fresh setup", table)
	}
	assert.True(t, columnNotNull(db, "current_executions", "data_encoding"))
	assert.Equal(t, "0.11", detectCoreVersion(db), "fresh DB should be at the current core schema version")
}
