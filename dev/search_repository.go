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

func (sCtx *searchContext) executeMainSearch() (string, string, error) {
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
			return "", "", fmt.Errorf("failed to list files for manual glob filtering: %w", err)
		}

		allFiles := strings.Split(strings.TrimSpace(listFilesOutput.Stdout), "\n")
		var filteredFiles []string
		if strings.TrimSpace(listFilesOutput.Stdout) != "" {
			filteredFiles, err = filterFilesByGlob(allFiles, sCtx.input.PathGlob)
			if err != nil {
				// Pass along stderr from listFilesOutput as it might contain relevant info
				return "", listFilesOutput.Stderr, fmt.Errorf("failed to filter files by glob: %w", err)
			}
		}

		if len(filteredFiles) == 0 {
			// No files matched the glob after listing.
			// Return empty stdout, but include stderr from the listing command if any.
			return "", listFilesOutput.Stderr, nil
		} else {
			// Run git grep on the filtered files
			escapedFiles := make([]string, len(filteredFiles))
			for i, file := range filteredFiles {
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
				return "", searchOutput.Stderr, fmt.Errorf("failed to search filtered files: %w", err)
			}
			// Success, return stdout and stderr from the git grep command
			return searchOutput.Stdout, searchOutput.Stderr, nil
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
			return "", searchOutput.Stderr, fmt.Errorf("failed to search the repository: %w", err)
		}
		return searchOutput.Stdout, searchOutput.Stderr, nil
	}
}

