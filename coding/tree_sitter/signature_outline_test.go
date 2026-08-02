package tree_sitter

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"sidekick/env"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOutlinesFromRawEntries_TruncationWhitespaceOnly(t *testing.T) {
	t.Parallel()

	// Markdown signature output ends with "---\n" which means trailing newline
	content := `# Heading One

Some content.

## Heading Two

More content.
`
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "test.md")
	err := os.WriteFile(filePath, []byte(content), 0644)
	require.NoError(t, err)

	// First, get the actual signature output to know its length
	sigContent, err := GetFileSignaturesStringFromBytes("markdown", []byte(content))
	require.NoError(t, err)

	// The signature should end with "---\n", verify it has trailing whitespace
	require.True(t, strings.HasSuffix(sigContent, "\n"), "signature should end with newline")

	// Set maxContentLength to truncate only the trailing newline
	maxLen := len(sigContent) - 1
	signaturePaths := map[string]int{"test.md": maxLen}

	ec := env.EnvContainer{Env: &env.LocalEnv{WorkingDirectory: tmpDir}}
	entries, err := GetDirectoryRawOutlines(context.Background(), ec, nil)
	require.NoError(t, err)
	outlines := OutlinesFromRawEntries(entries, nil, &signaturePaths)

	// Find the file outline
	var fileOutline *FileOutline
	for i := range outlines {
		if outlines[i].Path == "test.md" {
			fileOutline = &outlines[i]
			break
		}
	}
	require.NotNil(t, fileOutline, "should find test.md outline")

	// The truncated part was only whitespace, so no truncation message should appear
	assert.NotContains(t, fileOutline.Content, "truncated",
		"should not show truncation message when only whitespace is truncated")
}

func TestOutlinesFromRawEntries_TruncationWithContent(t *testing.T) {
	t.Parallel()

	content := `# Heading One

Some content.

## Heading Two

More content.

## Heading Three

Even more content.
`
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "test.md")
	err := os.WriteFile(filePath, []byte(content), 0644)
	require.NoError(t, err)

	// Get the actual signature output
	sigContent, err := GetFileSignaturesStringFromBytes("markdown", []byte(content))
	require.NoError(t, err)

	// Set maxContentLength to truncate actual content (not just whitespace)
	maxLen := len(sigContent) - 20
	signaturePaths := map[string]int{"test.md": maxLen}

	ec := env.EnvContainer{Env: &env.LocalEnv{WorkingDirectory: tmpDir}}
	entries, err := GetDirectoryRawOutlines(context.Background(), ec, nil)
	require.NoError(t, err)
	outlines := OutlinesFromRawEntries(entries, nil, &signaturePaths)

	// Find the file outline
	var fileOutline *FileOutline
	for i := range outlines {
		if outlines[i].Path == "test.md" {
			fileOutline = &outlines[i]
			break
		}
	}
	require.NotNil(t, fileOutline, "should find test.md outline")

	// Real content was truncated, so truncation message should appear
	assert.Contains(t, fileOutline.Content, "truncated",
		"should show truncation message when actual content is truncated")
}

