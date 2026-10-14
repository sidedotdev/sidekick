// visibility_gap checks whether workflow runs that exist in Temporal's
// history store but are absent from visibility are still reachable through
// the API. The codec cleanup scan enumerates executions via visibility, so any
// run that is describable but not listed is a run whose codec references the
// scan would miss.
//
// Input is a CSV of "workflow_id,run_id" rows (no header). Output is a summary
// by describe outcome / status, and, with -sample, per-run detail for the first
// N rows of each outcome.
package main

import (
	"context"
	"encoding/csv"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"

	"sidekick"
	"sidekick/common"
)

type runOutcome struct {
	workflowID string
	runID      string
	outcome    string // "not_found", "describe_error", or the execution status
	closeTime  time.Time
	historyOK  bool
	historyErr string
	events     int
}

func main() {
	var csvPath string
	var sample int
	var historyLimit int
	flag.StringVar(&csvPath, "csv", "", "CSV of workflow_id,run_id rows (no header)")
	flag.IntVar(&sample, "sample", 3, "Per-outcome detail rows to print")
	flag.IntVar(&historyLimit, "history-limit", 50, "Read history for at most this many describable runs (0 = none)")
	flag.Parse()
	if csvPath == "" {
		fmt.Fprintln(os.Stderr, "-csv is required")
		os.Exit(2)
	}

	runs, err := readRuns(csvPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	service, err := sidekick.GetService()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to initialize service: %v\n", err)
		os.Exit(1)
	}
	clientOptions, err := common.NewTemporalClientOptions(service, common.GetTemporalDiagnosticHostPort())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create client options: %v\n", err)
		os.Exit(1)
	}
	c, err := client.Dial(clientOptions)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to dial Temporal: %v\n", err)
		os.Exit(1)
	}
	defer c.Close()

	ctx := context.Background()
	outcomes := make([]runOutcome, 0, len(runs))
	historyReads := 0
	for _, r := range runs {
		o := describeRun(ctx, c, r[0], r[1])
		if o.outcome != "not_found" && o.outcome != "describe_error" && historyReads < historyLimit {
			historyReads++
			o.events, o.historyErr = readHistory(ctx, c, r[0], r[1])
			o.historyOK = o.historyErr == ""
		}
		outcomes = append(outcomes, o)
	}
	printSummary(outcomes, sample, historyLimit)
}

func readRuns(path string) ([][2]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	reader := csv.NewReader(f)
	reader.FieldsPerRecord = -1
	var runs [][2]string
	for {
		rec, err := reader.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		if len(rec) < 2 {
			return nil, fmt.Errorf("read %s: expected workflow_id,run_id but got %q", path, rec)
		}
		runs = append(runs, [2]string{strings.TrimSpace(rec[0]), strings.TrimSpace(rec[1])})
	}
	if len(runs) == 0 {
		return nil, fmt.Errorf("read %s: no rows", path)
	}
	return runs, nil
}

func describeRun(ctx context.Context, c client.Client, workflowID, runID string) runOutcome {
	o := runOutcome{workflowID: workflowID, runID: runID}
	desc, err := c.DescribeWorkflowExecution(ctx, workflowID, runID)
	var notFound *serviceerror.NotFound
	switch {
	case errors.As(err, &notFound):
		o.outcome = "not_found"
	case err != nil:
		o.outcome = "describe_error"
		o.historyErr = err.Error()
	default:
		info := desc.GetWorkflowExecutionInfo()
		o.outcome = info.GetStatus().String()
		if info.GetCloseTime() != nil {
			o.closeTime = info.GetCloseTime().AsTime()
		}
	}
	return o
}

func readHistory(ctx context.Context, c client.Client, workflowID, runID string) (int, string) {
	iter := c.GetWorkflowHistory(ctx, workflowID, runID, false, enums.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
	n := 0
	for iter.HasNext() {
		if _, err := iter.Next(); err != nil {
			return n, err.Error()
		}
		n++
	}
	return n, ""
}

func printSummary(outcomes []runOutcome, sample, historyLimit int) {
	byOutcome := map[string][]runOutcome{}
	for _, o := range outcomes {
		byOutcome[o.outcome] = append(byOutcome[o.outcome], o)
	}
	names := make([]string, 0, len(byOutcome))
	for name := range byOutcome {
		names = append(names, name)
	}
	sort.Strings(names)

	fmt.Printf("%d runs checked\n", len(outcomes))
	for _, name := range names {
		group := byOutcome[name]
		historyOK, historyFail := 0, 0
		var oldestClose, newestClose time.Time
		for _, o := range group {
			if o.historyOK {
				historyOK++
			} else if o.historyErr != "" {
				historyFail++
			}
			if !o.closeTime.IsZero() {
				if oldestClose.IsZero() || o.closeTime.Before(oldestClose) {
					oldestClose = o.closeTime
				}
				if o.closeTime.After(newestClose) {
					newestClose = o.closeTime
				}
			}
		}
		fmt.Printf("\n%s: %d", name, len(group))
		if historyLimit > 0 && name != "not_found" && name != "describe_error" {
			fmt.Printf(" (history readable: %d, unreadable: %d, unchecked: %d)", historyOK, historyFail, len(group)-historyOK-historyFail)
		}
		if !oldestClose.IsZero() {
			fmt.Printf(" closed between %s and %s", oldestClose.Format(time.RFC3339), newestClose.Format(time.RFC3339))
		}
		fmt.Println()
		for i, o := range group {
			if i >= sample {
				break
			}
			line := fmt.Sprintf("  %s %s", o.workflowID, o.runID)
			if !o.closeTime.IsZero() {
				line += " closed=" + o.closeTime.Format(time.RFC3339)
			}
			if o.historyOK {
				line += fmt.Sprintf(" events=%d", o.events)
			}
			if o.historyErr != "" {
				line += " err=" + o.historyErr
			}
			fmt.Println(line)
		}
	}
}