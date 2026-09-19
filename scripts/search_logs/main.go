// Command search_logs searches Sidekick's retained daily host logs. Retention
// is a rolling window of daily files, so it also reports what period was
// actually covered: "no matches" and "the logs for that period are gone" are
// very different answers when investigating an old flow.
//
// That distinction is carried by both the exit code (0 matches, 1 none, 2
// unanswerable) and an explicit stderr line. Prefer the stderr line when
// invoking via "go run", which collapses every non-zero exit to 1.
package main

import (
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"sidekick/common"
)

// fieldFilters collects repeated -field name=value flags.
type fieldFilters map[string]string

func (f fieldFilters) String() string {
	pairs := make([]string, 0, len(f))
	for name, value := range f {
		pairs = append(pairs, name+"="+value)
	}
	return strings.Join(pairs, ",")
}

func (f fieldFilters) Set(value string) error {
	name, want, err := parseFieldFilter(value)
	if err != nil {
		return err
	}
	f[name] = want
	return nil
}

const (
	exitMatches      = 0
	exitNoMatches    = 1
	exitUnanswerable = 2
)

func main() {
	dir := flag.String("dir", "", "log directory (defaults to the Sidekick state home)")
	since := flag.String("since", "", "only lines at or after this date (2006-01-02) or duration ago (e.g. 48h)")
	until := flag.String("until", "", "only lines at or before this date (2006-01-02) or duration ago (e.g. 1h)")
	max := flag.Int("max", 0, "stop after this many matching lines (0 = unlimited)")
	caseSensitive := flag.Bool("case-sensitive", false, "match terms case-sensitively")
	fields := fieldFilters{}
	flag.Var(fields, "field", "structured field filter, e.g. WorkflowID=flow_abc (repeatable)")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "Usage: %s [flags] [term...]\n", os.Args[0])
		fmt.Fprintf(flag.CommandLine.Output(), "All terms and field filters must hold for a line to match.\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	if flag.NArg() == 0 && len(fields) == 0 {
		flag.Usage()
		os.Exit(exitUnanswerable)
	}

	logDir := *dir
	if logDir == "" {
		stateHome, err := common.GetSidekickStateHome()
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to resolve Sidekick state home: %v\n", err)
			os.Exit(exitUnanswerable)
		}
		logDir = stateHome
	}

	now := time.Now()
	query := Query{Terms: flag.Args(), CaseSensitive: *caseSensitive, Fields: fields, Max: *max}
	var err error
	if *since != "" {
		if query.Since, err = parseTimeBound(*since, now, boundSince); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(exitUnanswerable)
		}
	}
	if *until != "" {
		if query.Until, err = parseTimeBound(*until, now, boundUntil); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(exitUnanswerable)
		}
	}

	files, err := discoverLogFiles(logDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(exitUnanswerable)
	}
	if len(files) == 0 {
		fmt.Fprintf(os.Stderr, "no %s*%s files in %s: nothing to search\n", logFilePrefix, logFileSuffix, logDir)
		os.Exit(exitUnanswerable)
	}

	result, err := search(files, query, os.Stdout)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(exitUnanswerable)
	}

	criteria := append([]string{}, query.Terms...)
	for name, value := range fields {
		criteria = append(criteria, name+"="+value)
	}
	sort.Strings(criteria)
	fmt.Fprintf(os.Stderr, "\n%d matching line(s) for [%s]\n", result.Matches, strings.Join(criteria, " "))
	fmt.Fprintf(os.Stderr, "retained logs: %d file(s) covering %s..%s in %s\n",
		len(files), result.Oldest.Format(dateLayout), result.Newest.Format(dateLayout), logDir)
	fmt.Fprintf(os.Stderr, "searched: %d file(s)\n", len(result.FilesSearched))

	switch result.Coverage {
	case CoverageNone:
		fmt.Fprintf(os.Stderr,
			"UNANSWERABLE: requested period ends %s, entirely before the oldest retained log (%s); those logs were rotated away, so this search says nothing about that period\n",
			query.Until.Format(dateLayout), result.Oldest.Format(dateLayout))
		os.Exit(exitUnanswerable)
	case CoveragePartial:
		fmt.Fprintf(os.Stderr,
			"WARNING: requested period starts %s, before the oldest retained log (%s); logs for that period were rotated away, so this search cannot answer questions about it\n",
			query.Since.Format(dateLayout), result.Oldest.Format(dateLayout))
		if result.Matches == 0 {
			os.Exit(exitUnanswerable)
		}
	}
	if result.Matches == 0 {
		os.Exit(exitNoMatches)
	}
	os.Exit(exitMatches)
}