func TestGetDirectoryRawOutlines_WalkCache(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "a.go"), []byte("package a\n\nfunc A() {}\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "b.go"), []byte("package b\n\nfunc B() {}\n"), 0644))
	ec := env.EnvContainer{Env: &env.LocalEnv{WorkingDirectory: tmpDir}}

	t.Run("records raw outlines during walk", func(t *testing.T) {
		t.Parallel()
		cache := &OutlineWalkCache{}
		entries, err := GetDirectoryRawOutlines(context.Background(), ec, cache)
		require.NoError(t, err)
		outlines := OutlinesFromRawEntries(entries, nil, nil)
		require.NotEmpty(t, outlines)
		updated := cache.UpdatedSnapshot()
		require.Contains(t, updated, "a.go")
		require.Contains(t, updated, "b.go")
		assert.True(t, strings.HasPrefix(updated["a.go"], rawOutlineSignaturePrefix))
		assert.Contains(t, updated["a.go"], "func A()")
	})

	t.Run("unaffected paths are served from cache without reparsing", func(t *testing.T) {
		t.Parallel()
		cache := &OutlineWalkCache{
			Raw: map[string]string{
				"a.go": rawOutlineSignaturePrefix + "func CachedFake()",
				"b.go": rawOutlineSignaturePrefix + "func StaleB()",
			},
			Affected: map[string]bool{"b.go": true},
		}
		entries, err := GetDirectoryRawOutlines(context.Background(), ec, cache)
		require.NoError(t, err)
		outlines := OutlinesFromRawEntries(entries, nil, nil)
		byPath := map[string]FileOutline{}
		for _, outline := range outlines {
			byPath[outline.Path] = outline
		}
		// unaffected path: cached raw content used verbatim, no read/parse
		assert.Equal(t, "func CachedFake()", byPath["a.go"].Content)
		assert.Equal(t, OutlineTypeFileSignature, byPath["a.go"].OutlineType)
		// affected path: cache bypassed, recomputed from actual content
		assert.Contains(t, byPath["b.go"].Content, "func B()")
		// the walk re-records both so the updated map reflects current state
		updated := cache.UpdatedSnapshot()
		assert.Equal(t, rawOutlineSignaturePrefix+"func CachedFake()", updated["a.go"])
		assert.Contains(t, updated["b.go"], "func B()")
	})

	t.Run("cached unhandled sentinel skips files without outlines", func(t *testing.T) {
		t.Parallel()
		cache := &OutlineWalkCache{Raw: map[string]string{"a.go": rawOutlineUnhandled}}
		entries, err := GetDirectoryRawOutlines(context.Background(), ec, cache)
		require.NoError(t, err)
		outlines := OutlinesFromRawEntries(entries, nil, nil)
		var aOutline *FileOutline
		for i := range outlines {
			if outlines[i].Path == "a.go" {
				aOutline = &outlines[i]
			}
		}
		require.NotNil(t, aOutline)
		assert.Equal(t, OutlineTypeFileUnhandled, aOutline.OutlineType)
		assert.Equal(t, rawOutlineUnhandled, cache.UpdatedSnapshot()["a.go"])
	})
}

func TestGetDirectoryRawOutlinesFromCache(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "pkg"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "pkg", "a.go"), []byte("package pkg\n\nfunc A() {}\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "new.go"), []byte("package main\n\nfunc New() {}\n"), 0644))
	ec := env.EnvContainer{Env: &env.LocalEnv{WorkingDirectory: tmpDir}}

	t.Run("no snapshot means fallback required", func(t *testing.T) {
		t.Parallel()
		entries, ok, err := GetDirectoryRawOutlinesFromCache(context.Background(), ec, &OutlineWalkCache{})
		require.NoError(t, err)
		assert.False(t, ok)
		assert.Nil(t, entries)
	})

	t.Run("empty snapshot is a miss", func(t *testing.T) {
		t.Parallel()
		cache := &OutlineWalkCache{
			Raw:      map[string]string{},
			Affected: map[string]bool{"new.go": true},
		}
		entries, ok, err := GetDirectoryRawOutlinesFromCache(context.Background(), ec, cache)
		require.NoError(t, err)
		assert.False(t, ok)
		assert.Nil(t, entries)
	})

	t.Run("non-not-found read failure propagates", func(t *testing.T) {
		t.Parallel()
		cache := &OutlineWalkCache{
			Raw: map[string]string{
				"pkg/a.go": rawOutlineSignaturePrefix + "func CachedA()",
			},
			// reading a path whose parent is a regular file fails with
			// ENOTDIR, a non-not-found error
			Affected: map[string]bool{"new.go/child.go": true},
		}
		_, ok, err := GetDirectoryRawOutlinesFromCache(context.Background(), ec, cache)
		require.Error(t, err)
		assert.False(t, ok)
	})

	t.Run("snapshot reconstruction without walking", func(t *testing.T) {
		t.Parallel()
		cache := &OutlineWalkCache{
			Raw: map[string]string{
				// unaffected: served from cache even though content differs on disk
				"pkg/a.go": rawOutlineSignaturePrefix + "func CachedA()",
				// affected and deleted from disk: must drop out
				"gone.go": rawOutlineSignaturePrefix + "func Gone()",
			},
			Affected: map[string]bool{
				"gone.go": true,
				// affected and new since the snapshot: must be parsed from disk
				"new.go": true,
			},
		}
		entries, ok, err := GetDirectoryRawOutlinesFromCache(context.Background(), ec, cache)
		require.NoError(t, err)
		require.True(t, ok)

		byPath := map[string]RawOutlineEntry{}
		for _, entry := range entries {
			byPath[entry.RelativePath] = entry
		}
		require.NotContains(t, byPath, "gone.go")
		assert.Equal(t, "func CachedA()", byPath["pkg/a.go"].Raw)
		assert.True(t, byPath["pkg/a.go"].Handled)
		assert.Contains(t, byPath["new.go"].Raw, "func New()")
		// dir entry derived from file paths, emitted before its first file
		require.Contains(t, byPath, "pkg")
		assert.True(t, byPath["pkg"].IsDir)
		var order []string
		for _, entry := range entries {
			order = append(order, entry.RelativePath)
		}
		assert.Equal(t, []string{"new.go", "pkg", "pkg/a.go"}, order)

		// everything included is re-recorded for persistence; deletions are not
		updated := cache.UpdatedSnapshot()
		assert.NotContains(t, updated, "gone.go")
		assert.Contains(t, updated, "pkg/a.go")
		assert.Contains(t, updated, "new.go")
	})
}

