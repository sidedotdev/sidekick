// check_review_diffs validates the review diffs recorded in a workflow's
// GenerateReviewDiffsActivity results, as dumped by
// `dump_workflow_events -verbose -activity-type GenerateReviewDiffsActivity`,
// reporting for each result whether the interdiff parser accepts it and which
// files it touches. This pinpoints why a step review fell back to the full
// diff.
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"sidekick/coding"
	"sidekick/coding/diffanalysis"
)

func main() {
	showFiles := flag.Bool("files", false, "list the files touched by each diff")
	flag.Parse()
	if flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: check_review_diffs [-files] <dump-file>")
		os.Exit(2)
	}

	f, err := os.Open(flag.Arg(0))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 1024*1024), 256*1024*1024)
	const prefix = "Result[0]: "
	found := 0
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		idx := strings.Index(line, prefix)
		if idx < 0 || !strings.HasPrefix(line[idx+len(prefix):], "{") {
			continue
		}
		var result coding.GenerateReviewDiffsResult
		if err := json.Unmarshal([]byte(line[idx+len(prefix):]), &result); err != nil {
			fmt.Fprintf(os.Stderr, "failed to decode result: %v\n", err)
			os.Exit(1)
		}
		found++
		report(found, result, *showFiles)
	}
	if err := scanner.Err(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if found == 0 {
		fmt.Fprintln(os.Stderr, "no GenerateReviewDiffsActivity results found in dump")
		os.Exit(1)
	}
}

func report(n int, result coding.GenerateReviewDiffsResult, showFiles bool) {
	files, parseErr := diffanalysis.ParseUnifiedDiff(result.FullDiff)
	rawGit := strings.Contains(result.FullDiff, "\nindex ") || strings.HasPrefix(result.FullDiff, "index ")
	_, interdiffErr := diffanalysis.Interdiff(result.FullDiff, result.FullDiff)

	fmt.Printf("#%d fullDiff=%d bytes files=%d rawGitOutput=%v sinceDiffError=%q\n", n, len(result.FullDiff), len(files), rawGit, result.SinceDiffError)
	if parseErr != nil {
		fmt.Printf("    parse error: %v\n", parseErr)
	}
	if interdiffErr != nil {
		fmt.Printf("    interdiff self-check error: %v\n", interdiffErr)
	}
	if showFiles {
		for _, file := range files {
			kind := ""
			switch {
			case file.IsBinary:
				kind = " (binary)"
			case len(file.Hunks) == 0:
				kind = " (no hunks)"
			}
			fmt.Printf("    %s -> %s hunks=%d%s\n", file.OldPath, file.NewPath, len(file.Hunks), kind)
		}
	}
}
