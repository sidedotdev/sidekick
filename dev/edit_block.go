package dev

import (
	"bufio"
	"sidekick/coding/tree_sitter"
	"sidekick/llm"
	"sidekick/utils"
	"strconv" // Added to use the Atoi function
	"strings"
)

// EditBlock represents a block of edits, including the file path, old lines, and new lines.
type EditBlock struct {
	FilePath string `json:"filePath"`
	// FIXME /gen/req remove AbsoluteFilePath: it's temporary until we refactor the symbol to visible file ranges stuff
	AbsoluteFilePath string   `json:"-"`
	OldLines         []string `json:"oldLines"`
	NewLines         []string `json:"newLines"`
	// TODO /gen typedef string with consts for: "create", "append", "update", or "delete"
	EditType string `json:"editType"`
	// Sequence number of the edit block
	SequenceNumber int `json:"sequenceNumber"`
	// file ranges that were visible when this edit block was created
	VisibleFileRanges []FileRange `json:"visibleFileRanges"`
	// code blocks that were visible when this edit block was created
	VisibleCodeBlocks []tree_sitter.CodeBlock `json:"visibleCodeBlocks"`
}

type FileRange struct {
	FilePath  string `json:"filePath"`
	StartLine int    `json:"startLine"`
	EndLine   int    `json:"endLine"`
}

// TODO: this doesn't handle edit blocks that were applied successfully, where
// the new lines should be returned as a code block
func extractAllCodeBlocks(chatHistory []llm.ChatMessage) []tree_sitter.CodeBlock {
	codeBlocks := make([]tree_sitter.CodeBlock, 0)
	for _, chatMessage := range chatHistory {
		if chatMessage.Role != llm.ChatMessageRoleAssistant {
			symDefCodeBlocks := tree_sitter.ExtractSymbolDefinitionBlocks(chatMessage.Content)
			searchCodeBlocks := tree_sitter.ExtractSearchCodeBlocks(chatMessage.Content)
			codeBlocks = append(codeBlocks, symDefCodeBlocks...)
			codeBlocks = append(codeBlocks, searchCodeBlocks...)
		}
	}
	return codeBlocks
}

// ExtractEditBlocks extracts edit blocks from the given string.
func ExtractEditBlocks(text string) ([]*EditBlock, error) {
	scanner := bufio.NewScanner(strings.NewReader(text))

	var blocks []*EditBlock // the blocks of edits
	var block *EditBlock    // the current block
	var oldLines, newLines *[]string
	var sequenceNumber int // the sequence number for the current block

	inCodeBlock := false    // flag whether the scanner is in a code block
	lastFilePath := ""      // keeps the last file path
	maybeNextFilePath := "" // keeps the potential next file path

	for scanner.Scan() {
		line := scanner.Text()

		// If a backtick fence is found, toggle the inCodeBlock flag
		if strings.HasPrefix(line, "```") {
			inCodeBlock = !inCodeBlock
			if inCodeBlock {
				// if entering a code block, reset everything
				newLines = nil
				oldLines = nil
				// we'll get a new file path now, don't use the old one if any since it's a brand-new code block
				lastFilePath = ""
				maybeNextFilePath = ""
			}
			continue // skip the rest of the loop
		}

		// continue with the loop if not in a code block
		if !inCodeBlock {
			continue
		}

		if strings.HasPrefix(line, "<<<<<<<") {
			editType := "update" // default edit type, corresponds to SEARCH but we aren't checking that
			switch {
			case strings.Contains(line, "CREATE_FILE"):
				editType = "create"
			case strings.Contains(line, "APPEND_TO_FILE"):
				editType = "append"
			case strings.Contains(line, "DELETE_FILE"):
				editType = "delete"
			}
			filePath := maybeNextFilePath
			if filePath == "" {
				filePath = lastFilePath
			} else {
				lastFilePath = maybeNextFilePath
			}
			block = &EditBlock{FilePath: filePath, EditType: editType, SequenceNumber: sequenceNumber}
			// Reset sequence number after creating a new block
			sequenceNumber = 0
			oldLines = &block.OldLines
			newLines = nil
			blocks = append(blocks, block)
		} else if strings.HasPrefix(line, "=======") {
			oldLines = nil
			newLines = &block.NewLines
		} else if strings.HasPrefix(line, ">>>>>>>") {
			newLines = nil
			oldLines = nil
		} else {
			// It's not a conflict marker line...
			// Check for a sequence number line above the file path
			if oldLines == nil && newLines == nil {
				if strings.HasPrefix(line, "edit_block:") {
					// Parse the sequence number from the line
					parts := strings.Split(line, ":")
					if len(parts) == 2 {
						numStr := strings.TrimSpace(parts[1])
						if val, err := strconv.Atoi(numStr); err == nil {
							sequenceNumber = val
						}
					}
				} else {
					// This handles the case where multiple edits are in the same file without a file name provided for each edit
					maybeNextFilePath = line // read a file path when no active OLD LINES section is in process
				}
			} else {
				// add line to the one of sections of the current block
				if oldLines != nil {
					*oldLines = append(*oldLines, line)
				} else if newLines != nil {
					*newLines = append(*newLines, line)
				}
			}
		}
	}

	for _, block := range blocks {
		if (block.EditType == "append" || block.EditType == "create") && len(block.NewLines) == 0 && len(block.OldLines) > 0 {
			// infer a missing divider, we'll parse this generously as adding new lines
			block.NewLines = block.OldLines
			block.OldLines = nil
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return blocks, nil
}

func ExtractEditBlocksWithVisibility(text string, chatHistory []llm.ChatMessage) ([]EditBlock, error) {
	editBlocksWithoutVisibility, err := ExtractEditBlocks(text)
	if err != nil {
		return nil, err
	}

	var extractedEditBlocks []EditBlock
	visibleCodeBlocks := extractAllCodeBlocks(chatHistory)
	for _, block := range editBlocksWithoutVisibility {
		// these file ranges visible now, but might not be later after we
		// ManageChatHistory, so we need to track visibility right now, at
		// the point the edit block is first authored. We also track it per
		// Remove GetRepoConfig as it is already set
		// visibility
		block.VisibleCodeBlocks = utils.Filter(visibleCodeBlocks, func(cb tree_sitter.CodeBlock) bool {
			return cb.FilePath == block.FilePath
		})
		block.VisibleFileRanges = codeBlocksToMergedFileRanges(block.FilePath, visibleCodeBlocks)

		// TODO /gen/req add one more visible code block (won't have
		// corresponding visible file range) that is based all on the
		// content in the first message, so if the first message has code in
		// it, we can use that code directly. We'll still force the LLM to
		// look up the file, but the error will say that nothing matches in
		// the file, vs it not being in the chat context (which it is)

		extractedEditBlocks = append(extractedEditBlocks, *block)
	}

	return extractedEditBlocks, nil
}