// TestGetDirectoryRawOutlines_ColdVsCacheHit_EmptyDir asserts that empty
// directories are consistently excluded on both cold walks and cache hits:
// git cannot represent them, so cached reconstruction could never track
// their creation or removal.
func TestGetDirectoryRawOutlines_ColdVsCacheHit_EmptyDir(t *testing.T) {
	t.Parallel()

	tmpDir, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "main.go"), []byte("package main\n\nfunc Hello() {}\n"), 0644))
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "emptydir"), 0755))
	ec := env.EnvContainer{Env: &env.LocalEnv{WorkingDirectory: tmpDir}}

	coldCache := &OutlineWalkCache{}
	cold, err := GetDirectoryRawOutlines(context.Background(), ec, coldCache)
	require.NoError(t, err)
	for _, entry := range cold {
		assert.NotEqual(t, "emptydir", entry.RelativePath, "empty directories must be excluded from cold walks")
	}

	hitCache := &OutlineWalkCache{Raw: coldCache.UpdatedSnapshot(), Affected: map[string]bool{}}
	cached, ok, err := GetDirectoryRawOutlinesFromCache(context.Background(), ec, hitCache)
	require.NoError(t, err)
	require.True(t, ok)
	assert.ElementsMatch(t, cold, cached)
}

// TestGetDirectoryRawOutlines_ColdVsCacheHit_DirAddRemove asserts cache
// reconstruction matches a fresh cold walk after one directory (with its
// last file) is deleted and another is created: the stale directory must not
// be retained or re-persisted, and the new one must appear.
func TestGetDirectoryRawOutlines_ColdVsCacheHit_DirAddRemove(t *testing.T) {
	t.Parallel()

	tmpDir, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "main.go"), []byte("package main\n\nfunc Hello() {}\n"), 0644))
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "oldpkg"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "oldpkg", "a.go"), []byte("package oldpkg\n\nfunc A() {}\n"), 0644))
	ec := env.EnvContainer{Env: &env.LocalEnv{WorkingDirectory: tmpDir}}

	coldCache := &OutlineWalkCache{}
	_, err = GetDirectoryRawOutlines(context.Background(), ec, coldCache)
	require.NoError(t, err)

	require.NoError(t, os.RemoveAll(filepath.Join(tmpDir, "oldpkg")))
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "newpkg"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "newpkg", "b.go"), []byte("package newpkg\n\nfunc B() {}\n"), 0644))

	hitCache := &OutlineWalkCache{
		Raw: coldCache.UpdatedSnapshot(),
		// git reports only file paths as changed, never directories
		Affected: map[string]bool{"oldpkg/a.go": true, "newpkg/b.go": true},
	}
	cached, ok, err := GetDirectoryRawOutlinesFromCache(context.Background(), ec, hitCache)
	require.NoError(t, err)
	require.True(t, ok)

	fresh, err := GetDirectoryRawOutlines(context.Background(), ec, &OutlineWalkCache{})
	require.NoError(t, err)
	assert.ElementsMatch(t, fresh, cached)

	byPath := map[string]RawOutlineEntry{}
	for _, entry := range cached {
		byPath[entry.RelativePath] = entry
	}
	assert.NotContains(t, byPath, "oldpkg")
	require.Contains(t, byPath, "newpkg")
	assert.True(t, byPath["newpkg"].IsDir)

	updated := hitCache.UpdatedSnapshot()
	assert.NotContains(t, updated, "oldpkg")
	assert.NotContains(t, updated, "oldpkg/a.go")
	assert.Contains(t, updated, "newpkg/b.go")
}

