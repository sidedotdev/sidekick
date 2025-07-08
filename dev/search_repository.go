package dev

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sidekick/common"
	"sidekick/env"
	"sidekick/utils"
	"strings"

	tree_sitter "sidekick/coding/tree_sitter"

	doublestar "github.com/bmatcuk/doublestar/v4"
	"go.temporal.io/sdk/workflow"
)

// escapeShellArg escapes a string for safe use as an argument in shell commands.
// It wraps the string in single quotes and escapes any single quotes within the string.
func escapeShellArg(arg string) string {
	// Replace single quotes with '\'' (end quote, escaped quote, start quote)
	escaped := strings.ReplaceAll(arg, "'", `'\''`)
	// Wrap in single quotes
	return "'" + escaped + "'"
}

// isSpecificPathGlob returns true if the glob pattern targets specific paths rather than matching everything.
func isSpecificPathGlob(pattern string) bool {
	matchAllPatterns := []string{"", "*", "**", "**/*"}
	for _, p := range matchAllPatterns {
		if pattern == p {
			return false
		}
	}
	return true
}

// filterFilesByGlob filters a list of files using the given glob pattern.
// It tries matching against both the full path and the basename.
func filterFilesByGlob(files []string, globPattern string) ([]string, error) {
	var filteredFiles []string
	for _, file := range files {
		if file == "" {
			continue
		}
		// Try matching against the full path first
		matched, err := doublestar.PathMatch(globPattern, file)
		if err != nil {
			return nil, fmt.Errorf("invalid glob pattern %s: %v", globPattern, err)
		}
		// If full path doesn't match, try matching against just the basename
		if !matched {
			base := filepath.Base(file)
			matched, err = doublestar.PathMatch(globPattern, base)
			if err != nil {
				return nil, fmt.Errorf("invalid glob pattern %s (when matching basename %s): %v", globPattern, base, err)
			}
		}
		if matched {
			filteredFiles = append(filteredFiles, file)
		}
	}
	return filteredFiles, nil
}

type SingleSearchParams struct {
	PathGlob   string `json:"path_glob" jsonschema:"description=The file glob path to search within."`
	SearchTerm string `json:"search_term" jsonschema:"description=The search term to look for within the files."`
}

const refuseAtSearchOutputLength = 6000
const maxSearchOutputLength = 2000

// addSearchPrefix adds a consistent search description prefix to the given output
func addSearchPrefix(input SearchRepositoryInput, output string) string {
	prefix := fmt.Sprintf("Searched for %q in %q", input.SearchTerm, input.PathGlob)
	return fmt.Sprintf("%s\n%s", prefix, output)
}

type SearchRepositoryInput struct {
	PathGlob        string
	SearchTerm      string
	ContextLines    int
	CaseInsensitive bool
	FixedStrings    bool
}

// TODO /gen include the function name in the associated with each search result

type searchContext struct {
	ctx                    workflow.Context
	envContainer           env.EnvContainer
	input                  SearchRepositoryInput
	coreIgnorePath         string
	rgArgs                 string
	gitGrepArgs            string
	escapedSearchTerm      string
	useManualGlobFiltering bool
	sideIgnoreExists       bool
}

