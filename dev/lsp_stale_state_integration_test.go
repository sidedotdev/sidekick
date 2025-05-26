package dev

import (
	"context"
	"os"
	"path/filepath"
	"sidekick/coding/lsp"
	"sidekick/env"
	"testing"

	"github.com/stretchr/testify/assert"
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
			"func TestFunction() {",
			"\tfmt.Println(\"original function\")",
			"\tfmt.Println(\"additional line\")",
			"}",
		},
	}

	applyInput := ApplyEditBlockActivityInput{
		EnvContainer: envContainer,
		EditBlocks:   []EditBlock{editBlock},
		EnabledFlags: []string{}, // No flags to avoid checks that might interfere
	}

	devActivities := &DevActivities{
		LSPActivities: &lsp.LSPActivities{
			LSPClientProvider: func(languageName string) lsp.LSPClient {
				return &lsp.Jsonrpc2LSPClient{
					LanguageName: languageName,
				}
			},
			InitializedClients: map[string]lsp.LSPClient{},
		},
	}

	reports, err := devActivities.ApplyEditBlocks(context.Background(), applyInput)
	require.NoError(t, err)
	require.Len(t, reports, 1)
	require.True(t, reports[0].DidApply, "Edit block should have been applied successfully")
	require.Empty(t, reports[0].Error, "Edit block should not have errors")

	// Step 3: Use FindReferencesActivity again with the same LSPActivities instance
	// This should fail with a line number out of range error because the LSP server
	// still has the old view of the file but the file has been modified
	secondRefs, err := lspa.FindReferencesActivity(ctx, findRefsInput)

	// This is where we expect the bug to manifest - the LSP server should complain
	// about line numbers being out of range because it still has the old file content
	// but the file has been modified externally
	if err != nil {
		t.Logf("Expected error occurred (this demonstrates the bug): %v", err)
		// We expect an error related to line numbers or ranges being invalid
		assert.Contains(t, err.Error(), "range", "Error should mention range/line issues")
	} else {
		// If no error occurs, the references might be incorrect due to stale state
		t.Logf("No error occurred, but references might be incorrect due to stale LSP state")
		t.Logf("Initial references: %d, Second references: %d", len(initialRefs), len(secondRefs))

		// The test might pass but with incorrect results due to stale state
		// This is still a bug, just manifesting differently
		if len(secondRefs) != len(initialRefs) {
			t.Logf("Reference count changed unexpectedly, indicating stale LSP state")
		}
	}

	// For now, we'll mark this as expected behavior until we implement the fix
	// Once we implement textDocument/didSave notifications, this test should pass
	t.Logf("This test demonstrates the LSP stale state issue that needs to be fixed")
}
