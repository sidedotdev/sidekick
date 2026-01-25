package tree_sitter

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetFileSignaturesString_Markdown(t *testing.T) {
	t.Parallel()

	content := `# Main Title

Some intro text.

## Section One

Content here.

### Subsection

More content.

## Section Two

Final content.
`
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "test.md")
	err := os.WriteFile(filePath, []byte(content), 0644)
	require.NoError(t, err)

	result, err := GetFileSignaturesString(filePath)
	require.NoError(t, err)

	// Should contain all headings in original form
	assert.Contains(t, result, "# Main Title")
	assert.Contains(t, result, "## Section One")
	assert.Contains(t, result, "### Subsection")
	assert.Contains(t, result, "## Section Two")
}

func TestGetFileSignaturesString_Markdown_Setext(t *testing.T) {
	t.Parallel()

	content := `Main Title
===========

Some content.

Section Two
-----------

More content.
`
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "test.md")
	err := os.WriteFile(filePath, []byte(content), 0644)
	require.NoError(t, err)

	result, err := GetFileSignaturesString(filePath)
	require.NoError(t, err)

	// Setext headings should preserve original form including underline
	assert.Contains(t, result, "Main Title")
	assert.Contains(t, result, "===========")
	assert.Contains(t, result, "Section Two")
	assert.Contains(t, result, "-----------")
}

func TestGetFileSymbolsString_Markdown(t *testing.T) {
	t.Parallel()

	content := `# Hello World

## Getting Started

### Installation

## Usage
`
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "test.md")
	err := os.WriteFile(filePath, []byte(content), 0644)
	require.NoError(t, err)

	result, err := GetFileSymbolsString(filePath)
	require.NoError(t, err)

	// Should contain canonical slugified symbols
	assert.Contains(t, result, "#hello-world")
	assert.Contains(t, result, "#getting-started")
	assert.Contains(t, result, "#installation")
	assert.Contains(t, result, "#usage")
}

func TestGetFileSymbolsString_Markdown_Setext(t *testing.T) {
	t.Parallel()

	// Use explicit string concatenation to ensure === line is preserved
	content := "Main Title\n" +
		"===========\n" +
		"\n" +
		"Some content.\n" +
		"\n" +
		"Section Two\n" +
		"-----------\n" +
		"\n" +
		"More content.\n"

	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "test.md")
	err := os.WriteFile(filePath, []byte(content), 0644)
	require.NoError(t, err)

	result, err := GetFileSymbolsString(filePath)
	require.NoError(t, err)

	// Setext headings should also produce canonical slugified symbols
	assert.Contains(t, result, "#main-title")
	assert.Contains(t, result, "#section-two")
}

func TestGetFileSymbolsString_Markdown_WithFrontmatter(t *testing.T) {
	t.Parallel()

	content := `---
title: My Document
description: A test document
---

# Introduction

Some content.
`
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "test.md")
	err := os.WriteFile(filePath, []byte(content), 0644)
	require.NoError(t, err)

	result, err := GetFileSymbolsString(filePath)
	require.NoError(t, err)

	// Should contain yaml_frontmatter and heading symbols
	assert.Contains(t, result, "yaml_frontmatter")
	assert.Contains(t, result, "#introduction")
}

func TestGetFileSymbols_Markdown_SectionRanges(t *testing.T) {
	t.Parallel()

	content := `# Main Title

Intro paragraph.

## Section One

Section one content.
More content here.

### Subsection A

Subsection content.

## Section Two

Section two content.
`
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "test.md")
	err := os.WriteFile(filePath, []byte(content), 0644)
	require.NoError(t, err)

	symbols, err := GetFileSymbols(filePath)
	require.NoError(t, err)

	// Find symbols by content
	var mainTitle, sectionOne, subsectionA, sectionTwo *Symbol
	for i := range symbols {
		switch symbols[i].Content {
		case "#main-title":
			mainTitle = &symbols[i]
		case "#section-one":
			sectionOne = &symbols[i]
		case "#subsection-a":
			subsectionA = &symbols[i]
		case "#section-two":
			sectionTwo = &symbols[i]
		}
	}

	require.NotNil(t, mainTitle, "main-title symbol not found")
	require.NotNil(t, sectionOne, "section-one symbol not found")
	require.NotNil(t, subsectionA, "subsection-a symbol not found")
	require.NotNil(t, sectionTwo, "section-two symbol not found")

	// Main title (h1) should extend to end of file since no other h1
	lines := strings.Split(content, "\n")
	lastLineRow := uint(len(lines) - 1)
	assert.Equal(t, lastLineRow, mainTitle.EndPoint.Row, "main-title should extend to EOF")

	// Section One (h2) should end where Section Two (h2) starts
	assert.Equal(t, sectionTwo.StartPoint.Row, sectionOne.EndPoint.Row,
		"section-one should end where section-two starts")

	// Subsection A (h3) should end where Section Two (h2) starts (higher level heading)
	assert.Equal(t, sectionTwo.StartPoint.Row, subsectionA.EndPoint.Row,
		"subsection-a should end where section-two starts")
}