func initSearchContext(ctx workflow.Context, envContainer env.EnvContainer, input SearchRepositoryInput) (*searchContext, error) {
	coreIgnorePath, err := getOrCreateCoreIgnoreFile()
	if err != nil {
		return nil, fmt.Errorf("failed to get core ignore file: %w", err)
	}

	sCtx := &searchContext{
		ctx:            ctx,
		envContainer:   envContainer,
		input:          input,
		coreIgnorePath: coreIgnorePath,
	}

	sCtx.escapedSearchTerm = escapeShellArg(input.SearchTerm)

	// Base rgArgs
	sCtx.rgArgs = "--files-with-matches --hidden --ignore-file " + escapeShellArg(sCtx.coreIgnorePath)

	// Base gitGrepArgs
	sCtx.gitGrepArgs = fmt.Sprintf("git grep --no-index --show-function --heading --line-number --context %d", input.ContextLines)

	if input.CaseInsensitive {
		sCtx.rgArgs += " --ignore-case"
		sCtx.gitGrepArgs += " --ignore-case"
	}
	if input.FixedStrings {
		sCtx.rgArgs += " --fixed-strings"
		sCtx.gitGrepArgs += " --fixed-strings"
	}

	// Determine if manual glob filtering should be used
	// Version guard for manual glob filtering logic
	v := workflow.GetVersion(sCtx.ctx, "manual-search-glob-filtering", workflow.DefaultVersion, 1)
	sCtx.useManualGlobFiltering = isSpecificPathGlob(sCtx.input.PathGlob) && v >= 1

	// Check for .sideignore file
	var catOutput env.EnvRunCommandOutput
	// TODO /gen replace with a new env.FileExistsActivity - we need to implement that. (This comment is from original code, moved here with the logic)
	err = workflow.ExecuteActivity(sCtx.ctx, env.EnvRunCommandActivity, env.EnvRunCommandActivityInput{
		EnvContainer:       sCtx.envContainer,
		RelativeWorkingDir: "./",
		Command:            "cat",
		Args:               []string{".sideignore"},
	}).Get(sCtx.ctx, &catOutput)
	if err != nil {
		// This error means the activity execution failed (e.g., worker unavailable, panic in activity)
		return nil, fmt.Errorf("failed to execute command to check for .sideignore file: %w", err)
	}

	sCtx.sideIgnoreExists = false
	if catOutput.ExitStatus == 0 { // cat was successful, so .sideignore exists
		sCtx.rgArgs += " --ignore-file .sideignore"
		sCtx.sideIgnoreExists = true
	}
	// If catOutput.ExitStatus != 0 (e.g. file not found), we proceed without adding .sideignore to rgArgs, which is the desired behavior.

	return sCtx, nil
}

// by processing the search results further through tree-sitter (note: This
// requires StructuredSearchRepository to be implemented first.) The tree-sitter
// stuff might be done simply if we find the smallest symbol definition that
// contains the matched rows from search result, and use that to get the first
// line of the function or type definition, and include it if it isn't already
// included. The idea behind this is to improve upon the git grep
// --show-function output, as we'll have better control and accuracy with
// tree-sitter

/*
TODO /gen created a function called StructuredSearchRepository. It calls
SearchRepository, parses the results from SearchRepository, and returns a slice
of SearchResult structs, each with a Path, Content, StartRow, EndRow and
MatchedRows fields.  MatchedRows is a slice with row numbers for each slice. Row
number is 0-indexed line number. The output from rg looks like this:

path/to/file.go
13-Some
14-Context
15-Lines
16:A matching line
17-More
18-
19-Context
20-
21-Lines
22:Another match
23-
24-blah
25-blah
26-blah
--
77-the above line denotes a break in the range of matched lines, but we're still in the same file
78-
79:a match, you get the drill
80-
81-

another/file.go
8-
9-yada
10-yada
11:MATCH
12-foo
13-bar
14-baz
*/

// TODO /gen use cs command to search the repository instead of just rg as a fallback at least given the "*"/"" path glob

// TODO /gen support "-F" flag for fixed string search in rg, and use it if the search term is a fixed string

func getOrCreateCoreIgnoreFile() (string, error) {
	configDir := common.GetSidekickConfigDir()
	coreIgnorePath := filepath.Join(configDir, "core_ignore")

	// Check if file exists
	if _, err := os.Stat(coreIgnorePath); os.IsNotExist(err) {
		// Create config directory if it doesn't exist
		if err := os.MkdirAll(configDir, 0755); err != nil {
			return "", fmt.Errorf("failed to create config directory: %v", err)
		}

		// Create core_ignore file with .git exclusion
		if err := os.WriteFile(coreIgnorePath, []byte(".git\n"), 0644); err != nil {
			return "", fmt.Errorf("failed to create core_ignore file: %v", err)
		}
	} else if err != nil {
		return "", fmt.Errorf("failed to check core_ignore file: %v", err)
	}

	return coreIgnorePath, nil
}

