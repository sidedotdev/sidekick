package dev

import (
	"context"
	"os"
	"path/filepath"
	"sidekick/coding/lsp"
	"sidekick/env"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestLSPDocumentSyncWhenApplyingEditBlocks ensures the LSP server state doesn't
// become stale after file modifications via ApplyEditBlocksActivity, ensuring
// document syncs occurred.
func TestLSPDocumentSyncWhenApplyingEditBlocks_golang(t *testing.T) {
	t.Parallel()
	// Create a temporary directory for test files
	tempDir := t.TempDir()

	// Define initial test file content with a simple function
	initialContent := `package testpkg

import "fmt"

func TestFunction() {
	fmt.Println("original function")
}

func main() {
	TestFunction()
}
`
	// Write test file to the temporary directory
	testFilePath := filepath.Join(tempDir, "test.go")
	err := os.WriteFile(testFilePath, []byte(initialContent), 0644)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(tempDir, "go.mod"), []byte("module example.com/fixture\n\ngo 1.23\n"), 0644))
	initCommittedGitRepo(t, tempDir)

	// Create LSPActivities with real Jsonrpc2LSPClient - this will be reused
	// across both FindReferencesActivity calls to reproduce the caching issue
	lspa := lsp.NewLSPActivities(func(language string) lsp.LSPClient {
		return &lsp.Jsonrpc2LSPClient{
			LanguageName: "golang",
		}
	})
	t.Cleanup(func() { require.NoError(t, lspa.Close()) })

	ctx := context.Background()
	envContainer := env.EnvContainer{
		Env: &env.LocalEnv{WorkingDirectory: tempDir},
	}

	// Step 1: Use FindReferencesActivity to find references to TestFunction
	findRefsInput := lsp.FindReferencesActivityInput{
		EnvContainer:     envContainer,
		RelativeFilePath: filepath.Base(testFilePath),
		SymbolText:       "TestFunction",
	}

	initialRefs, err := lspa.FindReferencesActivity(ctx, findRefsInput)
	require.NoError(t, err)
	require.Len(t, initialRefs, 1, "Should find one reference to TestFunction initially")

	// Step 2: Use ApplyEditBlocksActivity to append content to the function
	// This will modify the file and make the LSP server's view stale
	editBlock := EditBlock{
		FilePath:       filepath.Base(testFilePath),
		EditType:       "update",
		SequenceNumber: 1,
		OldLines: []string{
			"func TestFunction() {",
		},
		NewLines: []string{
			"/*",
			"multi-",
			"line",
			"comment",
			"that",
			"is",
			"long",
			"enough",
			"to",
			"move",
			"the",
			"symbol",
			"past",
			"the",
			"end",
			"of",
			"the",
			"original",
			"file",
			"*/",
			"func TestFunction() {",
		},
	}

	applyInput := ApplyEditBlockActivityInput{
		EnvContainer: envContainer,
		EditBlocks:   []EditBlock{editBlock},
	}

	devActivities := &DevActivities{LSPActivities: lspa} // should use same lspa

	// Use a context with timeout to avoid test hanging if LSP is slow
	ctxWithTimeout, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	reports, err := devActivities.ApplyEditBlocks(ctxWithTimeout, applyInput)
	require.NoError(t, err)
	require.Len(t, reports, 1)
	require.True(t, reports[0].DidApply, "Edit block should have been applied successfully")
	require.Empty(t, reports[0].Error, "Edit block should not have errors")

	// Step 3: Use FindReferencesActivity again with the same LSPActivities instance
	// This is a regression test so we don't fail with a line number out of range error because the LSP server
	// still has the old view of the file but the file has been modified
	secondRefs, err := lspa.FindReferencesActivity(ctxWithTimeout, findRefsInput)
	require.NoError(t, err)
	expected := initialRefs[0]
	lineDelta := len(editBlock.NewLines) - len(editBlock.OldLines)
	expected.Range.Start.Line += lineDelta
	expected.Range.End.Line += lineDelta
	require.Equal(t, []lsp.Location{expected}, secondRefs)

	otherDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(otherDir, "go.mod"), []byte("module example.com/other\n\ngo 1.23\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(otherDir, "other.go"), []byte("package other\nfunc Other() {}\n"), 0644))
	otherContainer := env.EnvContainer{Env: &env.LocalEnv{WorkingDirectory: otherDir}}
	definitions, err := lspa.GetSingleFileDefinitions(ctxWithTimeout, lsp.LSPDefinitionLocationsRequest{
		EnvContainer: &otherContainer, FilePath: "other.go", Symbols: []string{"Other"},
	})
	require.NoError(t, err)
	require.Len(t, definitions, 1)
	require.Empty(t, definitions[0].Error)

	reinitializedRefs, err := lspa.FindReferencesActivity(ctxWithTimeout, findRefsInput)
	require.NoError(t, err)
	require.Equal(t, secondRefs, reinitializedRefs, "fresh locations must survive eviction and initialization from disk")
}