func TestGetFileSymbols_Markdown_SetextSectionRanges(t *testing.T) {
	t.Parallel()

	// Use explicit string concatenation to ensure === line is preserved
	content := "Main Title\n" +
		"===========\n" +
		"\n" +
		"Intro paragraph.\n" +
		"\n" +
		"Section One\n" +
		"-----------\n" +
		"\n" +
		"Section one content.\n" +
		"\n" +
		"Section Two\n" +
		"-----------\n" +
		"\n" +
		"Section two content.\n"

	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "test.md")
	err := os.WriteFile(filePath, []byte(content), 0644)
	require.NoError(t, err)

	symbols, err := GetFileSymbols(filePath)
	require.NoError(t, err)

	// Find symbols by content
	var mainTitle, sectionOne, sectionTwo *Symbol
	for i := range symbols {
		switch symbols[i].Content {
		case "#main-title":
			mainTitle = &symbols[i]
		case "#section-one":
			sectionOne = &symbols[i]
		case "#section-two":
			sectionTwo = &symbols[i]
		}
	}

	require.NotNil(t, mainTitle, "main-title symbol not found")
	require.NotNil(t, sectionOne, "section-one symbol not found")
	require.NotNil(t, sectionTwo, "section-two symbol not found")

	// Main title (h1 via ===) should extend to end of file
	lines := strings.Split(content, "\n")
	lastLineRow := uint(len(lines) - 1)
	assert.Equal(t, lastLineRow, mainTitle.EndPoint.Row, "main-title should extend to EOF")

	// Section One (h2 via ---) should end where Section Two starts
	assert.Equal(t, sectionTwo.StartPoint.Row, sectionOne.EndPoint.Row,
		"section-one should end where section-two starts")
}

func TestSlugifyHeading(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input    string
		expected string
	}{
		{"Hello World", "hello-world"},
		{"Getting Started", "getting-started"},
		{"Installation & Setup", "installation-setup"},
		{"API Reference (v2)", "api-reference-v2"},
		{"  Trimmed  ", "trimmed"},
		{"Multiple   Spaces", "multiple-spaces"},
		{"Special!@#$%Characters", "specialcharacters"},
		{"Numbers 123 Here", "numbers-123-here"},
		{"Under_scores_work", "under_scores_work"},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			t.Parallel()
			result := slugifyHeading(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestNormalizeMarkdownSymbol(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"raw heading text", "Hello World", "#hello-world"},
		{"with hash prefix", "### Hello World", "#hello-world"},
		{"anchor style", "#hello-world", "#hello-world"},
		{"slug only", "hello-world", "#hello-world"},
		{"yaml_frontmatter unchanged", "yaml_frontmatter", "yaml_frontmatter"},
		{"with spaces", "  Getting Started  ", "#getting-started"},
		{"single hash heading", "# Title", "#title"},
		{"six hash heading", "###### Deep", "#deep"},
		{"empty string", "", ""},
		{"whitespace only", "   ", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := NormalizeMarkdownSymbol(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestNormalizeSymbolFromSnippet_Markdown(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		snippet     string
		expected    string
		expectError bool
	}{
		{"ATX heading", "# Hello World", "#hello-world", false},
		{"ATX h2", "## Getting Started", "#getting-started", false},
		{"ATX h3", "### Installation", "#installation", false},
		{"plain text (not a heading)", "Just some text", "", true},
		{"empty", "", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result, err := NormalizeSymbolFromSnippet("markdown", tt.snippet)
			if tt.expectError {
				assert.Error(t, err)
				assert.ErrorIs(t, err, ErrNoSymbolParsed)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.expected, result)
			}
		})
	}
}

func TestGetMarkdownHeadingLevel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		marker   string
		expected int
	}{
		{"#", 1},
		{"##", 2},
		{"###", 3},
		{"####", 4},
		{"#####", 5},
		{"######", 6},
		{"#######", 6}, // max is 6
		{"# ", 1},
		{"", 0},
	}

	for _, tt := range tests {
		t.Run(tt.marker, func(t *testing.T) {
			t.Parallel()
			result := getMarkdownHeadingLevel(tt.marker)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGetSetextHeadingLevel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		underline string
		expected  int
	}{
		{"===", 1},
		{"=", 1},
		{"---", 2},
		{"-", 2},
		{"", 2}, // default to 2 for empty
	}

	for _, tt := range tests {
		t.Run(tt.underline, func(t *testing.T) {
			t.Parallel()
			result := getSetextHeadingLevel(tt.underline)
			assert.Equal(t, tt.expected, result)
		})
	}
}
