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

func TestGetSymbolDefinitions_Markdown_ByCanonicalSlug(t *testing.T) {
	t.Parallel()

	content := `# Hello World

Some intro text.

## Getting Started

Getting started content.

## Usage

Usage content.
`
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "test.md")
	err := os.WriteFile(filePath, []byte(content), 0644)
	require.NoError(t, err)

	// Lookup by canonical slug
	blocks, err := GetSymbolDefinitions(filePath, "#getting-started", 0)
	require.NoError(t, err)
	require.Len(t, blocks, 1)

	blockContent := blocks[0].String()
	assert.Contains(t, blockContent, "## Getting Started")
	assert.Contains(t, blockContent, "Getting started content.")
	// Should not include the next section
	assert.NotContains(t, blockContent, "## Usage")
}

func TestGetSymbolDefinitions_Markdown_BySlugWithoutHash(t *testing.T) {
	t.Parallel()

	content := `# Main Title

## Installation

Install instructions.
`
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "test.md")
	err := os.WriteFile(filePath, []byte(content), 0644)
	require.NoError(t, err)

	// Lookup by slug without leading #
	blocks, err := GetSymbolDefinitions(filePath, "installation", 0)
	require.NoError(t, err)
	require.Len(t, blocks, 1)

	blockContent := blocks[0].String()
	assert.Contains(t, blockContent, "## Installation")
}

func TestGetSymbolDefinitions_Markdown_ByRawHeadingText(t *testing.T) {
	t.Parallel()

	content := `# API Reference

## Getting Started

Content here.
`
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "test.md")
	err := os.WriteFile(filePath, []byte(content), 0644)
	require.NoError(t, err)

	// Lookup by raw heading text
	blocks, err := GetSymbolDefinitions(filePath, "Getting Started", 0)
	require.NoError(t, err)
	require.Len(t, blocks, 1)

	blockContent := blocks[0].String()
	assert.Contains(t, blockContent, "## Getting Started")
}

func TestGetSymbolDefinitions_Markdown_ByHeadingWithHashPrefix(t *testing.T) {
	t.Parallel()

	content := `# Main

## Section One

Content.
`
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "test.md")
	err := os.WriteFile(filePath, []byte(content), 0644)
	require.NoError(t, err)

	// Lookup by heading text with ### prefix
	blocks, err := GetSymbolDefinitions(filePath, "## Section One", 0)
	require.NoError(t, err)
	require.Len(t, blocks, 1)

	blockContent := blocks[0].String()
	assert.Contains(t, blockContent, "## Section One")
}

func TestGetSymbolDefinitions_Markdown_DuplicateHeadings(t *testing.T) {
	t.Parallel()

	content := `# Main

## Example

First example.

## Other

Other content.

## Example

Second example.
`
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "test.md")
	err := os.WriteFile(filePath, []byte(content), 0644)
	require.NoError(t, err)

	// Lookup should return multiple blocks for duplicate headings
	blocks, err := GetSymbolDefinitions(filePath, "#example", 0)
	require.NoError(t, err)
	require.Len(t, blocks, 2)

	// First block should contain first example
	assert.Contains(t, blocks[0].String(), "First example")
	// Second block should contain second example
	assert.Contains(t, blocks[1].String(), "Second example")
}

func TestGetSymbolDefinitions_Markdown_ParentChild(t *testing.T) {
	t.Parallel()

	content := `# Main

## Parent One

### Child

Child under parent one.

## Parent Two

### Child

Child under parent two.
`
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "test.md")
	err := os.WriteFile(filePath, []byte(content), 0644)
	require.NoError(t, err)

	// Lookup with parent disambiguation
	blocks, err := GetSymbolDefinitions(filePath, "parent-one.child", 0)
	require.NoError(t, err)
	require.Len(t, blocks, 1)

	blockContent := blocks[0].String()
	assert.Contains(t, blockContent, "Child under parent one")
	assert.NotContains(t, blockContent, "Child under parent two")
}

func TestGetSymbolDefinitions_Markdown_ParentChildFallback(t *testing.T) {
	t.Parallel()

	content := `# Main

## Section

### Unique Child

Content here.
`
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "test.md")
	err := os.WriteFile(filePath, []byte(content), 0644)
	require.NoError(t, err)

	// Lookup with non-matching parent should fall back to child-only
	blocks, err := GetSymbolDefinitions(filePath, "nonexistent.unique-child", 0)
	require.NoError(t, err)
	require.Len(t, blocks, 1)

	blockContent := blocks[0].String()
	assert.Contains(t, blockContent, "### Unique Child")
}