// returns:
// 1. raw command stdOut
// 2. raw command stdErr
// 3. list of all files that contain search term (ignoring path glob), if useManualGlobFiltering is true
// 4. list of files that also matched the glob too
// 5. error
func (sCtx *searchContext) executeMainSearch() (string, string, []string, []string, error) {
	var searchOutput env.EnvRunCommandOutput
	var listFilesOutput env.EnvRunCommandOutput
	var err error

	if sCtx.useManualGlobFiltering {
		// When using manual glob filtering, we need to:
		// 1. Get the list of files from rg (respecting ignore files) that contain the search term
		// 2. Filter them manually using the glob pattern
		// 3. Run git grep on the filtered files
		listFilesCmd := fmt.Sprintf(`rg %s --files-with-matches -- %s`, sCtx.rgArgs, sCtx.escapedSearchTerm)

		err = workflow.ExecuteActivity(sCtx.ctx, env.EnvRunCommandActivity, env.EnvRunCommandActivityInput{
			EnvContainer:       sCtx.envContainer,
			RelativeWorkingDir: "./",
			Command:            "sh",
			Args:               []string{"-c", listFilesCmd},
		}).Get(sCtx.ctx, &listFilesOutput)
		if err != nil {
			return "", "", nil, nil, fmt.Errorf("failed to list files for manual glob filtering: %w", err)
		}

		var allFilesMatchingSearchTerm []string
		if trimmedStdout := strings.TrimSpace(listFilesOutput.Stdout); trimmedStdout != "" {
			allFilesMatchingSearchTerm = strings.Split(trimmedStdout, "\n")
		} else {
			allFilesMatchingSearchTerm = []string{}
		}

		var filesMatchingGlobAndSearchTerm []string
		if len(allFilesMatchingSearchTerm) > 0 {
			filesMatchingGlobAndSearchTerm, err = filterFilesByGlob(allFilesMatchingSearchTerm, sCtx.input.PathGlob)
			if err != nil {
				// Pass along stderr from listFilesOutput as it might contain relevant info
				return "", listFilesOutput.Stderr, allFilesMatchingSearchTerm, filesMatchingGlobAndSearchTerm, fmt.Errorf("failed to filter files by glob: %w", err)
			}
		}

		if len(filesMatchingGlobAndSearchTerm) == 0 {
			// No files matched the glob after listing.
			// Return empty stdout, but include stderr from the listing command if any.
			return "", listFilesOutput.Stderr, allFilesMatchingSearchTerm, filesMatchingGlobAndSearchTerm, nil
		} else {
			// Run git grep on the filtered files
			escapedFiles := make([]string, len(filesMatchingGlobAndSearchTerm))
			for i, file := range filesMatchingGlobAndSearchTerm {
				escapedFiles[i] = escapeShellArg(file)
			}
			filesArg := strings.Join(escapedFiles, " ")
			fullCmd := fmt.Sprintf(`%s -- %s %s`, sCtx.gitGrepArgs, sCtx.escapedSearchTerm, filesArg)

			// searchOutput will be populated by this activity
			err = workflow.ExecuteActivity(sCtx.ctx, env.EnvRunCommandActivity, env.EnvRunCommandActivityInput{
				EnvContainer:       sCtx.envContainer,
				RelativeWorkingDir: "./",
				Command:            "sh",
				Args:               []string{"-c", fullCmd},
			}).Get(sCtx.ctx, &searchOutput)
			if err != nil {
				return "", searchOutput.Stderr, allFilesMatchingSearchTerm, filesMatchingGlobAndSearchTerm, fmt.Errorf("failed to search filtered files: %w", err)
			}
			// Success, return stdout and stderr from the git grep command
			return searchOutput.Stdout, searchOutput.Stderr, allFilesMatchingSearchTerm, filesMatchingGlobAndSearchTerm, nil
		}
	} else {
		// Original behavior: use rg + git grep pipeline
		fullCmd := fmt.Sprintf(`rg %s -- %s | xargs -r %s -- %s`, sCtx.rgArgs, sCtx.escapedSearchTerm, sCtx.gitGrepArgs, sCtx.escapedSearchTerm)

		err = workflow.ExecuteActivity(sCtx.ctx, env.EnvRunCommandActivity, env.EnvRunCommandActivityInput{
			EnvContainer:       sCtx.envContainer,
			RelativeWorkingDir: "./",
			Command:            "sh",
			Args:               []string{"-c", fullCmd},
		}).Get(sCtx.ctx, &searchOutput)
		if err != nil {
			return "", searchOutput.Stderr, nil, nil, fmt.Errorf("failed to search the repository: %w", err)
		}
		return searchOutput.Stdout, searchOutput.Stderr, nil, nil, nil
	}
}

