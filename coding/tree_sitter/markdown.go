package tree_sitter

import (
	"regexp"
	"strings"
	"unicode"

	// tree-sitter-grammars/tree-sitter-markdown doesn't have Go bindings yet;
	// using ehsanul fork's golang-language-bindings branch (see replace in go.mod)
	tree_sitter_markdown "github.com/tree-sitter-grammars/tree-sitter-markdown/bindings/go"
	tree_sitter "github.com/tree-sitter/go-tree-sitter"
)

func getMarkdownLanguage() *tree_sitter.Language {
	return tree_sitter.NewLanguage(tree_sitter_markdown.Language())
}

// writeMarkdownSignatureCapture emits heading signatures in original source form.
// ATX headings preserve their # markers, Setext headings preserve their underlines.
// YAML frontmatter captures are ignored for signatures.
func writeMarkdownSignatureCapture(out *strings.Builder, sourceCode *[]byte, c tree_sitter.QueryCapture, name string) {
	content := c.Node.Utf8Text(*sourceCode)
	switch name {
	case "heading.marker":
		out.WriteString(content)
		out.WriteString(" ")
	case "heading.content":
		out.WriteString(content)
		out.WriteString("\n")
	case "setext_heading.h1.content", "setext_heading.h2.content":
		out.WriteString(strings.TrimSpace(content))
		out.WriteString("\n")
	case "setext_heading.h1.underline", "setext_heading.h2.underline":
		out.WriteString(content)
		out.WriteString("\n")
	}
}

// writeMarkdownSymbolCapture emits canonical heading symbols (#<slug>) and yaml_frontmatter.
func writeMarkdownSymbolCapture(out *strings.Builder, sourceCode *[]byte, c tree_sitter.QueryCapture, name string) {
	content := c.Node.Utf8Text(*sourceCode)
	switch name {
	case "heading.content", "setext_heading.h1.content", "setext_heading.h2.content":
		slug := slugifyHeading(content)
		out.WriteString("#")
		out.WriteString(slug)
	case "frontmatter.name":
		// Emit fixed symbol name for YAML frontmatter
		_ = content
		out.WriteString("yaml_frontmatter")
	}
}

// slugifyHeading converts heading text to GitHub-style anchor slug.
// This follows GitHub's heading ID generation algorithm (without de-duplication suffixes).
func slugifyHeading(text string) string {
	text = strings.TrimSpace(text)
	text = strings.ToLower(text)

	var result strings.Builder
	for _, r := range text {
		if r == ' ' || r == '-' {
			result.WriteRune('-')
		} else if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' {
			result.WriteRune(r)
		}
	}

	// Collapse multiple consecutive hyphens
	slug := result.String()
	for strings.Contains(slug, "--") {
		slug = strings.ReplaceAll(slug, "--", "-")
	}

	// Trim leading/trailing hyphens
	slug = strings.Trim(slug, "-")

	return slug
}

var headingPrefixRegex = regexp.MustCompile(`^#{1,6}\s*`)

// NormalizeMarkdownSymbol normalizes user input to canonical heading symbol form.
// It handles: raw heading text, text with ### prefix, #anchor-style, or slug.
func NormalizeMarkdownSymbol(input string) string {
	input = strings.TrimSpace(input)

	if input == "yaml_frontmatter" {
		return input
	}

	// Strip leading heading markers (### Heading -> Heading)
	input = headingPrefixRegex.ReplaceAllString(input, "")

	// Strip leading # used as anchor prefix (#my-heading -> my-heading)
	input = strings.TrimPrefix(input, "#")

	input = strings.TrimSpace(input)

	if input == "" {
		return ""
	}

	// Slugify and return canonical form
	slug := slugifyHeading(input)
	return "#" + slug
}

// getMarkdownHeadingLevel extracts the heading level from an ATX heading marker (# to ######).
func getMarkdownHeadingLevel(marker string) int {
	level := 0
	for _, r := range marker {
		if r == '#' {
			level++
		} else {
			break
		}
	}
	if level > 6 {
		level = 6
	}
	return level
}

// getSetextHeadingLevel returns 1 for = underline, 2 for - underline.
func getSetextHeadingLevel(underline string) int {
	if len(underline) > 0 && underline[0] == '=' {
		return 1
	}
	return 2
}

// computeMarkdownSectionRanges adjusts symbol EndPoints so each heading's range
// covers its entire section (from heading start to just before the next heading
// of the same or higher level, or EOF).
func computeMarkdownSectionRanges(symbols []Symbol, sourceCode *[]byte) []Symbol {
	if len(symbols) == 0 {
		return symbols
	}

	// Count total lines in source
	totalLines := uint(strings.Count(string(*sourceCode), "\n"))
	if len(*sourceCode) > 0 && (*sourceCode)[len(*sourceCode)-1] != '\n' {
		totalLines++
	}

	// Extract heading levels for each symbol
	type headingInfo struct {
		index int
		level int
	}
	var headings []headingInfo

	for i, sym := range symbols {
		level := getHeadingLevelFromSymbol(sym)
		if level > 0 {
			headings = append(headings, headingInfo{index: i, level: level})
		}
	}

	// For each heading, find where its section ends
	for i, h := range headings {
		// Find the next heading of same or higher level (lower number = higher level)
		var sectionEndRow uint = totalLines
		for j := i + 1; j < len(headings); j++ {
			if headings[j].level <= h.level {
				// Next heading of same or higher level ends this section
				sectionEndRow = symbols[headings[j].index].StartPoint.Row
				break
			}
		}

		// Update the symbol's end point to cover the section
		if sectionEndRow > symbols[h.index].EndPoint.Row {
			symbols[h.index].EndPoint = tree_sitter.Point{
				Row:    sectionEndRow,
				Column: 0,
			}
		}
	}

	return symbols
}

// getHeadingLevelFromSymbol extracts the heading level from a markdown symbol.
func getHeadingLevelFromSymbol(sym Symbol) int {
	for _, sc := range sym.SourceCaptures {
		switch sc.Name {
		case "heading.marker":
			return getMarkdownHeadingLevel(sc.Content)
		case "setext_heading.h1.underline":
			return 1
		case "setext_heading.h2.underline":
			return 2
		}
	}
	return 0
}
