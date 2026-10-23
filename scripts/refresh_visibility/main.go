// refresh_visibility re-indexes workflow runs that exist in Temporal's history
// store but are missing from visibility (see scripts/visibility_gap). It calls
// the admin RefreshWorkflowTasks RPC, which regenerates the run's transfer
// tasks including the visibility start/close task, without adding history
// events. For each run it records status and history length before and after,
// and polls ListWorkflow until the run is listed.
//
// Dry-run by default: prints what would be refreshed. Pass -apply to mutate.
package main

import (
	"context"
	"encoding/csv"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/server/api/adminservice/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"sidekick"
	"sidekick/common"
)

func main() {
	var csvPath string
	var apply bool
	var limit int
	var listTimeout time.Duration
	flag.StringVar(&csvPath, "csv", "", "CSV of workflow_id,run_id rows (no header)")
	flag.BoolVar(&apply, "apply", false, "Actually call RefreshWorkflowTasks (default is dry run)")
	flag.IntVar(&limit, "limit", 0, "Process at most this many runs (0 = all)")
	flag.DurationVar(&listTimeout, "list-timeout", 30*time.Second, "How long to wait for a refreshed run to appear in ListWorkflow")
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
	if limit > 0 && len(runs) > limit {
		runs = runs[:limit]
	}

	service, err := sidekick.GetService()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to initialize service: %v\n", err)
		os.Exit(1)
	}
	// The read-only diagnostic proxy does not forward admin RPCs, so dial the
	// real frontend.
	hostPort := common.GetTemporalServerHostPort()
	clientOptions, err := common.NewTemporalClientOptions(service, hostPort)
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
	r, err := NewRefresher(ctx, c, hostPort, client.DefaultNamespace)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer r.Close()

	failures, gone := 0, 0
	for i, run := range runs {
		res, err := r.Refresh(ctx, run[0], run[1], apply, listTimeout)
		if errors.Is(err, errRunGone) {
			// Retention purged it since the manifest was taken; nothing can
			// reference such a run any more.
			gone++
			fmt.Printf("[%d/%d] %s %s: gone\n", i+1, len(runs), run[0], run[1])
			continue
		}
		if err != nil {
			failures++
			fmt.Printf("[%d/%d] %s %s: ERROR %v\n", i+1, len(runs), run[0], run[1], err)
			if apply {
				// A refresh that misbehaves must be looked at before touching
				// more live runs.
				fmt.Println("\nStopping after first failure in apply mode")
				os.Exit(1)
			}
			continue
		}
		fmt.Printf("[%d/%d] %s %s: %s\n", i+1, len(runs), run[0], run[1], res)
	}
	if !apply {
		fmt.Printf("\nDry run: %d of %d runs would be refreshed (%d gone). Pass -apply to do it.\n", len(runs)-failures-gone, len(runs), gone)
	} else {
		fmt.Printf("\n%d of %d runs processed (%d gone)\n", len(runs)-failures-gone, len(runs), gone)
	}
	if failures > 0 {
		fmt.Printf("\n%d failures\n", failures)
		os.Exit(1)
	}
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
		if len(rec) != 2 {
			return nil, fmt.Errorf("read %s: expected exactly workflow_id,run_id but got %q", path, rec)
		}
		workflowID, runID := strings.TrimSpace(rec[0]), strings.TrimSpace(rec[1])
		if workflowID == "" || runID == "" {
			return nil, fmt.Errorf("read %s: empty workflow or run id in %q", path, rec)
		}
		runs = append(runs, [2]string{workflowID, runID})
	}
	if len(runs) == 0 {
		return nil, fmt.Errorf("read %s: no rows", path)
	}
	return runs, nil
}

// Refresher re-indexes individual runs via the admin service.
type Refresher struct {
	client      client.Client
	conn        *grpc.ClientConn
	admin       adminservice.AdminServiceClient
	namespaceID string
}

func NewRefresher(ctx context.Context, c client.Client, hostPort, namespace string) (*Refresher, error) {
	desc, err := c.WorkflowService().DescribeNamespace(ctx, &workflowservice.DescribeNamespaceRequest{Namespace: namespace})
	if err != nil {
		return nil, fmt.Errorf("describe namespace %q: %w", namespace, err)
	}
	conn, err := grpc.NewClient(hostPort, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("dial admin service at %s: %w", hostPort, err)
	}
	return &Refresher{
		client:      c,
		conn:        conn,
		admin:       adminservice.NewAdminServiceClient(conn),
		namespaceID: desc.GetNamespaceInfo().GetId(),
	}, nil
}

func (r *Refresher) Close() {
	_ = r.conn.Close()
}

const rpcTimeout = 20 * time.Second

// RefreshResult records what a refresh observed. Listed is whether the run
// appeared in ListWorkflow (after the refresh when applied, otherwise now).
type RefreshResult struct {
	Status        string
	HistoryLength int64
	ListedBefore  bool
	Listed        bool
	Applied       bool
	Skipped       bool
}

