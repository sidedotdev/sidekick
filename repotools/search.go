package repotools

import (
	"fmt"
	"regexp"
	"sidekick/coding/tree_sitter/parser"
	"sidekick/env/localio"
	"sidekick/utils"
	"strings"

	doublestar "github.com/bmatcuk/doublestar/v4"
	"github.com/rs/zerolog/log"
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

// hasLiteralPathSegment returns true if the glob pattern contains at least one
// path segment starting with a literal character, indicating the pattern targets
// a specific directory or file rather than matching broadly (e.g. "**/*.go").
func hasLiteralPathSegment(pattern string) bool {
	if pattern == "" {
		return false
	}
	for _, seg := range strings.Split(pattern, "/") {
		if seg != "" && seg[0] != '*' && seg[0] != '?' && seg[0] != '[' && seg[0] != '{' {
			return true
		}
	}
	return false
}

const RefuseAtSearchOutputLength = 20000
const MaxSearchOutputLength = 10000

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
	NoIgnore        bool
}

// TODO /gen include the function name in the associated with each search result

// SearchRunner supplies the primitive operations a repository search is
// composed of, so the one algorithm can run in-process against the local
// filesystem, through an environment, or as one Temporal activity per
// primitive. Implementations carry their own context.
type SearchRunner interface {
	// RunCommand runs command with args at the repository root.
	RunCommand(command string, args ...string) (localio.RunCommandOutput, error)
	// EnsureCoreIgnoreFile writes CoreIgnoreFileContent at CoreIgnoreRelPath
	// and returns the path to hand to rg.
	EnsureCoreIgnoreFile() (string, error)
	// FileSymbols lists the symbols defined in the repository file at path.
	FileSymbols(path string) ([]parser.Symbol, error)
	// Legacy reports whether the behavior that predates changeID must be
	// retained. Only replays of workflow histories recorded before the
	// change answer true.
	Legacy(changeID string) bool
}

type searchContext struct {
	runner                 SearchRunner
	input                  SearchRepositoryInput
	coreIgnorePath         string
	rgArgs                 string
	gitGrepArgs            string
	escapedSearchTerm      string
	useManualGlobFiltering bool
	sideIgnoreExists       bool
	gitIgnoreExists        bool
	// includeIgnoredTargets is set for path-specific globs: files matching the
	// glob are searched even when ignored, while hints about matches outside
	// the glob keep respecting ignores.
	includeIgnoredTargets bool
	// hiddenIgnoredDirs are ignored directories that the glob's leading
	// wildcard could descend into but which stayed excluded from the search;
	// they are reported so the caller can target them explicitly.
	hiddenIgnoredDirs []string
}

// ignoredEntriesSeparator terminates the ignored entry listing that precedes
// the search output in the same shell command (see explicitTargetRgCmd).
const ignoredEntriesSeparator = "__SIDEKICK_IGNORED_ENTRIES__"

// splitIgnoredEntriesListing separates the ignored entries listed by
// explicitTargetRgCmd from the output of the rg command that followed them.
func splitIgnoredEntriesListing(stdout string) (ignoredEntries []string, rest string) {
	before, after, found := strings.Cut(stdout, ignoredEntriesSeparator+"\n")
	if !found {
		return nil, stdout
	}
	if trimmed := strings.TrimSpace(before); trimmed != "" {
		ignoredEntries = strings.Split(trimmed, "\n")
	}
	return ignoredEntries, after
}

// hiddenIgnoredDirs returns the ignored directories, from a git ls-files
// listing, that one of the glob's leading directory patterns matches. Such
// directories cannot be re-included by targetUnignorePatterns without
// re-including every directory the wildcard matches, so they stay excluded.
// Directories nested under an already-reported one are dropped, as are
// Sidekick's core-ignored internals, which would otherwise be reported in
// every repository despite never being a useful search target.
func hiddenIgnoredDirs(ignoredEntries []string, glob string) []string {
	segments := strings.Split(glob, "/")
	var dirs []string
	for _, entry := range ignoredEntries {
		dir, isDir := strings.CutSuffix(entry, "/")
		if !isDir || isCoreIgnoredDir(dir) || hasReportedAncestor(dirs, dir) {
			continue
		}
		for i := 1; i < len(segments); i++ {
			if ok, _ := doublestar.Match(strings.Join(segments[:i], "/"), dir); ok {
				dirs = append(dirs, dir)
				break
			}
		}
	}
	return dirs
}