func (sCtx *searchContext) handleOutputLengthChecks(rawOutput string, globMatchedFiles []string) (string, bool, error) {
	if len(rawOutput) > refuseAtSearchOutputLength {
		var err error
		var filesToListCmd string
		var fileListString string

		if sCtx.useManualGlobFiltering {
			v := workflow.GetVersion(sCtx.ctx, "search-repo-remove-extra-command", workflow.DefaultVersion, 1)
			if v < 1 {
				// fake activity execution just to ensure workflows can be
				// replayed deterministically: we never needed this and did it
				// by mistake
				var listFilesOutput env.EnvRunCommandOutput
				err = workflow.ExecuteActivity(sCtx.ctx, env.EnvRunCommandActivity, env.EnvRunCommandActivityInput{}).Get(sCtx.ctx, &listFilesOutput)
				if err != nil {
					return "", true, fmt.Errorf("failed to list files (for too long output, manual glob): %w", err)
				}
			}

			fileListString = strings.Join(globMatchedFiles, "\n")
		} else {
			var listFilesOutput env.EnvRunCommandOutput
			filesToListCmd = fmt.Sprintf(`rg %s -- %s`, sCtx.rgArgs, sCtx.escapedSearchTerm)
			err = workflow.ExecuteActivity(sCtx.ctx, env.EnvRunCommandActivity, env.EnvRunCommandActivityInput{
				EnvContainer:       sCtx.envContainer,
				RelativeWorkingDir: "./",
				Command:            "sh",
				Args:               []string{"-c", filesToListCmd},
			}).Get(sCtx.ctx, &listFilesOutput)
			if err != nil {
				return "", true, fmt.Errorf("failed to list files (for too long output): %w", err)
			}
			fileListString = listFilesOutput.Stdout
		}

		// TODO check if the NumContextLines is too high and if so, reduce it and retry the search or at least provide that as feedback here
		if len(fileListString) > maxSearchOutputLength {
			return "Search output is too long, and even the list of files that matched is too long. Try a more constrained path glob and/or a more specific search term. Alternatively, skip doing this search entirely if it's not essential.", true, nil
		} else {
			return fmt.Sprintf("Search output is too long. You could try with fewer context lines, a more constrained path glob and a more specific search term. Alternatively, skip doing this search entirely if it's not essential. Here is the list of matching files:\n\n%s", fileListString), true, nil
		}
	}

	if len(rawOutput) > maxSearchOutputLength {
		filePathRegex := regexp.MustCompile(`^[^0-9\s-].*$|^\d+[^0-9:=-].*$`)
		var paths []string
		// Scan the truncated part of the output for file paths
		// output[maxSearchOutputLength:] is the part of the string that is being cut off.
		for i, line := range strings.Split(rawOutput[maxSearchOutputLength:], "\n") {
			if i == 0 {
				// first line is a cut-off message or partial line, so can't be used to infer paths reliably
				continue
			}
			if filePathRegex.MatchString(line) {
				paths = append(paths, line)
			}
		}
		message := ""
		if len(paths) > 0 {
			// TODO /gen add a test that leads to this message
			message = fmt.Sprintf("\n... (search output truncated). The last file's results may be partial. Further matches exist in these files: \n%s", strings.Join(paths, "\n"))
		} else {
			message = "\n... (search output truncated). The last file's matches are cut off, but no other files matched."
		}

		// Truncate rawOutput so that rawOutput_truncated + message fits within maxSearchOutputLength
		// Ensure there's enough space for the message. If not, the message itself might be truncated or lead to issues.
		// A simple approach is to prioritize the message if space is very limited.
		maxLengthForOriginalContent := maxSearchOutputLength - len(message)

		var finalOutput string
		if maxLengthForOriginalContent <= 0 {
			// Not enough space for any original content, just show the (potentially truncated) message.
			// Ensure the message itself doesn't exceed maxSearchOutputLength.
			if len(message) > maxSearchOutputLength {
				finalOutput = message[:maxSearchOutputLength-len("...")] + "..." // Indicate message truncation
			} else {
				finalOutput = message
			}
		} else {
			truncatedOutputPortion := rawOutput
			if len(rawOutput) > maxLengthForOriginalContent {
				// Find the last newline before or at maxLengthForOriginalContent to avoid cutting mid-line.
				// If no newline, truncate at maxLengthForOriginalContent.
				safeTruncatePoint := strings.LastIndex(rawOutput[:maxLengthForOriginalContent], "\n")
				if safeTruncatePoint == -1 { // No newline found in the allowed portion
					safeTruncatePoint = maxLengthForOriginalContent
				}
				// Ensure we don't take an empty string if safeTruncatePoint is 0 and original content was meant to be shown.
				if safeTruncatePoint == 0 && maxLengthForOriginalContent > 0 {
					truncatedOutputPortion = rawOutput[:maxLengthForOriginalContent]
				} else {
					truncatedOutputPortion = rawOutput[:safeTruncatePoint]
				}
			}
			finalOutput = truncatedOutputPortion + message
		}
		// Final check to ensure we absolutely do not exceed maxSearchOutputLength.
		// This is a safeguard; the logic above should ideally prevent this.
		if len(finalOutput) > maxSearchOutputLength {
			// Ensure space for the truncation indicator itself.
			const truncationIndicator = "... (further truncated)"
			if maxSearchOutputLength > len(truncationIndicator) {
				finalOutput = finalOutput[:maxSearchOutputLength-len(truncationIndicator)] + truncationIndicator
			} else {
				// If maxSearchOutputLength is extremely small, just truncate to it.
				finalOutput = finalOutput[:maxSearchOutputLength]
			}
		}

		return finalOutput, false, nil
	}

	// Default case: output is within acceptable limits, or was already handled by refuseAtSearchOutputLength
	return rawOutput, false, nil
}

