package dev

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sidekick/coding"
	"sidekick/common"
	"sidekick/env"
	"sidekick/llm"
	"sidekick/llm2"
	"sidekick/persisted_ai"
	"sidekick/utils"
	"slices"
	"strings"

	"github.com/invopop/jsonschema"
	"go.temporal.io/sdk/workflow"
)

type ReadFileActivityInput struct {
	FilePath     string
	LineNumber   int
	WindowSize   int
	AllowAnyPath bool
}

// validateFilePath rejects paths that would escape the base directory via traversal.
func validateFilePath(filePath string, allowAnyPath bool) error {
	if allowAnyPath {
		return nil
	}
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

func readFileLinesFromBytes(data []byte, relFilePath string, lineNumber, windowSize int) (string, error) {
	lang := utils.InferLanguageNameFromFilePath(relFilePath)

	scanner := bufio.NewScanner(bytes.NewReader(data))
	var lines []string
	lineNum := 1
	start := lineNumber - windowSize
	end := lineNumber + windowSize
	firstLineNumber := max(start, 1)
	lastLineNumber := 0
	for scanner.Scan() {
		line := scanner.Text()
		if lineNum >= start && lineNum <= end {
			lines = append(lines, line)
			lastLineNumber = lineNum
		}
		lineNum++
		if lineNum > end {
			break
		}
	}

	if len(lines) == 0 {
		return "", fmt.Errorf("no lines found in the specified window")
	}

	return fmt.Sprintf(
		"File: %s\nLines: %d-%d\n%s%s\n```",
		relFilePath,
		firstLineNumber,
		lastLineNumber,
		coding.CodeFenceStartForLanguage(lang),
		strings.Join(lines, "\n"),
	), nil
}

func ReadFileActivity(baseDir string, readFileParams ReadFileActivityInput) (string, error) {
	if readFileParams.LineNumber < 1 || readFileParams.WindowSize < 0 {
		return "", fmt.Errorf("line number must be greater than 0 and window size must be at least 0")
	}

	if err := validateFilePath(readFileParams.FilePath, readFileParams.AllowAnyPath); err != nil {
		return "", err
	}

	filePath := path.Join(baseDir, readFileParams.FilePath)
	if readFileParams.AllowAnyPath && filepath.IsAbs(readFileParams.FilePath) {
		filePath = readFileParams.FilePath
	}
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
	FileLines    []FileLine `json:"file_lines"`
	WindowSize   int        `json:"window_size"`
	AllowAnyPath bool       `json:"allow_any_path,omitempty"`
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

		if err := validateFilePath(fileLine.FilePath, params.AllowAnyPath); err != nil {
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
		if params.AllowAnyPath && filepath.IsAbs(filePath) {
			fullFilePath = filePath
		}
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

func bulkReadFileMerged(dCtx DevContext, params BulkReadFileParams) (string, error) {
	if len(params.FileLines) == 0 {
		return "", llm.ErrToolCallUnmarshal
	}

	envContainer := dCtx.EnvContainer
	params.AllowAnyPath = envContainer.Env.GetType() == env.EnvTypeDevPod

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

// BulkReadFileActivities holds dependencies for persisted bulk file reading.
type BulkReadFileActivities struct {
	Storage common.KeyValueStorage
}

// BulkReadFileActivityV2Input is the input for the ref-returning bulk read activity.
type BulkReadFileActivityV2Input struct {
	EnvContainer env.EnvContainer   `json:"envContainer"`
	Params       BulkReadFileParams `json:"params"`
	FlowId       string             `json:"flowId"`
	ToolCall     llm.ToolCall       `json:"toolCall"`
	WorkspaceId  string             `json:"workspaceId"`
}

// BulkReadFileActivityV2Output contains the persisted message ref.
type BulkReadFileActivityV2Output struct {
	Ref persisted_ai.MessageRef `json:"ref"`
}

// BulkReadFileActivityV2 reads files via Env.ReadFile, formats the output,
// persists it to KV storage, and returns a MessageRef.
func (a *BulkReadFileActivities) BulkReadFileActivityV2(ctx context.Context, input BulkReadFileActivityV2Input) (*BulkReadFileActivityV2Output, error) {
	params := input.Params
	if params.WindowSize < 0 {
		return nil, fmt.Errorf("window size must be at least 0")
	}

	fileGroups := make(map[string][]FileLine)
	fileOrder := make([]string, 0)
	seenFiles := make(map[string]bool)

	for _, fileLine := range params.FileLines {
		if fileLine.LineNumber < 1 {
			return nil, fmt.Errorf("line number must be greater than 0")
		}
		if err := validateFilePath(fileLine.FilePath, params.AllowAnyPath); err != nil {
			return nil, err
		}
		if !seenFiles[fileLine.FilePath] {
			fileOrder = append(fileOrder, fileLine.FilePath)
			seenFiles[fileLine.FilePath] = true
		}
		fileGroups[fileLine.FilePath] = append(fileGroups[fileLine.FilePath], fileLine)
	}

	var parts []string
	for _, filePath := range fileOrder {
		fileLines := fileGroups[filePath]

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

		slices.SortFunc(intervals, func(a, b interval) int {
			if a.start != b.start {
				return a.start - b.start
			}
			return a.end - b.end
		})

		var merged []interval
		for _, curr := range intervals {
			if len(merged) == 0 || curr.start > merged[len(merged)-1].end+1 {
				merged = append(merged, curr)
			} else {
				merged[len(merged)-1].end = max(merged[len(merged)-1].end, curr.end)
			}
		}

		data, readErr := input.EnvContainer.Env.ReadFile(ctx, filePath)
		if readErr != nil {
			if os.IsNotExist(readErr) {
				parts = append(parts, fmt.Sprintf("failed to read the file: no file exists at the given file path: %s", filePath))
			} else {
				parts = append(parts, fmt.Sprintf("failed to read the file: failed to open file: %v", readErr))
			}
			continue
		}

		scanner := bufio.NewScanner(bytes.NewReader(data))
		var allLines []string
		for scanner.Scan() {
			allLines = append(allLines, scanner.Text())
		}

		fileLength := len(allLines)
		lang := utils.InferLanguageNameFromFilePath(filePath)

		hasValidBlocks := false
		for _, mergedInterval := range merged {
			actualEnd := min(mergedInterval.end, fileLength)
			if mergedInterval.start > fileLength {
				continue
			}
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
				parts = append(parts, formatCodeBlock(MergedCodeBlock{
					FilePath:  filePath,
					StartLine: mergedInterval.start,
					EndLine:   min(actualEnd, fileLength),
					Language:  lang,
					Content:   strings.Join(extractedLines, "\n"),
				}))
			}
		}

		if !hasValidBlocks {
			parts = append(parts, "failed to read the file: no lines found in the specified window")
		}
	}

	content := strings.Join(parts, "\n\n")

	textBlock := llm2.ContentBlock{Type: llm2.ContentBlockTypeText, Text: content}
	trb := llm2.ToolResultBlock{
		ToolCallId: input.ToolCall.Id,
		Name:       input.ToolCall.Name,
		Content:    []llm2.ContentBlock{textBlock},
	}
	finalBlock := llm2.ContentBlock{
		Type:       llm2.ContentBlockTypeToolResult,
		ToolResult: &trb,
	}

	ref, err := persisted_ai.PersistContentBlock(
		ctx, a.Storage, input.FlowId, input.WorkspaceId, string(llm2.RoleUser), finalBlock,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to persist content block: %w", err)
	}

	return &BulkReadFileActivityV2Output{Ref: *ref}, nil
}

// BulkReadFileV2 reads files via Env and persists the result to KV, returning a ref.
func BulkReadFileV2(dCtx DevContext, params BulkReadFileParams, toolCall llm.ToolCall) (*BulkReadFileActivityV2Output, error) {
	if len(params.FileLines) == 0 {
		return nil, llm.ErrToolCallUnmarshal
	}

	envContainer := dCtx.EnvContainer
	params.AllowAnyPath = envContainer.Env.GetType() == env.EnvTypeDevPod
	flowId := workflow.GetInfo(dCtx).WorkflowExecution.ID

	var bra *BulkReadFileActivities
	var output BulkReadFileActivityV2Output
	err := workflow.ExecuteActivity(dCtx, bra.BulkReadFileActivityV2, BulkReadFileActivityV2Input{
		EnvContainer: *envContainer,
		Params:       params,
		FlowId:       flowId,
		ToolCall:     toolCall,
		WorkspaceId:  dCtx.WorkspaceId,
	}).Get(dCtx, &output)
	if err != nil {
		return nil, fmt.Errorf("failed to execute bulk read file activity: %v", err)
	}
	return &output, nil
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
		allowAnyPath := envContainer.Env.GetType() == env.EnvTypeDevPod

		var results []string

		for _, fileLine := range bulkReadFileParams.FileLines {
			readFileParams := ReadFileActivityInput{
				FilePath:     fileLine.FilePath,
				LineNumber:   fileLine.LineNumber,
				WindowSize:   bulkReadFileParams.WindowSize,
				AllowAnyPath: allowAnyPath,
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
		return bulkReadFileMerged(dCtx, bulkReadFileParams)
	}
}
