package tree_sitter

import (
	"bufio"
	"regexp"
	"strconv"
	"strings"
)

func ExtractSymbolDefinitionBlocks(content string) []CodeBlock {
	var blocks []CodeBlock
	var currentBlock CodeBlock
	var inCodeBlock bool
	var codeLines []string
	var headerLines []string
	var codeBlockDelimiter string

	scanner := bufio.NewScanner(strings.NewReader(content))
	for scanner.Scan() {
		line := scanner.Text()

		if strings.HasPrefix(line, "File: ") {
			// Reset header when encountering a new file header
			headerLines = nil
			currentBlock = CodeBlock{}
		}

		if strings.HasPrefix(line, "```") {
			if inCodeBlock {
				// End of code block
				currentBlock.Code = strings.Join(codeLines, "\n")
				currentBlock.BlockContent = codeBlockDelimiter + "\n" + strings.Join(codeLines, "\n") + "\n```"
				currentBlock.FullContent = strings.TrimSpace(strings.Join(append(headerLines, currentBlock.BlockContent), "\n"))
				blocks = append(blocks, currentBlock)
				inCodeBlock = false
				codeLines = nil
				headerLines = nil
				currentBlock = CodeBlock{}
				codeBlockDelimiter = ""
			} else {
				// Start of code block
				inCodeBlock = true
				codeBlockDelimiter = line
				currentBlock.HeaderContent = strings.TrimSpace(strings.Join(headerLines, "\n"))
				parseHeaderContent(&currentBlock)
			}
		} else if inCodeBlock {
			codeLines = append(codeLines, line)
		} else {
			headerLines = append(headerLines, line)
		}
	}

	// Handle case where the last block is not closed
	if inCodeBlock {
		currentBlock.Code = strings.Join(codeLines, "\n")
		currentBlock.BlockContent = codeBlockDelimiter + "\n" + strings.Join(codeLines, "\n") + "\n```"
		currentBlock.FullContent = strings.TrimSpace(strings.Join(append(headerLines, currentBlock.BlockContent), "\n"))
		blocks = append(blocks, currentBlock)
	}

	if len(blocks) == 0 {
		return nil
	}

	return blocks
}

func parseHeaderContent(block *CodeBlock) {
	headerContent := block.HeaderContent

	if headerContent == "" {
		return
	}

	fileRegex := regexp.MustCompile(`(?m)^File:\s*(.+)$`)
	if fileMatch := fileRegex.FindStringSubmatch(headerContent); len(fileMatch) > 1 {
		block.FilePath = strings.TrimSpace(fileMatch[1])
	}

	symbolRegex := regexp.MustCompile(`(?m)^Symbols?:\s*(.+)$`)
	if symbolMatch := symbolRegex.FindStringSubmatch(headerContent); len(symbolMatch) > 1 {
		block.Symbol = strings.TrimSpace(symbolMatch[1])
	}

	linesRegex := regexp.MustCompile(`(?m)^Lines:\s*(\d+)(?:-(\d+))?(?:\s*\(.*?\))?$`)
	if linesMatch := linesRegex.FindStringSubmatch(headerContent); len(linesMatch) > 1 {
		block.StartLine = parseInt(linesMatch[1])
		if len(linesMatch) > 2 && linesMatch[2] != "" {
			block.EndLine = parseInt(linesMatch[2])
		} else {
			block.EndLine = block.StartLine
		}
	}
}

// ExtractSearchCodeBlocks parses a string containing search results and returns a slice of CodeBlocks.
func ExtractSearchCodeBlocks(content string) []CodeBlock {
	var blocks []CodeBlock
	var currentBlock *CodeBlock
	var currentFilePath string

	scanner := bufio.NewScanner(strings.NewReader(content))

	filePathRegex := regexp.MustCompile(`^[^0-9\s-].*$|^\d+[^0-9:=-].*$`)
	lineNumberRegex := regexp.MustCompile(`^(\d+)([-=:])(.*)$`)

	for scanner.Scan() {
		line := scanner.Text()

		if line == "--" || line == "" || strings.HasPrefix(line, "... (search output truncated)") {
			// End the current block if there is one
			if currentBlock != nil {
				blocks = append(blocks, *currentBlock)
				currentBlock = nil
			}
			continue
		}

		if filePathRegex.MatchString(line) {
			// This is a new file path
			currentFilePath = line
			continue
		}

		if match := lineNumberRegex.FindStringSubmatch(line); match != nil {
			lineNumber, _ := strconv.Atoi(match[1])
			codeContent := match[3]

			if currentBlock == nil || currentBlock.FilePath != currentFilePath || lineNumber != currentBlock.EndLine+1 {
				// Start a new block
				if currentBlock != nil {
					blocks = append(blocks, *currentBlock)
				}
				currentBlock = &CodeBlock{
					FilePath:  currentFilePath,
					StartLine: lineNumber,
					EndLine:   lineNumber,
					Code:      codeContent,
				}
			} else {
				// Continue the current block
				currentBlock.EndLine = lineNumber
				currentBlock.Code += "\n" + codeContent
			}
		}
	}

	// Add the last block if there is one
	if currentBlock != nil {
		blocks = append(blocks, *currentBlock)
	}

	return blocks
}

