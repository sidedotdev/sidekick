package persisted_ai

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"sidekick/coding/tree_sitter"
	"sidekick/env"
	"sidekick/srv/sqlite"

	"github.com/kelindar/binary"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupOutlineCacheGitRepo creates a git repo with one committed go file and
// returns its path plus a helper to run further git commands in it.
func setupOutlineCacheGitRepo(t *testing.T) (string, func(args ...string)) {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %v: %s", args, out)
	}
	run("init")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "Test")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc Hello() {}\n"), 0644))
	run("add", ".")
	run("commit", "-m", "init")
	return dir, run
}

func TestOutlineWalkCache_LoadStoreRoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repoDir, runGitCmd := setupOutlineCacheGitRepo(t)
	storage := sqlite.NewTestSqliteStorage(t, "outline-cache-test")
	ra := &RagActivities{DatabaseAccessor: storage}
	ec := env.EnvContainer{Env: &env.LocalEnv{WorkingDirectory: repoDir}}
	workspaceId := "ws-outline-cache"

	// First load: no cached ancestor yet, but caching is enabled.
	state := ra.loadOutlineWalkCache(ctx, workspaceId, ec)
	require.NotNil(t, state.cache)
	require.NotEmpty(t, state.storeKey)
	assert.Empty(t, state.cache.Raw)

	// Populate via a real walk, then persist.
	tsa := tree_sitter.TreeSitterActivities{DatabaseAccessor: storage}
	_, err := tsa.CreateDirSignatureOutlinesCached(ctx, workspaceId, ec, 100000, state.cache)
	require.NoError(t, err)
	ra.storeOutlineWalkCache(ctx, workspaceId, state)

	// Clean reload: cache hit at HEAD with nothing affected, so there is
	// nothing worth re-storing.
	state2 := ra.loadOutlineWalkCache(ctx, workspaceId, ec)
	require.NotNil(t, state2.cache)
	assert.Empty(t, state2.storeKey)
	assert.Contains(t, state2.cache.Raw, "main.go")
	assert.Empty(t, state2.cache.Affected)

	// Commit a change and add an untracked file: both must be affected while
	// the cached ancestor's raw entries remain available.
	require.NoError(t, os.WriteFile(filepath.Join(repoDir, "main.go"), []byte("package main\n\nfunc Hello2() {}\n"), 0644))
	runGitCmd("add", ".")
	runGitCmd("commit", "-m", "change")
	require.NoError(t, os.WriteFile(filepath.Join(repoDir, "untracked.go"), []byte("package main\n"), 0644))

	state3 := ra.loadOutlineWalkCache(ctx, workspaceId, ec)
	require.NotNil(t, state3.cache)
	assert.Empty(t, state3.storeKey, "dirty working tree must never persist a snapshot")
	assert.Contains(t, state3.cache.Raw, "main.go")
	assert.True(t, state3.cache.Affected["main.go"])
	assert.True(t, state3.cache.Affected["untracked.go"])

	// Storing with a dirty working tree is a no-op, so the untracked file
	// never enters a persisted snapshot.
	_, err = tsa.CreateDirSignatureOutlinesCached(ctx, workspaceId, ec, 100000, state3.cache)
	require.NoError(t, err)
	ra.storeOutlineWalkCache(ctx, workspaceId, state3)

	state4 := ra.loadOutlineWalkCache(ctx, workspaceId, ec)
	require.NotNil(t, state4.cache)
	assert.Contains(t, state4.cache.Raw, "main.go")
	assert.NotContains(t, state4.cache.Raw, "untracked.go")
	assert.True(t, state4.cache.Affected["untracked.go"])

	// Once clean at the new HEAD, the snapshot is persisted and an exact
	// reload finds nothing affected.
	require.NoError(t, os.Remove(filepath.Join(repoDir, "untracked.go")))
	state5 := ra.loadOutlineWalkCache(ctx, workspaceId, ec)
	require.NotEmpty(t, state5.storeKey)
	_, err = tsa.CreateDirSignatureOutlinesCached(ctx, workspaceId, ec, 100000, state5.cache)
	require.NoError(t, err)
	ra.storeOutlineWalkCache(ctx, workspaceId, state5)

	state6 := ra.loadOutlineWalkCache(ctx, workspaceId, ec)
	assert.Empty(t, state6.storeKey)
	assert.Contains(t, state6.cache.Raw, "main.go")
	assert.Empty(t, state6.cache.Affected)
}