// getFilesMatchingPathGlob returns a list of files that match the input.PathGlob.
// It uses rg to find files and then filters them using filterFilesByGlob.
// This approach is consistent with how matchingFiles are determined in executeMainSearch when useManualGlobFiltering is true.
func (sCtx *searchContext) getFilesMatchingPathGlob() ([]string, error) {
	var rgFilesOutput env.EnvRunCommandOutput
	rgFilesCmdParts := []string{"--files", "--hidden"}
	rgFilesCmdParts = append(rgFilesCmdParts, "--ignore-file", sCtx.coreIgnorePath)
	if sCtx.sideIgnoreExists {
		rgFilesCmdParts = append(rgFilesCmdParts, "--ignore-file", ".sideignore")
	}

	// version guard not needed: this already existed before
	err := workflow.ExecuteActivity(sCtx.ctx, env.EnvRunCommandActivity, env.EnvRunCommandActivityInput{
		EnvContainer:       sCtx.envContainer,
		RelativeWorkingDir: "./",
		Command:            "rg",
		Args:               rgFilesCmdParts,
	}).Get(sCtx.ctx, &rgFilesOutput)
	if err != nil {
		return nil, fmt.Errorf("activity execution failed for rg to list files for glob '%s': %w", sCtx.input.PathGlob, err)
	}

	rawFiles := strings.TrimSpace(rgFilesOutput.Stdout)
	files := strings.Split(rawFiles, "\n")

	// Filter using filterFilesByGlob to ensure consistent glob matching (e.g., basename matching)
	// as per existing patterns in the codebase.
	return filterFilesByGlob(files, sCtx.input.PathGlob)
}