func parseInt(s string) int {
	result, _ := strconv.Atoi(s)
	return result
}

// ExtractGitDiffCodeBlocks parses git diff output and returns CodeBlocks
// representing the state of the code after the diff is applied.
// Each block contains a contiguous set of lines from the new file state.
func ExtractGitDiffCodeBlocks(content string) []CodeBlock {
	var blocks []CodeBlock
	var currentFilePath string
	var currentBlock *CodeBlock
	var currentNewLineNum int

	scanner := bufio.NewScanner(strings.NewReader(content))

	diffHeaderRegex := regexp.MustCompile(`^diff --git a/.+ b/(.+)$`)
	hunkHeaderRegex := regexp.MustCompile(`^@@ -\d+(?:,\d+)? \+(\d+)(?:,\d+)? @@`)

	for scanner.Scan() {
		line := scanner.Text()

		if match := diffHeaderRegex.FindStringSubmatch(line); match != nil {
			if currentBlock != nil {
				blocks = append(blocks, *currentBlock)
				currentBlock = nil
			}
			currentFilePath = match[1]
			continue
		}

		if match := hunkHeaderRegex.FindStringSubmatch(line); match != nil {
			if currentBlock != nil {
				blocks = append(blocks, *currentBlock)
				currentBlock = nil
			}
			currentNewLineNum, _ = strconv.Atoi(match[1])
			continue
		}

		if currentFilePath == "" || currentNewLineNum == 0 {
			continue
		}

		if strings.HasPrefix(line, "-") {
			// Removed line - doesn't exist in new file, don't advance line number
			continue
		}

		// Skip diff metadata lines
		if strings.HasPrefix(line, "diff ") || strings.HasPrefix(line, "index ") ||
			strings.HasPrefix(line, "---") || strings.HasPrefix(line, "+++") ||
			strings.HasPrefix(line, "@@") || strings.HasPrefix(line, "\\") {
			continue
		}

		var codeContent string
		if strings.HasPrefix(line, "+") {
			codeContent = line[1:]
		} else if strings.HasPrefix(line, " ") {
			codeContent = line[1:]
		} else {
			// Context line without prefix (some diff formats)
			codeContent = line
		}

		if currentBlock == nil || currentNewLineNum != currentBlock.EndLine+1 {
			if currentBlock != nil {
				blocks = append(blocks, *currentBlock)
			}
			currentBlock = &CodeBlock{
				FilePath:  currentFilePath,
				StartLine: currentNewLineNum,
				EndLine:   currentNewLineNum,
				Code:      codeContent,
			}
		} else {
			currentBlock.EndLine = currentNewLineNum
			currentBlock.Code += "\n" + codeContent
		}
		currentNewLineNum++
	}

	if currentBlock != nil {
		blocks = append(blocks, *currentBlock)
	}

	return blocks
}

type CodeBlock struct {
	/* the relevant parts before the code block */
	HeaderContent string `json:"headerContent"`
	/* includes the triple backticks etc, as well as the source code content within */
	BlockContent string `json:"blockContent"`
	/* the code within the triple backticks */
	Code string `json:"code"`
	/* everything matched */
	FullContent string `json:"fullContent"`

	FilePath  string `json:"filePath"`
	Symbol    string `json:"symbol"`
	StartLine int    `json:"startLine"` // 1-indexed. 0 means unknown/missing
	EndLine   int    `json:"endLine"`   // 1-indexed. 0 means unknown/missing
}
