package repotools

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"sidekick/env/localio"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testRepository(dir string) LocalRepository {
	return LocalRepository{
		FileSystem:      localio.FileSystem{WorkingDirectory: dir},
		IgnoreFileNames: IgnoreFileNames,
	}
}

func TestGetHintForNonExistentFile(t *testing.T) {
	t.Parallel()
	// Define test cases
	tests := []struct {
		name                    string
		nonExistentRelativePath string
		setupFiles              []string
		expectedHint            string
	}{
		{
			name:                    "No other files",
			nonExistentRelativePath: "nonexistent.txt",
			setupFiles:              []string{},
			expectedHint:            "No file at 'nonexistent.txt' exists in the repository. Please check the file path and try again.",
		},
		{
			name:                    "No other files + nested directory",
			nonExistentRelativePath: filepath.Join("nested", "nonexistent.txt"),
			setupFiles:              []string{},
			expectedHint:            "No file at 'nested/nonexistent.txt' exists in the repository. Please check the file path and try again.",
		},
		{
			name:                    "Too many similar files",
			nonExistentRelativePath: filepath.Join("similar", "nonexistent.txt"),
			setupFiles: []string{
				"similar/file1.txt",
				"similar/file2.txt",
				"similar/file3.txt",
				"similar/file4.txt",
			},
			expectedHint: "No file at 'similar/nonexistent.txt' exists in the repository. Did you mean one of the following?:\n" +
				"similar/file1.txt\nsimilar/file2.txt\nsimilar/file3.txt",
		},
		{
			name:                    "Some similar files, some dissimilar",
			nonExistentRelativePath: filepath.Join("similar", "nonexistent.txt"),
			setupFiles: []string{
				"similar/file1.txt",
				"similar/file2.txt",
				"dissimilar/file3.txt",
				"dissimilar/file4.txt",
			},
			expectedHint: "No file at 'similar/nonexistent.txt' exists in the repository. Did you mean one of the following?:\n" +
				"similar/file1.txt\nsimilar/file2.txt",
		},
		{
			name:                    "wrong directory for file",
			nonExistentRelativePath: filepath.Join("wrong", "file1.txt"),
			setupFiles: []string{
				"right/file1.txt",
				"right/file2.txt",
			},
			expectedHint: "No file at 'wrong/file1.txt' exists in the repository. Did you mean one of the following?:\n" + "right/file1.txt",
		},
		{
			name:                    "missing directory for nested file",
			nonExistentRelativePath: filepath.Join("nested", "file1.txt"),
			setupFiles: []string{
				"nested/again/file1.txt",
				"nested/again/file2.txt",
			},
			expectedHint: "No file at 'nested/file1.txt' exists in the repository. Did you mean one of the following?:\n" + "nested/again/file1.txt",
		},
		{
			name:                    "multiple with same segment-based ratio sorts by overall string similarity",
			nonExistentRelativePath: filepath.Join("nested", "file1.txt"),
			setupFiles: []string{
				"nested/file0a.txt",
				"nested/file1a.txt",
				"nested/file2a.txt",
				"nested2/file1.txt",
			},
			expectedHint: `No file at 'nested/file1.txt' exists in the repository. Did you mean one of the following?:
nested/file1a.txt
nested2/file1.txt
nested/file0a.txt`,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			// Create a temporary directory for the test
			tmpDir := t.TempDir()

			// Set up files in the temporary directory
			for _, file := range tt.setupFiles {
				filePath := filepath.Join(tmpDir, file)
				if err := os.MkdirAll(filepath.Dir(filePath), 0755); err != nil {
					t.Fatalf("Failed to create directory for %s: %v", filePath, err)
				}
				if _, err := os.Create(filePath); err != nil {
					t.Fatalf("Failed to create file %s: %v", filePath, err)
				}
			}

			// Call the function and check the result
			ec := testRepository(tmpDir)
			hint := getHintForNonExistentFile(context.Background(), ec, tt.nonExistentRelativePath)
			if hint != tt.expectedHint {
				t.Errorf("Expected hint %q, but got %q", tt.expectedHint, hint)
			}
		})
	}
}

func TestShouldRetrieveFullFile(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		symbols      []string
		absolutePath string
		expected     bool
	}{
		{
			name:         "wildcard star",
			symbols:      []string{"*"},
			absolutePath: "/project/src/main.go",
			expected:     true,
		},
		{
			name:         "wildcard empty string",
			symbols:      []string{""},
			absolutePath: "/project/src/main.go",
			expected:     true,
		},
		{
			name:         "wildcard empty list",
			symbols:      nil,
			absolutePath: "/project/src/main.go",
			expected:     true,
		},
		{
			name:         "vue file with matching component name",
			symbols:      []string{"MyComponent"},
			absolutePath: "/project/src/components/MyComponent.vue",
			expected:     true,
		},
		{
			name:         "vue file with case-insensitive match",
			symbols:      []string{"mycomponent"},
			absolutePath: "/project/src/components/MyComponent.vue",
			expected:     true,
		},
		{
			name:         "vue index file uses parent directory name",
			symbols:      []string{"MyComponent"},
			absolutePath: "/project/src/components/MyComponent/index.vue",
			expected:     true,
		},
		{
			name:         "vue index file case-insensitive match",
			symbols:      []string{"mycomponent"},
			absolutePath: "/project/src/components/MyComponent/index.vue",
			expected:     true,
		},
		{
			name:         "vue index file with hyphenated directory name",
			symbols:      []string{"MyComponent"},
			absolutePath: "/project/src/components/My-Component/index.vue",
			expected:     true,
		},
		{
			name:         "vue index file with underscored directory name",
			symbols:      []string{"MyComponent"},
			absolutePath: "/project/src/components/My_Component/index.vue",
			expected:     true,
		},
		{
			name:         "vue file with non-matching symbol",
			symbols:      []string{"OtherComponent"},
			absolutePath: "/project/src/components/MyComponent.vue",
			expected:     false,
		},
		{
			name:         "vue index file with non-matching symbol",
			symbols:      []string{"OtherComponent"},
			absolutePath: "/project/src/components/MyComponent/index.vue",
			expected:     false,
		},
		{
			name:         "svelte index file not recognized without language support",
			symbols:      []string{"MyWidget"},
			absolutePath: "/project/src/components/MyWidget/index.svelte",
			expected:     false,
		},
		{
			name:         "go file with non-matching symbol",
			symbols:      []string{"SomeFunc"},
			absolutePath: "/project/src/main.go",
			expected:     false,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			result := shouldRetrieveFullFile(tc.symbols, tc.absolutePath)
			assert.Equal(t, tc.expected, result)
		})
	}
}