// processGlobalFallbackResults processes files found by the global fallback search.
// It may run git grep, parse results using ExtractSearchCodeBlocks, and truncate them if necessary.
func (sCtx *searchContext) processGlobalFallbackResults(allFiles []string) (string, error) {
	v := workflow.GetVersion(sCtx.ctx, "search-repo-global-fallback", workflow.DefaultVersion, 1)
	if v < 1 {
		return "", nil
	}

	matchingFiles, err := filterFilesByGlob(allFiles, sCtx.input.PathGlob)
	var matchingFilesSet map[string]bool
	for _, file := range matchingFiles {
		matchingFilesSet[file] = true
	}
	var nonMatchingFiles []string
	for _, file := range allFiles {
		if _, ok := matchingFilesSet[file]; !ok {
			nonMatchingFiles = append(nonMatchingFiles, file)
		}
	}

	numFilesFound := len(nonMatchingFiles)

	if numFilesFound == 0 {
		return "", nil
	}

	if numFilesFound > 3 && numFilesFound < 10 {
		return fmt.Sprintf("\n%s", strings.Join(nonMatchingFiles, "\n")), nil
	}

	if numFilesFound >= 10 {
		// TODO: Show summary of these file paths, eg file directories
		return fmt.Sprintf("\nSearch term '%s' found in %d files NOT matching the path glob", sCtx.input.SearchTerm, numFilesFound), nil
	}

	// Case: 0 < numFilesFound <= 3. Execute git grep.
	var gitGrepOutput env.EnvRunCommandOutput
	escapedFiles := make([]string, len(nonMatchingFiles))
	for i, f := range nonMatchingFiles {
		escapedFiles[i] = escapeShellArg(f)
	}

	// sCtx.gitGrepArgs already includes context lines, --show-function, --heading, --line-number,
	// and incorporates input.CaseInsensitive and input.FixedStrings.
	// sCtx.escapedSearchTerm is already shell-escaped.
	cmdStr := fmt.Sprintf("git grep %s %s -- %s", sCtx.gitGrepArgs, sCtx.escapedSearchTerm, strings.Join(escapedFiles, " "))

	err = workflow.ExecuteActivity(sCtx.ctx, env.EnvRunCommandActivity, env.EnvRunCommandActivityInput{
		EnvContainer:       sCtx.envContainer,
		RelativeWorkingDir: "./",
		Command:            "sh",
		Args:               []string{"-c", cmdStr},
	}).Get(sCtx.ctx, &gitGrepOutput)

	if err != nil {
		return "", fmt.Errorf("failed to execute git grep for global fallback: %w", err)
	}

	batchGitGrepOutput := gitGrepOutput.Stdout
	allCodeBlocks := tree_sitter.ExtractSearchCodeBlocks(batchGitGrepOutput)

	fileStats := make(map[string]struct {
		totalCodeChars int
		numMatches     int
	})
	filesToTruncate := make(map[string]bool)

	if len(allCodeBlocks) > 0 {
		for _, block := range allCodeBlocks {
			if block.FilePath == "" {
				// This should ideally not happen with git grep --heading. Log if it does.
				workflow.GetLogger(sCtx.ctx).Warn("ExtractSearchCodeBlocks produced a block with an empty FilePath from git grep output.", "blockHeader", block.HeaderContent)
				continue
			}
			stats := fileStats[block.FilePath]
			// Assuming block.Code contains the relevant lines of code for the match.
			stats.totalCodeChars += len(block.Code)
			stats.numMatches++
			fileStats[block.FilePath] = stats
		}

		for filePath, stats := range fileStats {
			if stats.totalCodeChars > 1000 {
				filesToTruncate[filePath] = true
			}
		}
	}

	if len(filesToTruncate) == 0 {
		return batchGitGrepOutput, nil // No truncation needed
	}

	// This TODO comment is added as per requirements when truncation logic is active.
	// TODO: If results for a file are truncated (>1000 chars), consider re-searching that file with ContextLines = 0 before showing the summary message.

	var resultSegments []string
	// git grep output with --heading separates file sections with "\n--\n".
	// If only one file, there's no "--" separator.
	var segments []string
	if strings.Contains(batchGitGrepOutput, "\n--\n") {
		segments = strings.Split(batchGitGrepOutput, "\n--\n")
	} else {
		segments = []string{batchGitGrepOutput} // Treat as a single segment
	}

	for _, segment := range segments {
		trimmedSegment := strings.TrimSpace(segment)
		if trimmedSegment == "" {
			continue
		}

		// Extract filename from the first line of the segment (due to --heading).
		firstNewlineIdx := strings.Index(trimmedSegment, "\n")
		var filePathFromSegment string
		if firstNewlineIdx == -1 { // Segment might be just the filename (e.g. empty file or no actual matches after header)
			filePathFromSegment = trimmedSegment
		} else {
			filePathFromSegment = trimmedSegment[:firstNewlineIdx]
		}

		// Ensure filePathFromSegment is clean and matches keys in fileStats (relative paths)
		// (fileStats keys are from CodeBlock.FilePath, which should be relative)

		stats, statsOk := fileStats[filePathFromSegment]
		if statsOk && filesToTruncate[filePathFromSegment] {
			resultSegments = append(resultSegments, fmt.Sprintf("%s: %d matches (results truncated due to length)", filePathFromSegment, stats.numMatches))
		} else {
			// If file not in fileStats (e.g., header only, no blocks parsed by ExtractSearchCodeBlocks),
			// or not marked for truncation, keep its original segment.
			resultSegments = append(resultSegments, segment) // Use the original, non-trimmed segment
		}
	}

	return strings.Join(resultSegments, "\n--\n"), nil
}

