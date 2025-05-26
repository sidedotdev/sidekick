package dev

import (
	"context"
	"os"
	"path/filepath"
	"sidekick/coding/lsp"
	"sidekick/env"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestLSPStaleStateWithEditBlocks reproduces the issue where
// LSP server state becomes stale after file modifications via ApplyEditBlocksActivity.
// This test should initially fail with a line number out of range error.
func TestLSPStaleStateWithEditBlocks(t *testing.T) {
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
			"\tfmt.Println(\"original function\")",
			"}",
		},
		NewLines: []string{
			"// some comment to make TestFunction move to another line",
			"func TestFunction() {",
			"\tfmt.Println(\"original function\")",
			"}",
		},
	}

	applyInput := ApplyEditBlockActivityInput{
		EnvContainer: envContainer,
		EditBlocks:   []EditBlock{editBlock},
		EnabledFlags: []string{}, // No flags to avoid checks that might interfere
	}

	devActivities := &DevActivities{LSPActivities: lspa} // should use same lspa

	reports, err := devActivities.ApplyEditBlocks(context.Background(), applyInput)
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