func TestGetSymbolDefinitions_Markdown_YamlFrontmatter(t *testing.T) {
	t.Parallel()

	content := `---
title: My Document
description: A test document
tags:
  - test
  - example
---

# Introduction

Some content.
`
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "test.md")
	err := os.WriteFile(filePath, []byte(content), 0644)
	require.NoError(t, err)

	// Lookup yaml_frontmatter
	blocks, err := GetSymbolDefinitions(filePath, "yaml_frontmatter", 0)
	require.NoError(t, err)
	require.Len(t, blocks, 1)

	blockContent := blocks[0].String()
	assert.Contains(t, blockContent, "title: My Document")
	assert.Contains(t, blockContent, "description: A test document")
	assert.Contains(t, blockContent, "tags:")
	// Frontmatter should NOT include subsequent headings/content
	assert.NotContains(t, blockContent, "# Introduction")
	assert.NotContains(t, blockContent, "Some content")
}

func TestGetSymbolDefinitions_Markdown_YamlFrontmatterNotFound(t *testing.T) {
	t.Parallel()

	content := `# No Frontmatter

Just content.
`
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "test.md")
	err := os.WriteFile(filePath, []byte(content), 0644)
	require.NoError(t, err)

	// Lookup yaml_frontmatter when not present
	_, err = GetSymbolDefinitions(filePath, "yaml_frontmatter", 0)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "symbol not found: yaml_frontmatter")
}

func TestGetSymbolDefinitions_Markdown_SectionRanges(t *testing.T) {
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

	// Section One should include Subsection A but not Section Two
	blocks, err := GetSymbolDefinitions(filePath, "#section-one", 0)
	require.NoError(t, err)
	require.Len(t, blocks, 1)

	blockContent := blocks[0].String()
	assert.Contains(t, blockContent, "## Section One")
	assert.Contains(t, blockContent, "### Subsection A")
	assert.Contains(t, blockContent, "Subsection content")
	assert.NotContains(t, blockContent, "## Section Two")
}

func TestGetSymbolDefinitions_Markdown_SetextHeadings(t *testing.T) {
	t.Parallel()

	content := "Main Title\n" +
		"===========\n" +
		"\n" +
		"Intro paragraph.\n" +
		"\n" +
		"Section One\n" +
		"-----------\n" +
		"\n" +
		"Section one content.\n"

	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "test.md")
	err := os.WriteFile(filePath, []byte(content), 0644)
	require.NoError(t, err)

	// Lookup setext heading
	blocks, err := GetSymbolDefinitions(filePath, "#section-one", 0)
	require.NoError(t, err)
	require.Len(t, blocks, 1)

	blockContent := blocks[0].String()
	assert.Contains(t, blockContent, "Section One")
	assert.Contains(t, blockContent, "Section one content")
}

func TestGetSymbolDefinitions_Markdown_SymbolNotFound(t *testing.T) {
	t.Parallel()

	content := `# Main

## Existing Section

Content.
`
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "test.md")
	err := os.WriteFile(filePath, []byte(content), 0644)
	require.NoError(t, err)

	// Lookup non-existent symbol
	_, err = GetSymbolDefinitions(filePath, "#nonexistent", 0)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "symbol not found")
}

func TestGetFileHeadersString_Markdown_WithFrontmatter(t *testing.T) {
	t.Parallel()

	content := `---
name: My Document
title: A Great Title
description: This is a description
  that spans multiple lines
  with indentation.
tags:
  - tag1
  - tag2
  - tag3
author: John Doe
---

# Main Content

Some text here.
`
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "test.md")
	err := os.WriteFile(filePath, []byte(content), 0644)
	require.NoError(t, err)

	result, err := GetFileHeadersString(filePath, 0)
	require.NoError(t, err)

	// Should contain the supported keys
	assert.Contains(t, result, "name: My Document")
	assert.Contains(t, result, "title: A Great Title")
	assert.Contains(t, result, "description: This is a description")
	assert.Contains(t, result, "that spans multiple lines")
	assert.Contains(t, result, "tags:")
	assert.Contains(t, result, "- tag1")
	assert.Contains(t, result, "- tag2")

	// Should NOT contain unsupported keys
	assert.NotContains(t, result, "author: John Doe")
}

func TestGetFileHeadersString_Markdown_SingleKey(t *testing.T) {
	t.Parallel()

	content := `---
title: Just a Title
author: Someone
---

# Content
`
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "test.md")
	err := os.WriteFile(filePath, []byte(content), 0644)
	require.NoError(t, err)

	result, err := GetFileHeadersString(filePath, 0)
	require.NoError(t, err)

	assert.Contains(t, result, "title: Just a Title")
	assert.NotContains(t, result, "author")
}

func TestGetFileHeadersString_Markdown_NoSupportedKeys(t *testing.T) {
	t.Parallel()

	content := `---
author: John Doe
version: 1.0
---

# Content
`
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "test.md")
	err := os.WriteFile(filePath, []byte(content), 0644)
	require.NoError(t, err)

	result, err := GetFileHeadersString(filePath, 0)
	require.NoError(t, err)

	// Should return empty string when no supported keys exist
	assert.Empty(t, result)
}

