package dev

import (
	"fmt"
	"regexp"
	"sidekick/env"
	"strings"

	"go.temporal.io/sdk/workflow"
)

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

	if input.PathGlob != "" && input.PathGlob != "*" {
		rgArgs += fmt.Sprintf(` --glob "%s"`, input.PathGlob)
	}
	if input.CaseInsensitive {
		rgArgs += " --ignore-case"
		gitGrepArgs += " --ignore-case"
	}

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

	/* Combine rg and git grep commands into something like (plus the added args if any):
	*
	*     rg --files-with-matches search_term | xargs -r git grep --no-index --show-function --heading --line-number --context 5 search_term
	*
	* The idea is to use rg to get the list of files that contain the search
	* term very quickly, but then use git grep on the specific files to leverage
	* the --show-function parameter. This provides better context around the
	* search term.
	 */
	listFilesCmd := fmt.Sprintf(`rg %s "%s"`, rgArgs, input.SearchTerm)
	fullCmd := fmt.Sprintf(`%s | xargs -r %s "%s"`, listFilesCmd, gitGrepArgs, input.SearchTerm)

	var searchOutput env.EnvRunCommandOutput
	err = workflow.ExecuteActivity(ctx, env.EnvRunCommandActivity, env.EnvRunCommandActivityInput{
		EnvContainer:       envContainer,
		RelativeWorkingDir: "./",
		Command:            "sh",
		Args:               []string{"-c", fullCmd},
	}).Get(ctx, &searchOutput)
	if err != nil {
		return "", fmt.Errorf("failed to search the repository: %v", err)
	}

	output := searchOutput.Stdout
	if len(output) > refuseAtSearchOutputLength {
		var listFilesOutput env.EnvRunCommandOutput
		err = workflow.ExecuteActivity(ctx, env.EnvRunCommandActivity, env.EnvRunCommandActivityInput{
			EnvContainer:       envContainer,
			RelativeWorkingDir: "./",
			Command:            "sh",
			Args:               []string{"-c", listFilesCmd},
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
		if !input.CaseInsensitive {
			// retry with case-insensitive search
			input.CaseInsensitive = true
			return SearchRepository(ctx, envContainer, input)
		}

		if input.PathGlob != "" && input.PathGlob != "*" {
			// Check if the given path glob matches any files
			var listOutput env.EnvRunCommandOutput
			err = workflow.ExecuteActivity(ctx, env.EnvRunCommandActivity, env.EnvRunCommandActivityInput{
				EnvContainer:       envContainer,
				RelativeWorkingDir: "./",
				Command:            "rg",
				Args:               []string{"-g", input.PathGlob, "--files"},
			}).Get(ctx, &listOutput)
			if err != nil {
				return "", fmt.Errorf("failed to check path glob: %v", err)
			}

			if strings.TrimSpace(listOutput.Stdout) == "" {
				return fmt.Sprintf("No files matched the path glob %s - please try a different path glob", input.PathGlob), nil
			}
		}

		return SearchRepoNoResultsMessage, nil
	}

	return output, nil
}