// TestOutlineWalkCache_DirtyTrackedFileDoesNotPoisonSnapshot guards against a
// dirty working tree overwriting HEAD's snapshot with the dirty tracked
// file's outline removed or replaced by uncommitted content, which would make
// the file vanish from (or go stale in) cached results once the working tree
// is clean again at the same HEAD.
func TestOutlineWalkCache_DirtyTrackedFileDoesNotPoisonSnapshot(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repoDir, _ := setupOutlineCacheGitRepo(t)
	storage := sqlite.NewTestSqliteStorage(t, "outline-cache-dirty")
	ra := &RagActivities{DatabaseAccessor: storage}
	ec := env.EnvContainer{Env: &env.LocalEnv{WorkingDirectory: repoDir}}
	workspaceId := "ws-outline-dirty"

	// Seed a clean snapshot at HEAD.
	tsa := tree_sitter.TreeSitterActivities{DatabaseAccessor: storage}
	state := ra.loadOutlineWalkCache(ctx, workspaceId, ec)
	require.NotEmpty(t, state.storeKey)
	_, err := tsa.CreateDirSignatureOutlinesCached(ctx, workspaceId, ec, 100000, state.cache)
	require.NoError(t, err)
	ra.storeOutlineWalkCache(ctx, workspaceId, state)

	// Dirty a tracked file without committing, then run and store again.
	original := []byte("package main\n\nfunc Hello() {}\n")
	require.NoError(t, os.WriteFile(filepath.Join(repoDir, "main.go"), []byte("package main\n\nfunc Changed() {}\n"), 0644))
	dirtyState := ra.loadOutlineWalkCache(ctx, workspaceId, ec)
	require.NotNil(t, dirtyState.cache)
	assert.True(t, dirtyState.cache.Affected["main.go"])
	_, ok, err := tree_sitter.GetDirectoryRawOutlinesFromCache(ctx, ec, dirtyState.cache)
	require.NoError(t, err)
	require.True(t, ok)
	ra.storeOutlineWalkCache(ctx, workspaceId, dirtyState)

	// Clean again at the same HEAD: the file must still be present, with its
	// committed signature.
	require.NoError(t, os.WriteFile(filepath.Join(repoDir, "main.go"), original, 0644))
	cleanState := ra.loadOutlineWalkCache(ctx, workspaceId, ec)
	require.NotNil(t, cleanState.cache)
	assert.Empty(t, cleanState.cache.Affected)
	require.Contains(t, cleanState.cache.Raw, "main.go")
	entries, ok, err := tree_sitter.GetDirectoryRawOutlinesFromCache(ctx, ec, cleanState.cache)
	require.NoError(t, err)
	require.True(t, ok)
	found := false
	for _, entry := range entries {
		if entry.RelativePath == "main.go" {
			found = true
			assert.Contains(t, entry.Raw, "Hello")
			assert.NotContains(t, entry.Raw, "Changed")
		}
	}
	assert.True(t, found, "tracked file must not vanish from cached outlines")
}

func TestLoadOutlineWalkCache_NonGitDirDisablesCaching(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	storage := sqlite.NewTestSqliteStorage(t, "outline-cache-nongit")
	ra := &RagActivities{DatabaseAccessor: storage}
	ec := env.EnvContainer{Env: &env.LocalEnv{WorkingDirectory: dir}}

	state := ra.loadOutlineWalkCache(context.Background(), "ws", ec)
	assert.Nil(t, state.cache)
	assert.Empty(t, state.storeKey)

	// storing a disabled state is a no-op, not a panic
	ra.storeOutlineWalkCache(context.Background(), "ws", state)
}

func TestParseGitStatusZ(t *testing.T) {
	t.Parallel()
	out := " M a.go\x00?? b/c.txt\x00R  new.go\x00old.go\x00"
	assert.ElementsMatch(t, []string{"a.go", "b/c.txt", "new.go", "old.go"}, parseGitStatusZ(out))
	assert.Empty(t, parseGitStatusZ(""))
}

// TestOutlineWalkCache_CommittedRenameAffectsBothPaths guards against git's
// rename detection collapsing a committed rename into just its destination
// path, which would leave the deleted source path in cached reconstructions.
func TestOutlineWalkCache_CommittedRenameAffectsBothPaths(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repoDir, runGitCmd := setupOutlineCacheGitRepo(t)
	storage := sqlite.NewTestSqliteStorage(t, "outline-cache-rename-test")
	ra := &RagActivities{DatabaseAccessor: storage}
	ec := env.EnvContainer{Env: &env.LocalEnv{WorkingDirectory: repoDir}}
	workspaceId := "ws-outline-rename"

	state := ra.loadOutlineWalkCache(ctx, workspaceId, ec)
	require.NotNil(t, state.cache)
	require.NotEmpty(t, state.storeKey)
	tsa := tree_sitter.TreeSitterActivities{DatabaseAccessor: storage}
	_, err := tsa.CreateDirSignatureOutlinesCached(ctx, workspaceId, ec, 100000, state.cache)
	require.NoError(t, err)
	ra.storeOutlineWalkCache(ctx, workspaceId, state)

	runGitCmd("mv", "main.go", "renamed.go")
	runGitCmd("commit", "-m", "rename")

	state2 := ra.loadOutlineWalkCache(ctx, workspaceId, ec)
	require.NotNil(t, state2.cache)
	require.NotNil(t, state2.cache.Raw, "expected a cache hit on the pre-rename ancestor")
	assert.True(t, state2.cache.Affected["main.go"], "rename source must be affected")
	assert.True(t, state2.cache.Affected["renamed.go"], "rename destination must be affected")

	entries, ok, err := tree_sitter.GetDirectoryRawOutlinesFromCache(ctx, ec, state2.cache)
	require.NoError(t, err)
	require.True(t, ok)
	var paths []string
	for _, entry := range entries {
		paths = append(paths, entry.RelativePath)
	}
	assert.NotContains(t, paths, "main.go", "stale rename source must not survive reconstruction")
	assert.Contains(t, paths, "renamed.go")
}

