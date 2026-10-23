// compare_review_diffs renders a three-diff review comparison from a JSON
// object containing prior, current, and baseChange patch strings. Input may be
// gzip-compressed. The patch goes to stdout and diagnostics go to stderr.
package main

import (
	"compress/gzip"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"sidekick/coding/diffanalysis"
)

func main() {
	showFiles := flag.Bool("files", false, "list compared files on stderr")
	flag.Parse()
	if flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: compare_review_diffs [-files] <comparison.json[.gz]>")
		os.Exit(2)
	}
	if err := compare(flag.Arg(0), *showFiles); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func compare(path string, showFiles bool) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	var reader io.Reader = file
	if strings.HasSuffix(path, ".gz") {
		compressed, err := gzip.NewReader(file)
		if err != nil {
			return err
		}
		defer compressed.Close()
		reader = compressed
	}
	var input struct {
		Prior      *string `json:"prior"`
		Current    *string `json:"current"`
		BaseChange *string `json:"baseChange"`
	}
	decoder := json.NewDecoder(reader)
	if err := decoder.Decode(&input); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("expected one comparison object, trailing input: %v", err)
	}
	if input.Prior == nil || input.Current == nil || input.BaseChange == nil {
		return fmt.Errorf("prior, current, and baseChange must all be present")
	}
	patch, err := diffanalysis.InterdiffWithBaseChange(*input.Prior, *input.Current, *input.BaseChange)
	if err != nil {
		return err
	}
	files, err := diffanalysis.ParseUnifiedDiff(patch)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "since-review diff: %d bytes, %d files\n", len(patch), len(files))
	if showFiles {
		for _, file := range files {
			fmt.Fprintf(os.Stderr, "%s -> %s (%d hunks)\n", file.OldPath, file.NewPath, len(file.Hunks))
		}
	}
	_, err = io.WriteString(os.Stdout, patch)
	return err
}