func TestGetFileHeadersString_Markdown_NoFrontmatter(t *testing.T) {
	t.Parallel()

	content := `# Just a Heading

Some content without frontmatter.
`
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "test.md")
	err := os.WriteFile(filePath, []byte(content), 0644)
	require.NoError(t, err)

	result, err := GetFileHeadersString(filePath, 0)
	require.NoError(t, err)

	// Should return empty string when no frontmatter exists
	assert.Empty(t, result)
}

func TestGetFileHeadersString_Markdown_MultilineDescription(t *testing.T) {
	t.Parallel()

	content := `---
description: |
  This is a multiline description
  using YAML block scalar syntax.
  It preserves newlines.
name: Test
---

# Content
`
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "test.md")
	err := os.WriteFile(filePath, []byte(content), 0644)
	require.NoError(t, err)

	result, err := GetFileHeadersString(filePath, 0)
	require.NoError(t, err)

	assert.Contains(t, result, "description: |")
	assert.Contains(t, result, "This is a multiline description")
	assert.Contains(t, result, "name: Test")
}

func TestGetFileHeadersString_Markdown_TagsList(t *testing.T) {
	t.Parallel()

	content := `---
tags:
  - documentation
  - markdown
  - tree-sitter
---

# Content
`
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "test.md")
	err := os.WriteFile(filePath, []byte(content), 0644)
	require.NoError(t, err)

	result, err := GetFileHeadersString(filePath, 0)
	require.NoError(t, err)

	assert.Contains(t, result, "tags:")
	assert.Contains(t, result, "- documentation")
	assert.Contains(t, result, "- markdown")
	assert.Contains(t, result, "- tree-sitter")
}

func TestGetFileHeadersString_Markdown_BlockScalarWithDashes(t *testing.T) {
	t.Parallel()

	content := `---
description: |
  This is a block scalar
  ---
  with dashes inside
title: My Title
---

# Content
`
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "test.md")
	err := os.WriteFile(filePath, []byte(content), 0644)
	require.NoError(t, err)

	result, err := GetFileHeadersString(filePath, 0)
	require.NoError(t, err)

	// Should preserve the indented --- inside the block scalar
	assert.Contains(t, result, "description: |")
	assert.Contains(t, result, "  ---")
	assert.Contains(t, result, "with dashes inside")
	assert.Contains(t, result, "title: My Title")
}

func TestGetFileHeaders_Markdown_FormattingWithSeparators(t *testing.T) {
	t.Parallel()

	content := `---
name: Doc Name
title: Doc Title
---

# Content
`
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "test.md")
	err := os.WriteFile(filePath, []byte(content), 0644)
	require.NoError(t, err)

	headers, err := GetFileHeaders(filePath, 0)
	require.NoError(t, err)
	require.Len(t, headers, 2)

	// Verify FormatHeaders produces correct output with separators
	formatted := FormatHeaders(headers)
	assert.Contains(t, formatted, "name: Doc Name")
	assert.Contains(t, formatted, "title: Doc Title")
	// FormatHeaders uses "\n---\n" as separator between multiple blocks
	assert.Contains(t, formatted, "\n---\n")
}

func TestGetAllAlternativeFileSymbols_Markdown(t *testing.T) {
	t.Parallel()

	mdContent := `# Introduction

Some intro text.

## Getting Started

Getting started content.

### Installation

Install instructions.

Heading Two
===========

Setext content.
`

	tmpFile, err := os.CreateTemp("", "test_*.md")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	if _, err := tmpFile.WriteString(mdContent); err != nil {
		t.Fatalf("Failed to write to temp file: %v", err)
	}
	tmpFile.Close()

	symbols, err := GetAllAlternativeFileSymbols(tmpFile.Name())
	if err != nil {
		t.Fatalf("GetAllAlternativeFileSymbols failed: %v", err)
	}

	// Collect all symbol contents
	symbolContents := make(map[string]bool)
	for _, sym := range symbols {
		symbolContents[sym.Content] = true
	}

	// Check canonical symbols exist
	expectedCanonical := []string{
		"#introduction",
		"#getting-started",
		"#installation",
		"#heading-two",
	}
	for _, expected := range expectedCanonical {
		if !symbolContents[expected] {
			t.Errorf("Expected canonical symbol %q not found", expected)
		}
	}

	// Check alternative symbols exist
	expectedAlternatives := []string{
		// Raw heading text without # prefix
		"Introduction",
		"Getting Started",
		"Installation",
		"Heading Two",
		// Slug only (without leading #)
		"introduction",
		"getting-started",
		"installation",
		"heading-two",
		// Original heading line with # prefix (ATX only)
		"# Introduction",
		"## Getting Started",
		"### Installation",
	}
	for _, expected := range expectedAlternatives {
		if !symbolContents[expected] {
			t.Errorf("Expected alternative symbol %q not found", expected)
		}
	}
}