func (res RefreshResult) String() string {
	action := "would refresh"
	switch {
	case res.Skipped:
		action = "already listed, skipped"
	case res.Applied:
		action = "refreshed"
	}
	return fmt.Sprintf("%s status=%s events=%d listedBefore=%t listedAfter=%t", action, res.Status, res.HistoryLength, res.ListedBefore, res.Listed)
}

type runSnapshot struct {
	status        string
	historyLength int64
}

// errRunGone means Temporal no longer has the run at all.
var errRunGone = errors.New("workflow run not found")

func (r *Refresher) snapshot(ctx context.Context, workflowID, runID string) (runSnapshot, error) {
	ctx, cancel := context.WithTimeout(ctx, rpcTimeout)
	defer cancel()
	desc, err := r.client.DescribeWorkflowExecution(ctx, workflowID, runID)
	var notFound *serviceerror.NotFound
	if errors.As(err, &notFound) {
		return runSnapshot{}, errRunGone
	}
	if err != nil {
		return runSnapshot{}, fmt.Errorf("describe: %w", err)
	}
	info := desc.GetWorkflowExecutionInfo()
	return runSnapshot{status: info.GetStatus().String(), historyLength: info.GetHistoryLength()}, nil
}

// Refresh re-indexes one run that is not currently listed. It fails if the
// run's status or history length changes across the refresh (checked both
// immediately and again once the asynchronous visibility task has landed), or
// if the run does not become listable within listTimeout.
func (r *Refresher) Refresh(ctx context.Context, workflowID, runID string, apply bool, listTimeout time.Duration) (RefreshResult, error) {
	before, err := r.snapshot(ctx, workflowID, runID)
	if err != nil {
		return RefreshResult{}, err
	}
	// From here on a vanishing run is a real failure, not a purge to skip.
	res := RefreshResult{Status: before.status, HistoryLength: before.historyLength}
	res.ListedBefore, err = r.isListed(ctx, workflowID, runID)
	if err != nil {
		return RefreshResult{}, err
	}
	res.Listed = res.ListedBefore
	if res.ListedBefore {
		res.Skipped = true
		return res, nil
	}
	if !apply {
		return res, nil
	}

	rpcCtx, cancel := context.WithTimeout(ctx, rpcTimeout)
	_, err = r.admin.RefreshWorkflowTasks(rpcCtx, &adminservice.RefreshWorkflowTasksRequest{
		NamespaceId: r.namespaceID,
		Execution:   &commonpb.WorkflowExecution{WorkflowId: workflowID, RunId: runID},
	})
	cancel()
	if err != nil {
		return RefreshResult{}, fmt.Errorf("RefreshWorkflowTasks: %w", err)
	}
	res.Applied = true

	if err := r.verifyUnchanged(ctx, workflowID, runID, before, "immediately after refresh"); err != nil {
		return RefreshResult{}, err
	}

	deadline := time.Now().Add(listTimeout)
	for {
		res.Listed, err = r.isListed(ctx, workflowID, runID)
		if err != nil {
			return RefreshResult{}, err
		}
		if res.Listed || time.Now().After(deadline) {
			break
		}
		select {
		case <-ctx.Done():
			return RefreshResult{}, ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	if !res.Listed {
		return RefreshResult{}, fmt.Errorf("run still not listed %s after refresh", listTimeout)
	}
	if err := r.verifyUnchanged(ctx, workflowID, runID, before, "after visibility task landed"); err != nil {
		return RefreshResult{}, err
	}
	return res, nil
}

func (r *Refresher) verifyUnchanged(ctx context.Context, workflowID, runID string, before runSnapshot, when string) error {
	after, err := r.snapshot(ctx, workflowID, runID)
	if errors.Is(err, errRunGone) {
		return fmt.Errorf("%s: run disappeared", when)
	}
	if err != nil {
		return fmt.Errorf("%s: %w", when, err)
	}
	if after.status != before.status {
		return fmt.Errorf("status changed %s: %s -> %s", when, before.status, after.status)
	}
	if after.historyLength != before.historyLength {
		return fmt.Errorf("history length changed %s: %d -> %d", when, before.historyLength, after.historyLength)
	}
	return nil
}

func (r *Refresher) isListed(ctx context.Context, workflowID, runID string) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, rpcTimeout)
	defer cancel()
	resp, err := r.client.ListWorkflow(ctx, &workflowservice.ListWorkflowExecutionsRequest{
		Query: fmt.Sprintf("WorkflowId = %s AND RunId = %s", quoteQueryLiteral(workflowID), quoteQueryLiteral(runID)),
	})
	var notFound *serviceerror.NotFound
	if errors.As(err, &notFound) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("list %s/%s: %w", workflowID, runID, err)
	}
	return len(resp.GetExecutions()) > 0, nil
}

// quoteQueryLiteral renders a string for a visibility list filter.
func quoteQueryLiteral(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}