func hasReportedAncestor(reported []string, dir string) bool {
	for _, r := range reported {
		if strings.HasPrefix(dir, r+"/") {
			return true
		}
	}
	return false
}

// isCoreIgnoredDir reports whether dir is covered by a core ignore pattern or
// is an ancestor of one; git lists an ancestor when all of its contents are
// ignored.
func isCoreIgnoredDir(dir string) bool {
	for _, pattern := range strings.Fields(CoreIgnoreFileContent) {
		if dir == pattern || strings.HasPrefix(dir, pattern+"/") || strings.HasPrefix(pattern, dir+"/") {
			return true
		}
	}
	return false
}

// hiddenIgnoredDirsNote describes ignored directories left out of a search and
// how to reach them, or returns "" when there are none.
func (sCtx *searchContext) hiddenIgnoredDirsNote() string {
	dirs := sCtx.hiddenIgnoredDirs
	if len(dirs) == 0 {
		return ""
	}
	const maxListed = 10
	listed := dirs
	if len(listed) > maxListed {
		listed = listed[:maxListed]
	}
	lines := utils.Map(listed, func(dir string) string { return "\t" + dir + "/" })
	note := fmt.Sprintf("\n\nNote: %d ignored directories reachable only through the leading wildcard of '%s' were not searched:\n%s",
		len(dirs), sCtx.input.PathGlob, strings.Join(lines, "\n"))
	if len(dirs) > maxListed {
		note += fmt.Sprintf("\n\t... and %d more", len(dirs)-maxListed)
	}

	first, rest, _ := strings.Cut(sCtx.input.PathGlob, "/")
	example := dirs[0] + "/" + rest
	if first == "**" {
		example = dirs[0] + "/" + sCtx.input.PathGlob
	}
	return note + fmt.Sprintf("\nTo search them, start the path glob with a literal directory (e.g. '%s'), or run rg --no-ignore via a shell command.", example)
}

// targetUnignorePatterns returns gitignore-format negations that re-include
// files matching a path-specific glob. Ancestor directories are negated too,
// since an excluded directory is never descended into, so its children could
// not be re-included on their own. Leading pure-wildcard ancestors ("*",
// "**") are skipped as negating them would re-include every ignored directory.
func targetUnignorePatterns(glob string) []string {
	segments := strings.Split(glob, "/")
	var patterns []string
	hasLiteralAncestor := false
	for i := 1; i < len(segments); i++ {
		if strings.Trim(segments[i-1], "*") != "" {
			hasLiteralAncestor = true
		}
		if hasLiteralAncestor {
			patterns = append(patterns, "!"+strings.Join(segments[:i], "/")+"/")
		}
	}
	return append(patterns, "!"+glob)
}

// startsWithWildcardDir reports whether a glob's first directory segment is a
// pure wildcard such as "*" or "**", in which case targetUnignorePatterns
// cannot re-include files under excluded directories matched by it.
func startsWithWildcardDir(glob string) bool {
	first, _, hasDir := strings.Cut(glob, "/")
	return hasDir && strings.Trim(first, "*") == ""
}