// The symbol index is built via the content-aware entry walk rather than Walk
// + one ReadFile per file.
func TestGetRelativeFilePathsBySymbolName(t *testing.T) {
	t.Parallel()

	testDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(testDir, "a.go"), []byte("package main\n\nfunc Alpha() {}\n"), 0644))
	require.NoError(t, os.MkdirAll(filepath.Join(testDir, "b"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(testDir, "b", "b.go"), []byte("package b\n\nfunc Alpha() {}\n\ntype Beta struct{}\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(testDir, "notes.txt"), []byte("Alpha Beta not code\n"), 0644))

	// An unreadable file must be skipped without failing the whole index,
	// since the index only powers best-effort failure hints.
	if os.Geteuid() != 0 {
		unreadable := filepath.Join(testDir, "unreadable.go")
		require.NoError(t, os.WriteFile(unreadable, []byte("package main\n\nfunc Hidden() {}\n"), 0644))
		require.NoError(t, os.Chmod(unreadable, 0o000))
	}

	ec := testRepository(testDir)
	symbolToPaths, err := getRelativeFilePathsBySymbolName(context.Background(), ec)
	require.NoError(t, err)

	assert.Equal(t, []string{"a.go", filepath.Join("b", "b.go")}, symbolToPaths["Alpha"])
	assert.Equal(t, []string{filepath.Join("b", "b.go")}, symbolToPaths["Beta"])
	assert.NotContains(t, symbolToPaths, "Hidden")
}

// ctxAwareReadRepo fails reads once its context is canceled, like remote repositories do.
type ctxAwareReadRepo struct {
	Repository
}

func (e *ctxAwareReadRepo) ReadFile(ctx context.Context, path string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return e.Repository.ReadFile(ctx, path)
}

// retryProvingRepo blocks its first read until that read's context is canceled
// and serves later reads normally.
type retryProvingRepo struct {
	Repository
	calls    atomic.Int32
	inFlight chan struct{}
}

func (e *retryProvingRepo) ReadFile(ctx context.Context, path string) ([]byte, error) {
	if e.calls.Add(1) == 1 {
		close(e.inFlight)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return e.Repository.ReadFile(ctx, path)
}

// While one caller's read is in flight, a waiter whose own context is canceled
// must return promptly, and a live waiter must transparently retry when the
// owning read is canceled rather than inheriting its failure.
func TestReadCacheWaitersDuringCanceledRead(t *testing.T) {
	t.Parallel()

	testDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(testDir, "main.go"), []byte("package main\n"), 0644))

	readRepo := &retryProvingRepo{Repository: testRepository(testDir), inFlight: make(chan struct{})}
	readCache := newReadCache(readRepo)

	ownerCtx, cancelOwner := context.WithCancel(context.Background())
	defer cancelOwner()
	ownerDone := make(chan error, 1)
	go func() {
		_, err := readCache.ReadFile(ownerCtx, "main.go")
		ownerDone <- err
	}()
	<-readRepo.inFlight

	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	canceledWaiterDone := make(chan error, 1)
	go func() {
		_, err := readCache.ReadFile(canceledCtx, "main.go")
		canceledWaiterDone <- err
	}()
	select {
	case err := <-canceledWaiterDone:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(2 * time.Second):
		t.Fatal("canceled waiter blocked behind an in-flight read")
	}

	liveWaiterDone := make(chan struct{})
	var liveBytes []byte
	var liveErr error
	go func() {
		defer close(liveWaiterDone)
		liveBytes, liveErr = readCache.ReadFile(context.Background(), "main.go")
	}()
	// Let the live waiter join the in-flight read before canceling it.
	time.Sleep(50 * time.Millisecond)
	cancelOwner()

	require.Error(t, <-ownerDone)
	select {
	case <-liveWaiterDone:
	case <-time.After(2 * time.Second):
		t.Fatal("live waiter did not retry after the owning read was canceled")
	}
	require.NoError(t, liveErr)
	assert.Equal(t, "package main\n", string(liveBytes))
}

// A read canceled mid-flight answers nothing about the file, so it must not
// poison the cache for later callers whose context is still live: one
// related-symbols lookup failing (which cancels its sibling loads) must not
// break another lookup sharing the cache.
func TestReadCacheRetriesAfterCanceledRead(t *testing.T) {
	t.Parallel()

	testDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(testDir, "main.go"), []byte("package main\n"), 0644))

	readCache := newReadCache(&ctxAwareReadRepo{Repository: testRepository(testDir)})
	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := readCache.ReadFile(canceledCtx, "main.go")
	require.Error(t, err)

	fileBytes, err := readCache.ReadFile(context.Background(), "main.go")
	require.NoError(t, err)
	assert.Equal(t, "package main\n", string(fileBytes))
}