func (sCtx *searchContext) handleOutputLengthChecks(rawOutput string) (string, bool, error) {
	if len(rawOutput) > refuseAtSearchOutputLength {
		var listFilesOutput env.EnvRunCommandOutput
		var err error
		var filesToListCmd string
		var fileListString string

		if sCtx.useManualGlobFiltering {
			// When manual glob filtering is used, list files matching the term, then filter by glob.
			// sCtx.rgArgs already includes the PathGlob if it's a simple one, or it's empty if complex (handled by filterFilesByGlob).
			// We need to find files containing the search term first.
			baseRgCmd := strings.Replace(sCtx.rgArgs, sCtx.input.PathGlob, "", 1) // Remove PathGlob if present, it's for filtering after
			baseRgCmd = strings.TrimSpace(baseRgCmd)
			filesToListCmd = fmt.Sprintf(`rg %s --files-with-matches -- %s`, baseRgCmd, sCtx.escapedSearchTerm)
			err = workflow.ExecuteActivity(sCtx.ctx, env.EnvRunCommandActivity, env.EnvRunCommandActivityInput{
				EnvContainer:       sCtx.envContainer,
				RelativeWorkingDir: "./",
				Command:            "sh",
				Args:               []string{"-c", filesToListCmd},
			}).Get(sCtx.ctx, &listFilesOutput)
			if err != nil {
				return "", true, fmt.Errorf("failed to list files (for too long output, manual glob): %w", err)
			}

			allFiles := strings.Split(strings.TrimSpace(listFilesOutput.Stdout), "\n")
			var filteredFiles []string
			if strings.TrimSpace(listFilesOutput.Stdout) != "" {
				// Now filter these files by the original PathGlob
				filteredFiles, err = filterFilesByGlob(allFiles, sCtx.input.PathGlob)
				if err != nil {
					return "", true, fmt.Errorf("failed to filter files (for too long output, manual glob): %w", err)
				}
			}
			fileListString = strings.Join(filteredFiles, "\n")
		} else {
			// Standard behavior: list files matching the term.
			// sCtx.rgArgs already includes the PathGlob and other necessary rg flags.
			filesToListCmd = fmt.Sprintf(`rg %s --files-with-matches -- %s`, sCtx.rgArgs, sCtx.escapedSearchTerm)
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
		maxLengthForOriginalContent := maxSearchOutputLength - len(message)
		if maxLengthForOriginalContent < 0 {
			maxLengthForOriginalContent = 0
		}

		truncatedOutputPortion := rawOutput
		if len(rawOutput) > maxLengthForOriginalContent {
			truncatedOutputPortion = rawOutput[:maxLengthForOriginalContent]
		}

		return truncatedOutputPortion + message, false, nil
	}

	return rawOutput, false, nil
}

// SearchRepository searches the repository for the given search term, ignoring matching .gitingore or .sideignore
// TODO: The core logic of this function will be refactored in Step 4 to use sCtx methods for retries and fallback.
// For now, it's minimally adapted to use the results from executeMainSearch and handleOutputLengthChecks.
func SearchRepository(ctx workflow.Context, envContainer env.EnvContainer, input SearchRepositoryInput) (string, error) {
	sCtx, err := initSearchContext(ctx, envContainer, input)
	if err != nil {
		return "", fmt.Errorf("failed to initialize search: %w", err)
	}

	rawOutput, cmdStderr, err := sCtx.executeMainSearch()
	if err != nil {
		// err from executeMainSearch is already descriptive (e.g., "failed to search repository")
		return "", err
	}

	processedOutput, returnEarlyWithMessage, err := sCtx.handleOutputLengthChecks(rawOutput)
	if err != nil {
		// err from handleOutputLengthChecks is already descriptive
		return "", err
	}

	if returnEarlyWithMessage {
		return addSearchPrefix(sCtx.input, processedOutput), nil
	}

	// At this point, processedOutput is the potentially truncated search output,
	// and cmdStderr is the stderr from the main search command.
	// The original logic for handling no results, retries, etc., will follow.
	// For now, we use processedOutput as 'output' and cmdStderr as 'searchOutput.Stderr'.

	// handle no results
	if strings.TrimSpace(processedOutput) == "" {
		// Note: searchOutput.Stderr is now cmdStderr
		if cmdStderr != "" && !strings.Contains(cmdStderr, "regex parse error") && !strings.Contains(cmdStderr, "No files were searched") {
			return cmdStderr, nil
		}

		if !input.FixedStrings {
			// retry with fixed strings search
			input.FixedStrings = true
			return SearchRepository(ctx, envContainer, input)
		}

		if !input.CaseInsensitive {
			// retry with case-insensitive search
			input.CaseInsensitive = true
			return SearchRepository(ctx, envContainer, input)
		}

		if isSpecificPathGlob(input.PathGlob) {
			// Check if the given path glob matches any files using manual filtering to respect ignore files
			var listOutput env.EnvRunCommandOutput
			listAllFilesCmd := "rg --files --hidden --ignore-file " + escapeShellArg(coreIgnorePath)
			if catOutput.ExitStatus == 0 {
				listAllFilesCmd += " --ignore-file .sideignore"
			}

			err = workflow.ExecuteActivity(ctx, env.EnvRunCommandActivity, env.EnvRunCommandActivityInput{
				EnvContainer:       envContainer,
				RelativeWorkingDir: "./",
				Command:            "sh",
				Args:               []string{"-c", listAllFilesCmd},
			}).Get(ctx, &listOutput)
			if err != nil {
				return "", fmt.Errorf("failed to list all files for path glob check: %v", err)
			}

			// Filter files using glob pattern
			allFiles := strings.Split(strings.TrimSpace(listOutput.Stdout), "\n")
			matchingFiles, err := filterFilesByGlob(allFiles, input.PathGlob)
			if err != nil {
				return "", err
			}

			if len(matchingFiles) == 0 {
				return addSearchPrefix(input, fmt.Sprintf("No files matched the path glob %s - please try a different path glob", input.PathGlob)), nil
			}

			// Show file metadata for no results when path glob is specified
			if len(matchingFiles) <= 10 {
				indentedFiles := utils.Map(matchingFiles, func(file string) string {
					return "\t" + file
				})
				return addSearchPrefix(input, fmt.Sprintf("No results found in the following files:\n%s",
					strings.Join(indentedFiles, "\n"))), nil
			}
			return addSearchPrefix(input, fmt.Sprintf("No results found in %d matching files", len(matchingFiles))), nil
		}

		return addSearchPrefix(input, "No results found. Try a less restrictive search query, or try a different tool."), nil
	}

	return addSearchPrefix(input, output), nil
}