// explicitTargetRgCmd builds a shell command running rg with the given
// arguments so that files matching the target glob are listed even when
// ignored. It reuses the precedence mechanism behind .sideignore overrides:
// rg's own ignore file discovery is disabled and a single generated ignore
// file stands in for every root-level ignore source, followed by the target
// negations, which win as the last patterns. Discovery must be fully disabled
// because rg gives .ignore/.rgignore precedence over any --ignore-file. The
// file is created and removed within the one shell command to avoid extra
// round-trips. As with .sideignore overrides, ignore files in subdirectories
// are not applied in this mode. Like git itself, this cannot re-include files
// whose excluded ancestor directory is only reachable through wildcard
// segments of the glob; the empty-result NoIgnore fallback still covers those.
//
// When listIgnoredDirs is set, the directories excluded by that same generated
// file are first printed from the git index (without descending into them,
// each with a trailing slash), followed by ignoredEntriesSeparator, so the
// caller can report which directories the search skipped. Outside a git
// repository nothing is listed.
func (sCtx *searchContext) explicitTargetRgCmd(listIgnoredDirs bool, rgArgs ...string) string {
	patterns := utils.Map(targetUnignorePatterns(sCtx.input.PathGlob), escapeShellArg)
	listCmd := ""
	if listIgnoredDirs {
		listCmd = `git ls-files --others --ignored --directory --exclude-from="$f" 2>/dev/null; echo ` + ignoredEntriesSeparator + `; `
	}
	// Sources are concatenated in ascending precedence, with .sideignore last
	// so that it keeps overriding the others as in normal searches.
	ignoreSources := escapeShellArg(sCtx.coreIgnorePath) + " .git/info/exclude .gitignore .ignore .rgignore .sideignore"
	return fmt.Sprintf(
		`f=$(mktemp) && { for i in %s; do [ -f "$i" ] && cat "$i" && echo; done; printf '%%s\n' %s; } > "$f" && { %srg --no-ignore --ignore-file "$f" %s; }; s=$?; rm -f "$f"; exit $s`,
		ignoreSources, strings.Join(patterns, " "), listCmd, strings.Join(rgArgs, " "))
}