// TestOutlineWalkCache_VersionedKeyIgnoresOldSnapshots asserts that snapshots
// persisted by a different extraction/encoding version are cache misses:
// without versioning, an unchanged git tree would reuse stale derived
// outlines forever after code/query/parser updates (exact-tree hits never
// re-store, so they would never self-heal).
func TestOutlineWalkCache_VersionedKeyIgnoresOldSnapshots(t *testing.T) {
	t.Parallel()
	require.True(t, strings.HasPrefix(dirOutlineCacheKeyPrefix, "dirOutlineRaw:v"),
		"cache keys must embed the snapshot version")

	ctx := context.Background()
	repoDir, _ := setupOutlineCacheGitRepo(t)
	storage := sqlite.NewTestSqliteStorage(t, "outline-cache-version-test")
	ra := &RagActivities{DatabaseAccessor: storage}
	ec := env.EnvContainer{Env: &env.LocalEnv{WorkingDirectory: repoDir}}
	workspaceId := "ws-outline-version"

	// A snapshot stored under an unversioned (older) key for HEAD's tree
	// must not be found by the versioned lookup.
	state := ra.loadOutlineWalkCache(ctx, workspaceId, ec)
	require.NotEmpty(t, state.storeKey)
	tree := strings.TrimPrefix(state.storeKey, dirOutlineCacheKeyPrefix)
	staleData, err := binary.Marshal(map[string]string{"main.go": "Sfunc Stale()"})
	require.NoError(t, err)
	require.NoError(t, storage.MSetRaw(ctx, workspaceId, map[string][]byte{"dirOutlineRaw:" + tree: staleData}))

	reloaded := ra.loadOutlineWalkCache(ctx, workspaceId, ec)
	assert.Nil(t, reloaded.cache.Raw, "old-version snapshot must be a cache miss")
	assert.NotEmpty(t, reloaded.storeKey, "a miss at HEAD must still allow storing a fresh snapshot")
}

// TestOutlineWalkCache_AncestorEviction asserts that once a newer snapshot is
// stored, the ancestor snapshot it was reconstructed from is deleted so
// per-tree snapshots don't accumulate without bound along a lineage.
func TestOutlineWalkCache_AncestorEviction(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	repoDir, runGitCmd := setupOutlineCacheGitRepo(t)
	storage := sqlite.NewTestSqliteStorage(t, "outline-cache-evict-test")
	ra := &RagActivities{DatabaseAccessor: storage}
	ec := env.EnvContainer{Env: &env.LocalEnv{WorkingDirectory: repoDir}}
	workspaceId := "ws-outline-evict"
	tsa := tree_sitter.TreeSitterActivities{DatabaseAccessor: storage}

	state := ra.loadOutlineWalkCache(ctx, workspaceId, ec)
	require.NotEmpty(t, state.storeKey)
	ancestorKey := state.storeKey
	_, err := tsa.CreateDirSignatureOutlinesCached(ctx, workspaceId, ec, 100000, state.cache)
	require.NoError(t, err)
	ra.storeOutlineWalkCache(ctx, workspaceId, state)

	require.NoError(t, os.WriteFile(filepath.Join(repoDir, "main.go"), []byte("package main\n\nfunc Hello2() {}\n"), 0644))
	runGitCmd("add", ".")
	runGitCmd("commit", "-m", "change")

	state2 := ra.loadOutlineWalkCache(ctx, workspaceId, ec)
	require.NotNil(t, state2.cache.Raw, "expected a hit on the ancestor snapshot")
	require.NotEmpty(t, state2.storeKey)
	require.NotEqual(t, ancestorKey, state2.storeKey)
	_, err = tsa.CreateDirSignatureOutlinesCached(ctx, workspaceId, ec, 100000, state2.cache)
	require.NoError(t, err)
	ra.storeOutlineWalkCache(ctx, workspaceId, state2)

	values, err := storage.MGet(ctx, workspaceId, []string{ancestorKey, state2.storeKey})
	require.NoError(t, err)
	assert.Nil(t, values[0], "superseded ancestor snapshot must be evicted")
	assert.NotNil(t, values[1], "new HEAD snapshot must be stored")
}
