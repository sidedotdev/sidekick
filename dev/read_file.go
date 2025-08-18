package dev

import (
	"bufio"
	"fmt"
	"os"
	"path"
	"sidekick/coding"
	"sidekick/llm"
	"sidekick/utils"
	"strings"

	"github.com/invopop/jsonschema"
	"go.temporal.io/sdk/workflow"
)

type ReadFileActivityInput struct {
	FilePath   string
	LineNumber int
	WindowSize int
}

func ReadFileActivity(baseDir string, readFileParams ReadFileActivityInput) (string, error) {
	if readFileParams.LineNumber < 1 || readFileParams.WindowSize < 0 {
		return "", fmt.Errorf("line number must be greater than 0 and window size must be at least 0")
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

var bulkReadFileTool = llm.Tool{
	Name:        "read_file_lines",
	Description: "Read files from the repo given specific line numbers and a window for additional context. Most useful for debugging errors that mention a line number, or for retrieving context that you can't see another way. Do not use for reading code, retrieve_code_context should be favored for that.",
	Parameters:  (&jsonschema.Reflector{DoNotReference: true}).Reflect(&BulkReadFileParams{}),
}

// TODO add tests for bulk read file, with mock read file activity
func BulkReadFile(dCtx DevContext, bulkReadFileParams BulkReadFileParams) (string, error) {
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
}