func updatedInputWithFixedStrings(input SearchRepositoryInput) SearchRepositoryInput {
	newInput := input
	newInput.FixedStrings = true
	return newInput
}

func updatedInputWithCaseInsensitive(input SearchRepositoryInput) SearchRepositoryInput {
	newInput := input
	newInput.CaseInsensitive = true
	return newInput
}

// isNonCriticalRgError checks if the stderr from rg indicates an error that
// shouldn't halt the search process (e.g., allow retries or fallback).
func isNonCriticalRgError(stderr string) bool {
	// "regex parse error" might occur if a fixed string search term is invalid as a regex.
	// "No files were searched" can happen if rg filters out all files due to ignores or path globs,
	// but we might still want to proceed with global fallback logic.
	return strings.Contains(stderr, "regex parse error") || strings.Contains(stderr, "No files were searched")
}

func constructFallbackMessage(input SearchRepositoryInput, allFilesMatchingGlob []string, allFiles []string, fallbackDetails string) string {
	var message string

	detailsForDisplay := ""
	if fallbackDetails != "" {
		if strings.HasPrefix(fallbackDetails, "\n") {
			detailsForDisplay = fallbackDetails // Already formatted with a leading newline
		} else {
			detailsForDisplay = "\n" + fallbackDetails // Prepend newline for direct content like git grep output
		}
	}

	if len(allFilesMatchingGlob) == 0 { // No files matched original PathGlob
		if len(allFiles) == 0 {
			message = fmt.Sprintf("No files matched the path glob '%s', and the search term '%s' was not found in any other files. Consider using a different path glob and/or search term.", input.PathGlob, input.SearchTerm)
		} else { // Term found globally
			message = fmt.Sprintf("No files matched the path glob '%s'. However, the search term '%s' was found in other files:%s", input.PathGlob, input.SearchTerm, detailsForDisplay)
		}
	} else { // Files matched the PathGlob, but term not found in them
		baseMessage := ""
		if len(allFilesMatchingGlob) <= 10 {
			// utils.Map is available from "sidekick/utils" import
			indentedFiles := utils.Map(allFilesMatchingGlob, func(file string) string {
				return "\t" + file
			})
			baseMessage = fmt.Sprintf("No results found in the following files:\n%s", strings.Join(indentedFiles, "\n"))
		} else {
			baseMessage = fmt.Sprintf("No results found in %d matching files", len(allFilesMatchingGlob))
		}

		if len(allFiles) == 0 {
			message = baseMessage + fmt.Sprintf("\nThe search term '%s' was also not found in any other files.", input.SearchTerm)
		} else { // Term found globally
			message = baseMessage + fmt.Sprintf("\nHowever, the search term '%s' was found in other files:%s", input.SearchTerm, detailsForDisplay)
		}
	}
	return message
}