func initSearchContext(runner SearchRunner, input SearchRepositoryInput) (*searchContext, error) {
	coreIgnorePath, err := runner.EnsureCoreIgnoreFile()
	if err != nil {
		return nil, fmt.Errorf("failed to ensure core ignore file: %w", err)
	}

	sCtx := &searchContext{
		runner:         runner,
		input:          input,
		coreIgnorePath: coreIgnorePath,
	}

	sCtx.escapedSearchTerm = escapeShellArg(input.SearchTerm)

	legacyGlobFiltering := sCtx.runner.Legacy("manual-search-glob-filtering")
	sCtx.useManualGlobFiltering = isSpecificPathGlob(sCtx.input.PathGlob) && !legacyGlobFiltering

	// A glob with a literal path segment names its target explicitly, so
	// ignored files matching it are searched from the outset instead of only
	// via the empty-result NoIgnore fallback.
	sCtx.includeIgnoredTargets = !sCtx.runner.Legacy("explicit-target-ignore-bypass") && !input.NoIgnore &&
		sCtx.useManualGlobFiltering && hasLiteralPathSegment(sCtx.input.PathGlob)

	// Base rgArgs
	sCtx.rgArgs = "--files-with-matches --hidden"
	if input.NoIgnore {
		sCtx.rgArgs += " --no-ignore"
	}
	if !sCtx.includeIgnoredTargets {
		// explicitTargetRgCmd folds the core ignore file into its own
		// generated ignore file, where the target negations can override it.
		sCtx.rgArgs += " --ignore-file " + escapeShellArg(sCtx.coreIgnorePath)
	}

	// Base gitGrepArgs. rg selects files using Rust regex syntax, so git grep
	// must use extended regex (not its default basic regex, where "|", "+",
	// "?", "{}" and "()" are literals) or it finds nothing in the files rg
	// listed. A later --fixed-strings overrides --extended-regexp.
	sCtx.gitGrepArgs = fmt.Sprintf("git grep --no-index --extended-regexp --show-function --heading --line-number --context %d", input.ContextLines)

	if input.CaseInsensitive {
		sCtx.rgArgs += " --ignore-case"
		sCtx.gitGrepArgs += " --ignore-case"
	}
	if input.FixedStrings {
		sCtx.rgArgs += " --fixed-strings"
		sCtx.gitGrepArgs += " --fixed-strings"
	}

	// Explicit-target searches supply .gitignore/.sideignore themselves via
	// explicitTargetRgCmd, so the existence probes below are unnecessary.
	if input.NoIgnore || sCtx.includeIgnoredTargets {
		return sCtx, nil
	}

	// Check for .sideignore file
	// TODO /gen replace with a file-exists SearchRunner primitive
	catOutput, err := sCtx.runner.RunCommand("cat", ".sideignore")
	if err != nil {
		// This error means the activity execution failed (e.g., worker unavailable, panic in activity)
		return nil, fmt.Errorf("failed to execute command to check for .sideignore file: %w", err)
	}

	sCtx.sideIgnoreExists = false
	if catOutput.ExitStatus == 0 { // cat was successful, so .sideignore exists
		sCtx.sideIgnoreExists = true

		if !sCtx.runner.Legacy("sideignore-overrides-gitignore") {
			// Use --no-ignore-vcs to disable .gitignore processing, then add both
			// .gitignore and .sideignore as ignore files. Since later ignore files
			// have higher precedence in ripgrep, .sideignore patterns (including
			// negation patterns like !vendor/) will override .gitignore patterns.
			sCtx.rgArgs += " --no-ignore-vcs"

			// Check if .gitignore exists before adding it
			gitignoreOutput, _ := sCtx.runner.RunCommand("cat", ".gitignore")
			if gitignoreOutput.ExitStatus == 0 {
				sCtx.rgArgs += " --ignore-file .gitignore"
				sCtx.gitIgnoreExists = true
			}

			sCtx.rgArgs += " --ignore-file .sideignore"
		} else {
			// Old behavior: just add .sideignore without override support
			sCtx.rgArgs += " --ignore-file .sideignore"
		}
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

// CoreIgnoreFileContent is the content sidekick writes to the core ignore file
// it manages. Besides .git, it keeps .side/tmp ignored so agents have a
// sanctioned scratch location that never pollutes search results.
const CoreIgnoreFileContent = ".git\n.side/tmp\n"

// CoreIgnoreRelPath is where SearchRunner.EnsureCoreIgnoreFile places the core
// ignore file, relative to the env's working directory. Living in the
// workspace's sanctioned scratch dir rather than a system temp dir means it
// persists with the workspace (e.g. across devpod container restarts), and it
// is itself excluded from search results by CoreIgnoreFileContent.
const CoreIgnoreRelPath = ".side/tmp/core_ignore"

// returns:
// 1. raw command stdOut
// 2. raw command stdErr
// 3. list of all files that contain search term (ignoring path glob), if useManualGlobFiltering is true
// 4. list of files that also matched the glob too
// 5. error
func (sCtx *searchContext) executeMainSearch() (string, string, []string, []string, error) {
	var searchOutput localio.RunCommandOutput
	var listFilesOutput localio.RunCommandOutput
	var err error

	if sCtx.useManualGlobFiltering {
		// When using manual glob filtering, we need to:
		// 1. Get the list of files from rg (respecting ignore files) that contain the search term
		// 2. Filter them manually using the glob pattern
		// 3. Run git grep on the filtered files
		listFilesCmd := fmt.Sprintf(`rg %s --files-with-matches -- %s`, sCtx.rgArgs, sCtx.escapedSearchTerm)
		reportHiddenIgnoredDirs := sCtx.includeIgnoredTargets && startsWithWildcardDir(sCtx.input.PathGlob)
		if sCtx.includeIgnoredTargets {
			listFilesCmd = sCtx.explicitTargetRgCmd(reportHiddenIgnoredDirs, sCtx.rgArgs, "--", sCtx.escapedSearchTerm)
		}

		listFilesOutput, err = sCtx.runner.RunCommand("sh", "-c", listFilesCmd)
		if err != nil {
			return "", "", nil, nil, fmt.Errorf("failed to list files for manual glob filtering: %w", err)
		}

		filesStdout := listFilesOutput.Stdout
		if reportHiddenIgnoredDirs {
			var ignoredEntries []string
			ignoredEntries, filesStdout = splitIgnoredEntriesListing(filesStdout)
			sCtx.hiddenIgnoredDirs = hiddenIgnoredDirs(ignoredEntries, sCtx.input.PathGlob)
		}

		var allFilesMatchingSearchTerm []string
		if trimmedStdout := strings.TrimSpace(filesStdout); trimmedStdout != "" {
			allFilesMatchingSearchTerm = strings.Split(trimmedStdout, "\n")
		} else {
			allFilesMatchingSearchTerm = []string{}
		}

		var filesMatchingGlobAndSearchTerm []string
		if len(allFilesMatchingSearchTerm) > 0 {
			filesMatchingGlobAndSearchTerm, err = FilterFilesByGlob(allFilesMatchingSearchTerm, sCtx.input.PathGlob)
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

			searchOutput, err = sCtx.runner.RunCommand("sh", "-c", fullCmd)
			if err != nil {
				return "", searchOutput.Stderr, allFilesMatchingSearchTerm, filesMatchingGlobAndSearchTerm, fmt.Errorf("failed to search filtered files: %w", err)
			}
			// Success, return stdout and stderr from the git grep command
			return searchOutput.Stdout, searchOutput.Stderr, allFilesMatchingSearchTerm, filesMatchingGlobAndSearchTerm, nil
		}
	} else {
		// Original behavior: use rg + git grep pipeline
		fullCmd := fmt.Sprintf(`rg %s -- %s | xargs -r %s -- %s`, sCtx.rgArgs, sCtx.escapedSearchTerm, sCtx.gitGrepArgs, sCtx.escapedSearchTerm)

		searchOutput, err = sCtx.runner.RunCommand("sh", "-c", fullCmd)
		if err != nil {
			return "", searchOutput.Stderr, nil, nil, fmt.Errorf("failed to search the repository: %w", err)
		}
		return searchOutput.Stdout, searchOutput.Stderr, nil, nil, nil
	}
}

func (sCtx *searchContext) handleOutputLengthChecks(rawOutput string, globMatchedFiles []string) (string, bool, error) {
	if len(rawOutput) > RefuseAtSearchOutputLength {
		var err error
		var filesToListCmd string
		var fileListString string

		if sCtx.useManualGlobFiltering {
			if sCtx.runner.Legacy("search-repo-remove-extra-command") {
				// fake activity execution just to ensure workflows can be
				// replayed deterministically: we never needed this and did it
				// by mistake
				_, err = sCtx.runner.RunCommand("")
				if err != nil {
					return "", true, fmt.Errorf("failed to list files (for too long output, manual glob): %w", err)
				}
			}

			fileListString = strings.Join(globMatchedFiles, "\n")
		} else {
			var listFilesOutput localio.RunCommandOutput
			filesToListCmd = fmt.Sprintf(`rg %s -- %s`, sCtx.rgArgs, sCtx.escapedSearchTerm)
			listFilesOutput, err = sCtx.runner.RunCommand("sh", "-c", filesToListCmd)
			if err != nil {
				return "", true, fmt.Errorf("failed to list files (for too long output): %w", err)
			}
			fileListString = listFilesOutput.Stdout
		}

		// TODO check if the NumContextLines is too high and if so, reduce it and retry the search or at least provide that as feedback here
		if len(fileListString) > MaxSearchOutputLength {
			return "Search output is too long, and even the list of files that matched is too long. Try a more constrained path glob and/or a more specific search term. Alternatively, skip doing this search entirely if it's not essential.", true, nil
		} else {
			return fmt.Sprintf("Search output is too long. You could try with fewer context lines, a more constrained path glob and a more specific search term. Alternatively, skip doing this search entirely if it's not essential. Here is the list of matching files:\n\n%s", fileListString), true, nil
		}
	}

	if len(rawOutput) > MaxSearchOutputLength {
		filePathRegex := regexp.MustCompile(`^[^0-9\s-].*$|^\d+[^0-9:=-].*$`)
		var paths []string
		// Scan the truncated part of the output for file paths
		// output[MaxSearchOutputLength:] is the part of the string that is being cut off.
		for i, line := range strings.Split(rawOutput[MaxSearchOutputLength:], "\n") {
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

		// Truncate rawOutput so that rawOutput_truncated + message fits within MaxSearchOutputLength
		// Ensure there's enough space for the message. If not, the message itself might be truncated or lead to issues.
		// A simple approach is to prioritize the message if space is very limited.
		maxLengthForOriginalContent := MaxSearchOutputLength - len(message)

		var finalOutput string
		if maxLengthForOriginalContent <= 0 {
			// Not enough space for any original content, just show the (potentially truncated) message.
			// Ensure the message itself doesn't exceed MaxSearchOutputLength.
			if len(message) > MaxSearchOutputLength {
				finalOutput = message[:MaxSearchOutputLength-len("...")] + "..." // Indicate message truncation
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
		// Final check to ensure we absolutely do not exceed MaxSearchOutputLength.
		// This is a safeguard; the logic above should ideally prevent this.
		if len(finalOutput) > MaxSearchOutputLength {
			// Ensure space for the truncation indicator itself.
			const truncationIndicator = "... (further truncated)"
			if MaxSearchOutputLength > len(truncationIndicator) {
				finalOutput = finalOutput[:MaxSearchOutputLength-len(truncationIndicator)] + truncationIndicator
			} else {
				// If MaxSearchOutputLength is extremely small, just truncate to it.
				finalOutput = finalOutput[:MaxSearchOutputLength]
			}
		}

		return finalOutput, false, nil
	}

	// Default case: output is within acceptable limits, or was already handled by RefuseAtSearchOutputLength
	return rawOutput, false, nil
}

// getFilesMatchingPathGlob returns a list of files that match the input.PathGlob.
// It uses rg to find files and then filters them using filterFilesByGlob.
// This approach is consistent with how matchingFiles are determined in executeMainSearch when useManualGlobFiltering is true.
func (sCtx *searchContext) getFilesMatchingPathGlob() ([]string, error) {
	rgFilesCmdParts := []string{"--files", "--hidden"}
	if sCtx.includeIgnoredTargets {
		// Ignored target files are listed as well, so the no-results message
		// names them as searched.
		rgFilesOutput, err := sCtx.runner.RunCommand("sh", "-c", sCtx.explicitTargetRgCmd(false, rgFilesCmdParts...))
		if err != nil {
			return nil, fmt.Errorf("activity execution failed for rg to list files for glob '%s': %w", sCtx.input.PathGlob, err)
		}
		files := strings.Split(strings.TrimSpace(rgFilesOutput.Stdout), "\n")
		return FilterFilesByGlob(files, sCtx.input.PathGlob)
	}

	if sCtx.input.NoIgnore {
		rgFilesCmdParts = append(rgFilesCmdParts, "--no-ignore")
	}
	rgFilesCmdParts = append(rgFilesCmdParts, "--ignore-file", sCtx.coreIgnorePath)
	if sCtx.sideIgnoreExists {
		if sCtx.gitIgnoreExists {
			// New behavior: use --no-ignore-vcs so .sideignore can override .gitignore
			rgFilesCmdParts = append(rgFilesCmdParts, "--no-ignore-vcs", "--ignore-file", ".gitignore", "--ignore-file", ".sideignore")
		} else {
			// Old behavior or no .gitignore: just add .sideignore
			rgFilesCmdParts = append(rgFilesCmdParts, "--ignore-file", ".sideignore")
		}
	}

	// version guard not needed: this already existed before
	rgFilesOutput, err := sCtx.runner.RunCommand("rg", rgFilesCmdParts...)
	if err != nil {
		return nil, fmt.Errorf("activity execution failed for rg to list files for glob '%s': %w", sCtx.input.PathGlob, err)
	}

	rawFiles := strings.TrimSpace(rgFilesOutput.Stdout)
	files := strings.Split(rawFiles, "\n")

	// Filter using filterFilesByGlob to ensure consistent glob matching (e.g., basename matching)
	// as per existing patterns in the codebase.
	return FilterFilesByGlob(files, sCtx.input.PathGlob)
}

// processGlobalFallbackResults processes files found by the global fallback search.
// It may run git grep, parse results using ExtractSearchCodeBlocks, and truncate them if necessary.
func (sCtx *searchContext) processGlobalFallbackResults(allFiles []string) (string, error) {
	if sCtx.runner.Legacy("search-repo-global-fallback") {
		return "", nil
	}

	matchingFiles, err := FilterFilesByGlob(allFiles, sCtx.input.PathGlob)
	if err != nil {
		return "", fmt.Errorf("failed to filter files by glob in fallback: %w", err)
	}

	matchingFilesSet := make(map[string]bool)
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
	escapedFiles := make([]string, len(nonMatchingFiles))
	for i, f := range nonMatchingFiles {
		escapedFiles[i] = escapeShellArg(f)
	}

	// sCtx.gitGrepArgs already includes context lines, --show-function, --heading, --line-number,
	// and incorporates input.CaseInsensitive and input.FixedStrings.
	// sCtx.escapedSearchTerm is already shell-escaped.
	cmdStr := fmt.Sprintf("%s -- %s %s", sCtx.gitGrepArgs, sCtx.escapedSearchTerm, strings.Join(escapedFiles, " "))

	gitGrepOutput, err := sCtx.runner.RunCommand("sh", "-c", cmdStr)
	if err != nil {
		return "", fmt.Errorf("failed to execute git grep for global fallback: %w", err)
	}

	batchGitGrepOutput := gitGrepOutput.Stdout
	allCodeBlocks, err := ExtractSearchCodeBlocks(batchGitGrepOutput)
	if err != nil {
		return "", fmt.Errorf("failed to parse git grep output: %w", err)
	}

	fileStats := make(map[string]struct {
		totalCodeChars int
		numMatches     int
	})
	filesToTruncate := make(map[string]bool)

	if len(allCodeBlocks) > 0 {
		for _, block := range allCodeBlocks {
			if block.FilePath == "" {
				// This should ideally not happen with git grep --heading. Log if it does.
				log.Warn().Str("blockHeader", block.HeaderContent).Msg("ExtractSearchCodeBlocks produced a block with an empty FilePath from git grep output")
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

func updatedInputWithNoIgnore(input SearchRepositoryInput) SearchRepositoryInput {
	newInput := input
	newInput.NoIgnore = true
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
func SearchRepository(runner SearchRunner, input SearchRepositoryInput) (string, error) {
	sCtx, err := initSearchContext(runner, input)
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
		return addSearchPrefix(sCtx.input, processedOutput+sCtx.hiddenIgnoredDirsNote()), nil
	}

	// Output is genuinely empty (or cleared due to length checks but not returned early)
	if strings.TrimSpace(processedOutput) == "" {
		// Check for significant rg errors before attempting retries or fallback.
		// If cmdStderr is not empty and the error is critical (not a parse error or "no files searched"),
		// return the stderr directly. rg usually prefixes its own errors, so no addSearchPrefix here.
		if cmdStderr != "" && !isNonCriticalRgError(cmdStderr) {
			return cmdStderr, nil
		}

		if !sCtx.input.FixedStrings {
			return SearchRepository(runner, updatedInputWithFixedStrings(sCtx.input))
		}
		if !sCtx.input.CaseInsensitive {
			return SearchRepository(runner, updatedInputWithCaseInsensitive(sCtx.input))
		}

		// Retry with ignore files disabled for path-specific globs, so that
		// files in .gitignored or .sideignored directories can still be found.
		// Explicit-target searches already include ignored files, except under
		// excluded directories that the glob reaches only through a wildcard.
		if !sCtx.input.NoIgnore && hasLiteralPathSegment(sCtx.input.PathGlob) &&
			(!sCtx.includeIgnoredTargets || startsWithWildcardDir(sCtx.input.PathGlob)) {
			if !runner.Legacy("search-no-ignore-fallback") {
				return SearchRepository(runner, updatedInputWithNoIgnore(sCtx.input))
			}
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
		return addSearchPrefix(sCtx.input, processedOutput+sCtx.hiddenIgnoredDirsNote()), nil
	}
}