// TestGetDirectoryRawOutlines_ColdVsCacheHit_Ordering asserts cache
// reconstruction reproduces the cold walk's exact entry order, including the
// case where a directory's subtree precedes a sibling file whose name sorts
// after the directory name ("foo" walks before "foo.go", so "foo/a.go" must
// precede "foo.go" despite plain lexical path order saying otherwise).
func TestGetDirectoryRawOutlines_ColdVsCacheHit_Ordering(t *testing.T) {
	t.Parallel()

	tmpDir, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "foo.go"), []byte("package foo\n\nfunc F() {}\n"), 0644))
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "foo"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "foo", "a.go"), []byte("package foo\n\nfunc A() {}\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "main.go"), []byte("package main\n\nfunc M() {}\n"), 0644))
	ec := env.EnvContainer{Env: &env.LocalEnv{WorkingDirectory: tmpDir}}

	coldCache := &OutlineWalkCache{}
	cold, err := GetDirectoryRawOutlines(context.Background(), ec, coldCache)
	require.NoError(t, err)
	for i := 1; i < len(cold); i++ {
		assert.True(t, walkOrderLess(cold[i-1].RelativePath, cold[i].RelativePath),
			"cold walk entries must be in canonical walk order: %q before %q",
			cold[i-1].RelativePath, cold[i].RelativePath)
	}

	hitCache := &OutlineWalkCache{Raw: coldCache.UpdatedSnapshot(), Affected: map[string]bool{}}
	cached, ok, err := GetDirectoryRawOutlinesFromCache(context.Background(), ec, hitCache)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, cold, cached, "cache reconstruction must preserve cold walk ordering")
}

// TestGetDirectoryRawOutlines_ReadFailureNotPersisted asserts that a walk
// that failed to read some file never yields a persistable snapshot: the
// recorded map understates the tree, and persisting it would make the file
// vanish from all future exact-tree cache hits.
func TestGetDirectoryRawOutlines_ReadFailureNotPersisted(t *testing.T) {
	t.Parallel()
	if os.Geteuid() == 0 {
		t.Skip("permission-based read failures do not apply to root")
	}

	tmpDir, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "main.go"), []byte("package main\n\nfunc Hello() {}\n"), 0644))
	unreadable := filepath.Join(tmpDir, "secret.go")
	require.NoError(t, os.WriteFile(unreadable, []byte("package main\n"), 0644))
	require.NoError(t, os.Chmod(unreadable, 0o000))
	t.Cleanup(func() { _ = os.Chmod(unreadable, 0o644) })
	ec := env.EnvContainer{Env: &env.LocalEnv{WorkingDirectory: tmpDir}}

	cache := &OutlineWalkCache{}
	_, err = GetDirectoryRawOutlines(context.Background(), ec, cache)
	require.NoError(t, err)
	assert.Empty(t, cache.UpdatedSnapshot(), "incomplete walks must not produce a snapshot")
}

// TestOutlineSnapshotVersion asserts the snapshot version fingerprint is a
// stable non-empty value derived from the embedded signature queries, so
// persisted outline snapshots are invalidated exactly when extraction inputs
// change.
func TestOutlineSnapshotVersion(t *testing.T) {
	t.Parallel()
	require.NotEmpty(t, OutlineSnapshotVersion)
	assert.Equal(t, OutlineSnapshotVersion, computeOutlineSnapshotVersion(), "fingerprint must be deterministic")

	entries, err := signatureQueriesFS.ReadDir("signature_queries")
	require.NoError(t, err)
	require.NotEmpty(t, entries, "fingerprint must cover the embedded signature queries")
}

// TestHashFSFiles_OrderIndependent asserts the query digest depends only on
// file paths and contents, never on filesystem traversal order, and changes
// when content changes.
func TestHashFSFiles_OrderIndependent(t *testing.T) {
	t.Parallel()
	digest := func(fsys fs.FS) string {
		h := sha256.New()
		hashFSFiles(h, fsys)
		return hex.EncodeToString(h.Sum(nil))
	}

	a := fstest.MapFS{
		"queries/b.scm": &fstest.MapFile{Data: []byte("bee")},
		"queries/a.scm": &fstest.MapFile{Data: []byte("ay")},
	}
	b := fstest.MapFS{
		"queries/a.scm": &fstest.MapFile{Data: []byte("ay")},
		"queries/b.scm": &fstest.MapFile{Data: []byte("bee")},
	}
	assert.Equal(t, digest(a), digest(b), "same files must digest identically regardless of map construction order")

	c := fstest.MapFS{
		"queries/a.scm": &fstest.MapFile{Data: []byte("ay")},
		"queries/b.scm": &fstest.MapFile{Data: []byte("changed")},
	}
	assert.NotEqual(t, digest(a), digest(c), "changed content must change the digest")
}