// SearchRepository searches the repository for the given search term, using a searchContext
// to manage state, retries, and fallback logic.
func SearchRepository(ctx workflow.Context, envContainer env.EnvContainer, input SearchRepositoryInput) (string, error) {
	sCtx, err := initSearchContext(ctx, envContainer, input)
	if err != nil {
		return "", fmt.Errorf("failed to initialize search context: %w", err)
	}

	rawOutput, cmdStderr, allFiles, globMatchedFiles, err := sCtx.executeMainSearch()
	if err != nil {
		return "", err
	}

	processedOutput, shouldReturnEarlyWithMsg, err := sCtx.handleOutputLengthChecks(rawOutput, globMatchedFiles)
	if err != nil { // Error from handleOutputLengthChecks
		return "", err
	}
	if shouldReturnEarlyWithMsg {
		return addSearchPrefix(sCtx.input, processedOutput), nil
	}

	// Output is genuinely empty (or cleared due to length checks but not returned early)
	if strings.TrimSpace(processedOutput) == "" {
		// Check for significant rg errors before attempting retries or fallback.
		// If cmdStderr is not empty and the error is critical (not a parse error or "no files searched"),
		// return the stderr directly. rg usually prefixes its own errors, so no addSearchPrefix here.
		if cmdStderr != "" && !isNonCriticalRgError(cmdStderr) {
			return cmdStderr, nil
		}

		// Attempt retries for FixedStrings and CaseInsensitive.
		// These recursive calls create new searchContexts internally.
		if !sCtx.input.FixedStrings {
			return SearchRepository(ctx, envContainer, updatedInputWithFixedStrings(sCtx.input))
		}
		if !sCtx.input.CaseInsensitive {
			return SearchRepository(ctx, envContainer, updatedInputWithCaseInsensitive(sCtx.input))
		}

		// Retries exhausted, still no results. Apply specific glob fallback logic.
		if isSpecificPathGlob(sCtx.input.PathGlob) {
			allFilesMatchingGlob, err := sCtx.getFilesMatchingPathGlob() // Files matching original glob
			if err != nil {
				return "", fmt.Errorf("failed to get files matching path glob for fallback: %w", err)
			}

			fallbackDetails, err := sCtx.processGlobalFallbackResults(allFiles)
			if err != nil {
				return "", fmt.Errorf("failed to process global fallback results: %w", err)
			}

			finalMessage := constructFallbackMessage(sCtx.input, allFilesMatchingGlob, allFiles, fallbackDetails)
			return addSearchPrefix(sCtx.input, finalMessage), nil
		} else {
			// Generic no results message if not a specific path glob after all retries.
			return addSearchPrefix(sCtx.input, "No results found. Try a less restrictive search query, or try a different tool."), nil
		}
	} else {
		// Main search yielded results, and they were of acceptable length (or truncated and accepted).
		return addSearchPrefix(sCtx.input, processedOutput), nil
	}
}
