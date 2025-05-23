package dev

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sidekick/env"
	"strings"

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

// filterFilesByGlob filters a list of files using the given glob pattern.
// It tries matching against both the full path and the basename.
func filterFilesByGlob(files []string, globPattern string) ([]string, error) {
	var filteredFiles []string
	for _, file := range files {
		if file == "" {
			continue
		}
		// Try matching against the full path first
		matched, err := filepath.Match(globPattern, file)
		if err != nil {
			return nil, fmt.Errorf("invalid glob pattern %s: %v", globPattern, err)
		}
		// If full path doesn't match, try matching against just the basename
		if !matched {
			matched, err = filepath.Match(globPattern, filepath.Base(file))
			if err != nil {
				return nil, fmt.Errorf("invalid glob pattern %s: %v", globPattern, err)
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
const SearchRepoNoResultsMessage = "No results found. Try a less restrictive search query, or try a different tool."

type SearchRepositoryInput struct {
	PathGlob        string
	SearchTerm      string
	ContextLines    int
	CaseInsensitive bool
	FixedStrings    bool
}

// TODO /gen include the function name in the associated with each search result
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

// SearchRepository searches the repository for the given search term, ignoring matching .gitingore or .sideignore
func SearchRepository(ctx workflow.Context, envContainer env.EnvContainer, input SearchRepositoryInput) (string, error) {
	rgArgs := "--files-with-matches"
	gitGrepArgs := fmt.Sprintf("git grep --no-index --show-function --heading --line-number --context %d", input.ContextLines)

	if input.CaseInsensitive {
		rgArgs += " --ignore-case"
		gitGrepArgs += " --ignore-case"
	}
	if input.FixedStrings {
		rgArgs += " --fixed-strings"
		gitGrepArgs += " --fixed-strings"
	}
	// Don't use --glob with rg when PathGlob is specified, as it overrides ignore files
	// We'll filter manually instead to respect .gitignore and .sideignore
	useManualGlobFiltering := input.PathGlob != "" && input.PathGlob != "*"

	// TODO /gen replace with a new env.FileExistsActivity - we need to implement that.
	var catOutput env.EnvRunCommandOutput
	err := workflow.ExecuteActivity(ctx, env.EnvRunCommandActivity, env.EnvRunCommandActivityInput{
		EnvContainer:       envContainer,
		RelativeWorkingDir: "./",
		Command:            "cat",
		Args:               []string{".sideignore"},
	}).Get(ctx, &catOutput)
	if err != nil {
		return "", fmt.Errorf("failed to cat .sideignore: %v", err)
	}

	// if successful, .sideignore file exists
	if catOutput.ExitStatus == 0 {
		rgArgs += " --ignore-file .sideignore"
	}

	escapedSearchTerm := escapeShellArg(input.SearchTerm)
	
	var searchOutput env.EnvRunCommandOutput
	if useManualGlobFiltering {
		// When using manual glob filtering, we need to:
		// 1. Get the list of files from rg (respecting ignore files)
		// 2. Filter them manually using the glob pattern
		// 3. Run git grep on the filtered files
		listFilesCmd := fmt.Sprintf(`rg %s %s`, rgArgs, escapedSearchTerm)
		
		var listFilesOutput env.EnvRunCommandOutput
		err = workflow.ExecuteActivity(ctx, env.EnvRunCommandActivity, env.EnvRunCommandActivityInput{
			EnvContainer:       envContainer,
			RelativeWorkingDir: "./",
			Command:            "sh",
			Args:               []string{"-c", listFilesCmd},
		}).Get(ctx, &listFilesOutput)
		if err != nil {
			return "", fmt.Errorf("failed to list files for manual glob filtering: %v", err)
		}

		// Filter files using glob pattern
		allFiles := strings.Split(strings.TrimSpace(listFilesOutput.Stdout), "\n")
		filteredFiles, err := filterFilesByGlob(allFiles, input.PathGlob)
		if err != nil {
			return "", err
		}

		if len(filteredFiles) == 0 {
			// No files matched the glob pattern after filtering
			if strings.TrimSpace(listFilesOutput.Stdout) == "" {
				// No files matched the search term at all - continue to normal "no results" handling
				searchOutput.Stdout = ""
			} else {
				// Files matched the search term but none matched the glob pattern
				return fmt.Sprintf("No files matched the path glob %s - please try a different path glob", input.PathGlob), nil
			}
		} else {
			// Run git grep on the filtered files
			escapedFiles := make([]string, len(filteredFiles))
			for i, file := range filteredFiles {
				escapedFiles[i] = escapeShellArg(file)
			}
			filesArg := strings.Join(escapedFiles, " ")
			fullCmd := fmt.Sprintf(`%s %s %s`, gitGrepArgs, escapedSearchTerm, filesArg)
			
			err = workflow.ExecuteActivity(ctx, env.EnvRunCommandActivity, env.EnvRunCommandActivityInput{
				EnvContainer:       envContainer,
				RelativeWorkingDir: "./",
				Command:            "sh",
				Args:               []string{"-c", fullCmd},
			}).Get(ctx, &searchOutput)
			if err != nil {
				return "", fmt.Errorf("failed to search filtered files: %v", err)
			}
		}
	} else {
		// Original behavior: use rg + git grep pipeline
		fullCmd := fmt.Sprintf(`rg %s %s | xargs -r %s %s`, rgArgs, escapedSearchTerm, gitGrepArgs, escapedSearchTerm)
		
		err = workflow.ExecuteActivity(ctx, env.EnvRunCommandActivity, env.EnvRunCommandActivityInput{
			EnvContainer:       envContainer,
			RelativeWorkingDir: "./",
			Command:            "sh",
			Args:               []string{"-c", fullCmd},
		}).Get(ctx, &searchOutput)
		if err != nil {
			return "", fmt.Errorf("failed to search the repository: %v", err)
		}
	}

	output := searchOutput.Stdout
	if len(output) > refuseAtSearchOutputLength {
		var listFilesOutput env.EnvRunCommandOutput
		if useManualGlobFiltering {
			// For manual glob filtering, we already have the file list from the previous step
			listFilesCmd := fmt.Sprintf(`rg %s %s`, rgArgs, escapedSearchTerm)
			err = workflow.ExecuteActivity(ctx, env.EnvRunCommandActivity, env.EnvRunCommandActivityInput{
				EnvContainer:       envContainer,
				RelativeWorkingDir: "./",
				Command:            "sh",
				Args:               []string{"-c", listFilesCmd},
			}).Get(ctx, &listFilesOutput)
			if err != nil {
				return "", fmt.Errorf("failed to list files to search the repository: %v", err)
			}

			// Filter files using glob pattern for the file list
			allFiles := strings.Split(strings.TrimSpace(listFilesOutput.Stdout), "\n")
			filteredFiles, err := filterFilesByGlob(allFiles, input.PathGlob)
			if err != nil {
				return "", err
			}

			fileList := strings.Join(filteredFiles, "\n")
			if len(fileList) > maxSearchOutputLength {
				return "Search output is too long, and even the list of files that matched is too long. Try a more constrained path glob and/or a more specific search term. Alternatively, skip doing this search entirely if it's not essential.", nil
			} else {
				return fmt.Sprintf("Search output is too long. You could try with fewer context lines, a more constrained path glob and a more specific search term. Alternatively, skip doing this search entirely if it's not essential. Here is the list of matching files:\n\n%s", fileList), nil
			}
		} else {
			err = workflow.ExecuteActivity(ctx, env.EnvRunCommandActivity, env.EnvRunCommandActivityInput{
				EnvContainer:       envContainer,
				RelativeWorkingDir: "./",
				Command:            "sh",
				Args:               []string{"-c", fmt.Sprintf(`rg %s %s`, rgArgs, escapedSearchTerm)},
			}).Get(ctx, &listFilesOutput)
			if err != nil {
				return "", fmt.Errorf("failed to list files to search the repository: %v", err)
			}

			// TODO check if the NumContextLines is too high and if so, reduce it and retry the search or at least provide that as feedback here
			if len(listFilesOutput.Stdout) > maxSearchOutputLength {
				return "Search output is too long, and even the list of files that matched is too long. Try a more constrained path glob and/or a more specific search term. Alternatively, skip doing this search entirely if it's not essential.", nil
			} else {
				return fmt.Sprintf("Search output is too long. You could try with fewer context lines, a more constrained path glob and a more specific search term. Alternatively, skip doing this search entirely if it's not essential. Here is the list of matching files:\n\n%s", listFilesOutput.Stdout), nil
			}
		}
	}

	if len(output) > maxSearchOutputLength {
		var paths []string
		filePathRegex := regexp.MustCompile(`^[^0-9\s-].*$|^\d+[^0-9:=-].*$`)
		for i, line := range strings.Split(output[maxSearchOutputLength:], "\n") {
			if i == 0 {
				// first line is a cut-off message, so can't be used to infer paths
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
		return output[:maxSearchOutputLength-len(message)] + message, nil
	}

	// handle no results
	if strings.TrimSpace(output) == "" {
		if searchOutput.Stderr != "" && !strings.Contains(searchOutput.Stderr, "regex parse error") && !strings.Contains(searchOutput.Stderr, "No files were searched") {
			return searchOutput.Stderr, nil
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

		if input.PathGlob != "" && input.PathGlob != "*" {
			// Check if the given path glob matches any files using manual filtering to respect ignore files
			var listOutput env.EnvRunCommandOutput
			listAllFilesCmd := "rg --files"
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
				return fmt.Sprintf("No files matched the path glob %s - please try a different path glob", input.PathGlob), nil
			}
		}

		return SearchRepoNoResultsMessage, nil
	}

	return output, nil
}
