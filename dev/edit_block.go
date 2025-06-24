package dev

import (
	"bufio"
	"sidekick/coding/tree_sitter"
	"sidekick/llm"
	"sidekick/utils"
	"slices"
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
	// file ranges that were visible when this edit block was created
	VisibleCodeBlocks []tree_sitter.CodeBlock `json:"visibleCodeBlocks"`
}

type FileRange struct {
	FilePath  string `json:"filePath"`
	StartLine int    `json:"startLine"`
	EndLine   int    `json:"endLine"`
}

// TODO: this doesn't handle edit blocks that were applied successfully, where
// the new lines should be returned as a code block
func extractAllCodeBlocks(chatHistory []llm.ChatMessage, shouldExtractEditBlocks bool) []tree_sitter.CodeBlock {
	codeBlocks := make([]tree_sitter.CodeBlock, 0)
	for _, chatMessage := range chatHistory {
		// Existing logic for symbol and search blocks from non-assistant messages
		if chatMessage.Role != llm.ChatMessageRoleAssistant {
			symDefCodeBlocks := tree_sitter.ExtractSymbolDefinitionBlocks(chatMessage.Content)
			codeBlocks = append(codeBlocks, symDefCodeBlocks...)
			searchCodeBlocks := tree_sitter.ExtractSearchCodeBlocks(chatMessage.Content)
			codeBlocks = append(codeBlocks, searchCodeBlocks...)
		}

		if shouldExtractEditBlocks {
			editBlocks, err := ExtractEditBlocks(chatMessage.Content)
			if err == nil {
				for _, editBlock := range editBlocks {
					if len(editBlock.OldLines) > 0 {
						code := strings.Join(editBlock.OldLines, "\n")
						syntheticBlock := tree_sitter.CodeBlock{
							Code:          code,
							BlockContent:  "```\n" + code + "\n```",
							FullContent:   "```\n" + code + "\n```",
							FilePath:      editBlock.FilePath,
							StartLine:     -1, // Mark as synthetic
							EndLine:       -1, // Mark as synthetic
							HeaderContent: "",
							Symbol:        "",
						}
						codeBlocks = append(codeBlocks, syntheticBlock)
					}
					if len(editBlock.NewLines) > 0 {
						code := strings.Join(editBlock.NewLines, "\n")
						syntheticBlock := tree_sitter.CodeBlock{
							Code:          code,
							BlockContent:  "```\n" + code + "\n```",
							FullContent:   "```\n" + code + "\n```",
							FilePath:      editBlock.FilePath,
							StartLine:     -1, // Mark as synthetic
							EndLine:       -1, // Mark as synthetic
							HeaderContent: "",
							Symbol:        "",
						}
						codeBlocks = append(codeBlocks, syntheticBlock)
					}
				}
			}
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
						sequenceNumber, _ = strconv.Atoi(parts[1])
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

func ExtractEditBlocksWithVisibility(text string, chatHistory []llm.ChatMessage, shouldExtractEditBlocks bool) ([]EditBlock, error) {
	editBlocksWithoutVisibility, err := ExtractEditBlocks(text)
	if err != nil {
		return nil, err
	}

	// chatHistoryCodeBlocks contains all code blocks from the chat history.
	// If shouldExtractEditBlocks is true, this will include synthetic ones from edit blocks in prior messages.
	chatHistoryCodeBlocks := extractAllCodeBlocks(chatHistory, shouldExtractEditBlocks)

	// runningPrefixCodeBlocks will accumulate synthetic code blocks from the OldLines/NewLines
	// of edit blocks processed so far within the current 'text', if enabled.
	runningPrefixCodeBlocks := make([]tree_sitter.CodeBlock, 0)

	// extractedEditBlocks will store the processed edit blocks with their visibility information.
	extractedEditBlocks := make([]EditBlock, 0)

	for _, editBlock := range editBlocksWithoutVisibility { // editBlock is *EditBlock
		var availableCodeBlocks []tree_sitter.CodeBlock
		if shouldExtractEditBlocks {
			// For the current editBlock, its VisibleCodeBlocks should include:
			// 1. All code blocks from the chat history (chatHistoryCodeBlocks, which may include synthetic blocks from history).
			// 2. Synthetic code blocks from OldLines/NewLines of preceding edit blocks in the *current* message (runningPrefixCodeBlocks).
			availableCodeBlocks = append(slices.Clone(chatHistoryCodeBlocks), runningPrefixCodeBlocks...)
		} else {
			// Only use code blocks from chat history (which won't include synthetic blocks from history if the flag was false for extractAllCodeBlocks).
			// Progressive rendering from current text is also disabled.
			availableCodeBlocks = slices.Clone(chatHistoryCodeBlocks)
		}

		editBlock.VisibleCodeBlocks = utils.Filter(availableCodeBlocks, func(cb tree_sitter.CodeBlock) bool {
			return cb.FilePath == editBlock.FilePath
		})

		// VisibleFileRanges are based ONLY on \"real\" code blocks from chat history.
		// They should NOT be affected by synthetic blocks generated from the current \'text\' or from chat history.
		// This filtering ensures that even if chatHistoryCodeBlocks contains synthetic blocks (because shouldExtractEditBlocks is true),
		// they are excluded for VisibleFileRanges calculation.
		chatHistoryCodeBlocks := utils.Filter(chatHistoryCodeBlocks, func(cb tree_sitter.CodeBlock) bool {
			return cb.StartLine != -1 && cb.EndLine != -1 // Filter out synthetic blocks
		})
		editBlock.VisibleFileRanges = codeBlocksToMergedFileRanges(editBlock.FilePath, chatHistoryCodeBlocks)

		// TODO /gen/req add one more visible code block (won\'t have
		// corresponding visible file range) that is based all on the
		// content in the first message, so if the first message has code in
		// it, we can use that code directly. We\'ll still force the LLM to
		// look up the file, but the error will say that nothing matches in
		// the file, vs it not being in the chat context (which it is)

		extractedEditBlocks = append(extractedEditBlocks, *editBlock) // Append a copy of the modified block.

		if shouldExtractEditBlocks {
			// After the current block's visibility is determined and it's added to extractedEditBlocks,
			// generate synthetic code blocks from its OldLines and NewLines if extracing edit blocks is enabled.
			// These will be available to subsequent edit blocks within the same 'text'.
			if len(editBlock.OldLines) > 0 {
				code := strings.Join(editBlock.OldLines, "\n")
				syntheticOldCb := tree_sitter.CodeBlock{
					Code:          code,
					BlockContent:  "```\n" + code + "\n```",
					FullContent:   "```\n" + code + "\n```",
					FilePath:      editBlock.FilePath,
					StartLine:     -1, // Synthetic blocks use -1 for line numbers
					EndLine:       -1,
					HeaderContent: "",
					Symbol:        "",
				}
				runningPrefixCodeBlocks = append(runningPrefixCodeBlocks, syntheticOldCb)
			}
			if len(editBlock.NewLines) > 0 {
				code := strings.Join(editBlock.NewLines, "\n")
				syntheticNewCb := tree_sitter.CodeBlock{
					Code:          code,
					BlockContent:  "```\n" + code + "\n```",
					FullContent:   "```\n" + code + "\n```",
					FilePath:      editBlock.FilePath,
					StartLine:     -1, // Synthetic blocks use -1 for line numbers
					EndLine:       -1,
					HeaderContent: "",
					Symbol:        "",
				}
				runningPrefixCodeBlocks = append(runningPrefixCodeBlocks, syntheticNewCb)
			}
		}
	}

	return extractedEditBlocks, nil
}
