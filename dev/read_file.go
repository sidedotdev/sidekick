package dev

import (
	"bufio"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sidekick/coding"
	"sidekick/llm"
	"sidekick/utils"
	"slices"
	"strings"

	"github.com/invopop/jsonschema"
	"go.temporal.io/sdk/workflow"
)

type ReadFileActivityInput struct {
	FilePath   string
	LineNumber int
	WindowSize int
}

// validateFilePath rejects paths that would escape the base directory via traversal.
func validateFilePath(filePath string) error {
	if filepath.IsAbs(filePath) {
		return fmt.Errorf("file path must be relative, got: %s", filePath)
	}
	for _, segment := range strings.Split(filepath.ToSlash(filePath), "/") {
		if segment == ".." {
			return fmt.Errorf("file path must not contain parent directory references: %s", filePath)
		}
	}
	return nil
}

func ReadFileActivity(baseDir string, readFileParams ReadFileActivityInput) (string, error) {
	if readFileParams.LineNumber < 1 || readFileParams.WindowSize < 0 {
		return "", fmt.Errorf("line number must be greater than 0 and window size must be at least 0")
	}

	if err := validateFilePath(readFileParams.FilePath); err != nil {
		return "", err
	}

	filePath := path.Join(baseDir, readFileParams.FilePath)
	lang := utils.InferLanguageNameFromFilePath(filePath)

	file, err := os.Open(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("no file exists at the given file path: %s", readFileParams.FilePath)
		} else {
			return "", fmt.Errorf("failed to open file: %v", err)
		}
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	var lines []string
	lineNumber := 1
	start := readFileParams.LineNumber - readFileParams.WindowSize
	end := readFileParams.LineNumber + readFileParams.WindowSize
	firstLineNumber := max(start, 1)
	lastLineNumber := 0
	for scanner.Scan() {
		line := scanner.Text()
		if lineNumber >= start && lineNumber <= end {
			lines = append(lines, line)
			lastLineNumber = lineNumber
		}
		lineNumber++
		if lineNumber > end {
			break
		}
	}

	if len(lines) == 0 {
		return "", fmt.Errorf("no lines found in the specified window")
	}

	formattedOutput := fmt.Sprintf(
		"File: %s\nLines: %d-%d\n%s%s\n```",
		readFileParams.FilePath,
		firstLineNumber,
		lastLineNumber,
		coding.CodeFenceStartForLanguage(lang),
		strings.Join(lines, "\n"),
	)

	return formattedOutput, nil
}

type FileLine struct {
	FilePath   string `json:"file_path" jsonschema:"description=The file path to read from."`
	LineNumber int    `json:"line_number" jsonschema:"description=The line number to center the window around."`
}

type BulkReadFileParams struct {
	FileLines  []FileLine `json:"file_lines"`
	WindowSize int        `json:"window_size"`
}

type MergedCodeBlock struct {
	FilePath  string `json:"filePath"`
	StartLine int    `json:"startLine"`
	EndLine   int    `json:"endLine"`
	Language  string `json:"language"`
	Content   string `json:"content"`
}

type BulkReadFileActivityResult struct {
	CodeBlocks []MergedCodeBlock `json:"codeBlocks"`
	Errors     []string          `json:"errors"`
}

var bulkReadFileTool = llm.Tool{
	Name:        "read_file_lines",
	Description: "Read files from the repo given specific line numbers and a window for additional context. Most useful for debugging errors that mention a line number, or for retrieving context that you can't see another way. Do not use for reading code, get_symbol_definitions should be favored for that.",
	Parameters:  (&jsonschema.Reflector{DoNotReference: true}).Reflect(&BulkReadFileParams{}),
}

