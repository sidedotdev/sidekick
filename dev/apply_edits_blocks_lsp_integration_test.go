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

	// Create LSPActivities with real Jsonrpc2LSPClient - this will be reused
	// across both FindReferencesActivity calls to reproduce the caching issue
	lspa := lsp.NewLSPActivities(func(language string) lsp.LSPClient {
		return &lsp.Jsonrpc2LSPClient{
			LanguageName: "golang",
		}
	})

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
		EnabledFlags: []string{}, // check-edits flag not passed in, thus no git repo is needed
	}

	devActivities := &DevActivities{LSPActivities: lspa} // should use same lspa

	// Use a context with timeout to avoid test hanging if LSP is slow
	ctxWithTimeout, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	reports, err := devActivities.ApplyEditBlocks(ctxWithTimeout, applyInput)
	require.NoError(t, err)
	require.Len(t, reports, 1)
	require.True(t, reports[0].DidApply, "Edit block should have been applied successfully")
	require.Empty(t, reports[0].Error, "Edit block should not have errors")

	// Step 3: Use FindReferencesActivity again with the same LSPActivities instance
	// This is a regression test so we don't fail with a line number out of range error because the LSP server
	// still has the old view of the file but the file has been modified
	secondRefs, err := lspa.FindReferencesActivity(ctx, findRefsInput)
	require.NoError(t, err)
	if len(secondRefs) != len(initialRefs) {
		t.Errorf("Reference count changed unexpectedly, indicating stale LSP state")
	}
}
