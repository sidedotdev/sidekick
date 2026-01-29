package tree_sitter

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetDirectorySignatureOutlines_TruncationWhitespaceOnly(t *testing.T) {
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
	sigContent, err := GetFileSignaturesString(filePath)
	require.NoError(t, err)

	// The signature should end with "---\n", verify it has trailing whitespace
	require.True(t, strings.HasSuffix(sigContent, "\n"), "signature should end with newline")

	// Set maxContentLength to truncate only the trailing newline
	maxLen := len(sigContent) - 1
	signaturePaths := map[string]int{"test.md": maxLen}

	outlines, err := GetDirectorySignatureOutlines(tmpDir, nil, &signaturePaths)
	require.NoError(t, err)

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

func TestGetDirectorySignatureOutlines_TruncationWithContent(t *testing.T) {
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
	sigContent, err := GetFileSignaturesString(filePath)
	require.NoError(t, err)

	// Set maxContentLength to truncate actual content (not just whitespace)
	maxLen := len(sigContent) - 20
	signaturePaths := map[string]int{"test.md": maxLen}

	outlines, err := GetDirectorySignatureOutlines(tmpDir, nil, &signaturePaths)
	require.NoError(t, err)

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