func BulkReadFileActivity(baseDir string, params BulkReadFileParams) (BulkReadFileActivityResult, error) {
	if params.WindowSize < 0 {
		return BulkReadFileActivityResult{}, fmt.Errorf("window size must be at least 0")
	}

	// Group file lines by file path
	fileGroups := make(map[string][]FileLine)
	fileOrder := make([]string, 0)
	seenFiles := make(map[string]bool)

	for _, fileLine := range params.FileLines {
		if fileLine.LineNumber < 1 {
			return BulkReadFileActivityResult{}, fmt.Errorf("line number must be greater than 0")
		}

		if err := validateFilePath(fileLine.FilePath); err != nil {
			return BulkReadFileActivityResult{}, err
		}

		if !seenFiles[fileLine.FilePath] {
			fileOrder = append(fileOrder, fileLine.FilePath)
			seenFiles[fileLine.FilePath] = true
		}
		fileGroups[fileLine.FilePath] = append(fileGroups[fileLine.FilePath], fileLine)
	}

	var codeBlocks []MergedCodeBlock
	var errors []string

	for _, filePath := range fileOrder {
		fileLines := fileGroups[filePath]

		// Build intervals for this file
		type interval struct {
			start int
			end   int
		}

		var intervals []interval
		for _, fileLine := range fileLines {
			start := max(fileLine.LineNumber-params.WindowSize, 1)
			end := fileLine.LineNumber + params.WindowSize
			intervals = append(intervals, interval{start: start, end: end})
		}

		// Sort intervals by start, then by end
		slices.SortFunc(intervals, func(a, b interval) int {
			if a.start != b.start {
				return a.start - b.start
			}
			return a.end - b.end
		})

		// Merge overlapping or adjacent intervals
		var merged []interval
		for _, curr := range intervals {
			if len(merged) == 0 || curr.start > merged[len(merged)-1].end+1 {
				merged = append(merged, curr)
			} else {
				// Merge with the last interval
				merged[len(merged)-1].end = max(merged[len(merged)-1].end, curr.end)
			}
		}

		// Read the file and extract content for each merged interval
		fullFilePath := path.Join(baseDir, filePath)
		file, err := os.Open(fullFilePath)
		if err != nil {
			if os.IsNotExist(err) {
				errors = append(errors, fmt.Sprintf("failed to read the file: no file exists at the given file path: %s", filePath))
			} else {
				errors = append(errors, fmt.Sprintf("failed to read the file: failed to open file: %v", err))
			}
			continue
		}

		// Read all lines from the file
		scanner := bufio.NewScanner(file)
		var allLines []string
		for scanner.Scan() {
			allLines = append(allLines, scanner.Text())
		}
		file.Close()

		if err := scanner.Err(); err != nil {
			errors = append(errors, fmt.Sprintf("failed to read the file: error reading file: %v", err))
			continue
		}

		fileLength := len(allLines)
		lang := utils.InferLanguageNameFromFilePath(fullFilePath)

		// Extract content for each merged interval
		hasValidBlocks := false
		for _, mergedInterval := range merged {
			// Clamp end to file length
			actualEnd := min(mergedInterval.end, fileLength)

			// Skip if the interval is completely out of range
			if mergedInterval.start > fileLength {
				continue
			}

			// Extract lines (convert to 0-based indexing)
			startIdx := mergedInterval.start - 1
			endIdx := actualEnd - 1

			if startIdx < 0 || startIdx >= fileLength {
				continue
			}

			var extractedLines []string
			for i := startIdx; i <= endIdx && i < fileLength; i++ {
				extractedLines = append(extractedLines, allLines[i])
			}

			if len(extractedLines) > 0 {
				hasValidBlocks = true
				codeBlocks = append(codeBlocks, MergedCodeBlock{
					FilePath:  filePath,
					StartLine: mergedInterval.start,
					EndLine:   min(actualEnd, fileLength),
					Language:  lang,
					Content:   strings.Join(extractedLines, "\n"),
				})
			}
		}

		// If no valid blocks were found for this file, add an error
		if !hasValidBlocks {
			errors = append(errors, fmt.Sprintf("failed to read the file: no lines found in the specified window"))
		}
	}

	return BulkReadFileActivityResult{
		CodeBlocks: codeBlocks,
		Errors:     errors,
	}, nil
}

func formatCodeBlock(block MergedCodeBlock) string {
	return fmt.Sprintf(
		"File: %s\nLines: %d-%d\n%s%s\n```",
		block.FilePath,
		block.StartLine,
		block.EndLine,
		coding.CodeFenceStartForLanguage(block.Language),
		block.Content,
	)
}

func BulkReadFileV2(dCtx DevContext, params BulkReadFileParams) (string, error) {
	if len(params.FileLines) == 0 {
		return "", llm.ErrToolCallUnmarshal
	}

	envContainer := dCtx.EnvContainer
	var result BulkReadFileActivityResult
	err := workflow.ExecuteActivity(dCtx, BulkReadFileActivity, envContainer.Env.GetWorkingDirectory(), params).Get(dCtx, &result)
	if err != nil {
		return "", fmt.Errorf("failed to execute bulk read file activity: %v", err)
	}

	var results []string

	// Add formatted code blocks
	for _, block := range result.CodeBlocks {
		results = append(results, formatCodeBlock(block))
	}

	// Add errors
	for _, errorMsg := range result.Errors {
		results = append(results, errorMsg)
	}

	return strings.Join(results, "\n\n"), nil
}

// TODO add tests for bulk read file, with mock read file activity
func BulkReadFile(dCtx DevContext, bulkReadFileParams BulkReadFileParams) (string, error) {
	if len(bulkReadFileParams.FileLines) == 0 {
		return "", llm.ErrToolCallUnmarshal
	}

	// Use workflow versioning to gate the new behavior
	version := workflow.GetVersion(dCtx, "read_file_lines_merge_adjacent_v2", workflow.DefaultVersion, 1)

	if version == workflow.DefaultVersion {
		// Original behavior: execute ReadFileActivity for each request
		envContainer := dCtx.EnvContainer
		var results []string

		for _, fileLine := range bulkReadFileParams.FileLines {
			readFileParams := ReadFileActivityInput{
				FilePath:   fileLine.FilePath,
				LineNumber: fileLine.LineNumber,
				WindowSize: bulkReadFileParams.WindowSize,
			}
			var result string
			err := workflow.ExecuteActivity(dCtx, ReadFileActivity, envContainer.Env.GetWorkingDirectory(), readFileParams).Get(dCtx, &result)
			if err != nil {
				// TODO strip the "activity error (type: ...) from the error message" before returning it upstream
				errorMsg := fmt.Sprintf("failed to read the file: %v", err)
				results = append(results, errorMsg)
			} else {
				results = append(results, result)
			}
		}
		return strings.Join(results, "\n\n"), nil
	} else {
		// New behavior: use BulkReadFileV2 with merging
		return BulkReadFileV2(dCtx, bulkReadFileParams)
	}
}
