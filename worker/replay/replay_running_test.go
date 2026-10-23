package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sidekick"
	"strings"
	"sync"
	"testing"
	"time"

	"sidekick/common"
	"sidekick/utils"
	sidekick_worker "sidekick/worker"

	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/api/enums/v1"
	"go.temporal.io/api/history/v1"
	"go.temporal.io/api/proxy"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	zerologadapter "logur.dev/adapter/zerolog"
	"logur.dev/logur"
)

const (
	blacklistFileName    = "replay_blacklist.txt"
	maxWorkflowsToReplay = 20
)

func loadBlacklist() map[string]struct{} {
	blacklist := make(map[string]struct{})

	cacheHome, err := common.GetSidekickCacheHome()
	if err != nil {
		return blacklist
	}

	f, err := os.Open(filepath.Join(cacheHome, blacklistFileName))
	if err != nil {
		return blacklist
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" && !strings.HasPrefix(line, "#") {
			blacklist[line] = struct{}{}
		}
	}
	return blacklist
}

type listedWorkflow struct {
	id     string
	runID  string
	status enums.WorkflowExecutionStatus
}

func listRecentRunningWorkflows(ctx context.Context, svc workflowservice.WorkflowServiceClient, namespace string, limit int) ([]listedWorkflow, error) {
	var results []listedWorkflow
	var nextPageToken []byte

	for len(results) < limit {
		// ListWorkflow caps RPCs at ten seconds even with a longer parent
		// deadline. Reverse-forwarded visibility responses can exceed that.
		pageCtx, cancel := context.WithTimeout(ctx, time.Minute)
		resp, err := svc.ListWorkflowExecutions(pageCtx, &workflowservice.ListWorkflowExecutionsRequest{
			Namespace: namespace,
			// The server default can transfer hundreds of unneeded entries.
			PageSize:      int32(min(limit-len(results), 100)),
			Query:         "ExecutionStatus = 'Running'",
			NextPageToken: nextPageToken,
		})
		cancel()
		if err != nil {
			return nil, err
		}
		for _, wfExec := range resp.Executions {
			results = append(results, listedWorkflow{
				id:     wfExec.Execution.WorkflowId,
				runID:  wfExec.Execution.RunId,
				status: wfExec.Status,
			})
			if len(results) >= limit {
				return results, nil
			}
		}
		if len(resp.NextPageToken) == 0 {
			break
		}
		nextPageToken = resp.NextPageToken
	}
	return results, nil
}

// isDivergentBuild reports whether buildID names a commit, possibly built with
// uncommitted changes, that is neither head nor one of its ancestors, including
// commits this checkout lacks. Build IDs that don't name a commit, such as
// "unknown" or the Temporal SDK's binary checksum fallback, are never divergent.
func isDivergentBuild(head, buildID string) bool {
	commit := strings.TrimSuffix(buildID, common.DirtyWorkerBuildSuffix)
	if !isFullCommitID(head) || !isFullCommitID(commit) {
		return false
	}
	cmd := exec.Command("git", "merge-base", "--is-ancestor", commit, head)
	return cmd.Run() != nil
}

func isFullCommitID(ref string) bool {
	return (len(ref) == 40 || len(ref) == 64) && strings.Trim(ref, "0123456789abcdef") == ""
}

// TestReplayRunningWorkflows connects to the local Temporal server, fetches
// the most recently started running workflows, and replays each one against the
// current registered workflows. Workflows listed in
// $SIDE_CACHE_HOME/replay_blacklist.txt (one ID per line) are skipped; use
// `go run ./worker/replay blacklist <workflow_id>` to manage it.
func TestReplayRunningWorkflows(t *testing.T) {
	t.Parallel()
	if os.Getenv("SIDE_INTEGRATION_TEST") != "true" {
		t.Skip("Skipping integration test; SIDE_INTEGRATION_TEST not set")
	}

	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})

	ctx := context.Background()

	storage, err := sidekick.GetStorage()
	if err != nil {
		t.Fatalf("Failed to initialize storage for codec: %v", err)
	}
	// This test replays real histories, so it genuinely needs the host's
	// Temporal server. Inside a remote container only its read-only proxy is
	// reverse-forwarded (see port_forwards in side.yml), which suffices here
	// since the test only lists workflows and reads histories.
	temporalHostPort := common.GetTemporalDiagnosticHostPort()
	clientOptions, err := common.NewTemporalClientOptions(storage, temporalHostPort)
	if err != nil {
		t.Fatalf("Failed to create Temporal client options: %v", err)
	}
	clientOptions.Logger = logur.LoggerToKV(zerologadapter.New(log.Logger))
	c, err := client.Dial(clientOptions)
	if err != nil {
		t.Fatalf("Failed to create Temporal client: %v", err)
	}
	defer c.Close()

	blacklist := loadBlacklist()

	// Fetch more than needed so we can fill up to maxWorkflowsToReplay after filtering
	fetchLimit := maxWorkflowsToReplay + len(blacklist)
	if fetchLimit < maxWorkflowsToReplay*2 {
		fetchLimit = maxWorkflowsToReplay * 2
	}
	listed, err := listRecentRunningWorkflows(ctx, c.WorkflowService(), client.DefaultNamespace, fetchLimit)
	if err != nil {
		t.Fatalf("Failed to list running workflows: %v", err)
	}

	statusCounts := make(map[enums.WorkflowExecutionStatus]int)
	for _, wf := range listed {
		statusCounts[wf.status]++
	}
	t.Logf("Fetched %d workflows from visibility query; status breakdown: %v", len(listed), statusCounts)
	if nonRunning := len(listed) - statusCounts[enums.WORKFLOW_EXECUTION_STATUS_RUNNING]; nonRunning > 0 {
		t.Logf("WARNING: %d/%d workflows returned by Running query were not actually Running", nonRunning, len(listed))
	}

	head, err := exec.Command("git", "rev-parse", "--verify", "HEAD^{commit}").Output()
	if err != nil {
		t.Logf("Cannot resolve HEAD, so no replay failure will be excused: %v", err)
	}
	currentSha := strings.TrimSpace(string(head))
	t.Logf("Current HEAD: %s", currentSha)

	var filtered []listedWorkflow
	for _, wf := range listed {
		if wf.status != enums.WORKFLOW_EXECUTION_STATUS_RUNNING {
			continue
		}
		if _, ok := blacklist[wf.id]; ok {
			t.Logf("Skipping blacklisted workflow: %s", wf.id)
			continue
		}
		filtered = append(filtered, wf)
		if len(filtered) >= maxWorkflowsToReplay {
			break
		}
	}

	if len(filtered) == 0 {
		t.Logf("No running workflows to replay (fetched: %d, all blacklisted or non-running)", len(listed))
		return
	}

	t.Logf("Replaying %d most recent running workflows", len(filtered))

	// Fetch all histories concurrently, then replay concurrently in subtests.
	type historyResult struct {
		id         string
		err        error
		skipReason string
	}

	terminalEventTypes := map[enums.EventType]bool{
		enums.EVENT_TYPE_WORKFLOW_EXECUTION_COMPLETED:        true,
		enums.EVENT_TYPE_WORKFLOW_EXECUTION_FAILED:           true,
		enums.EVENT_TYPE_WORKFLOW_EXECUTION_TIMED_OUT:        true,
		enums.EVENT_TYPE_WORKFLOW_EXECUTION_CANCELED:         true,
		enums.EVENT_TYPE_WORKFLOW_EXECUTION_TERMINATED:       true,
		enums.EVENT_TYPE_WORKFLOW_EXECUTION_CONTINUED_AS_NEW: true,
	}

	var mu sync.Mutex
	histories := make(map[string]*historyResult)
	var wg sync.WaitGroup

	// HEAD is fixed for this invocation; only ancestry results are cached.
	buildFilter := newLatestBuildFilter(currentSha)

	// Limit concurrent replays to avoid CPU contention that triggers the
	// deadlock detector (which measures wall-clock time between yields).
	replaySem := make(chan struct{}, runtime.NumCPU())

	// Bound concurrent fetches: measured aggregate throughput to the
	// reverse-forwarded read-only proxy saturates around 5-10 MB/s regardless
	// of added concurrency, so more in-flight fetches only inflate memory
	// usage and per-page latency (risking RPC deadlines) without finishing
	// any sooner.
	fetchSem := make(chan struct{}, 4)

	// Running-workflow histories are append-only per run and a full replay
	// set totals hundreds of MB, so fetched prefixes are persisted (on Modal
	// the cache home is a volume shared across sandboxes) and later runs
	// resume from the last page instead of re-transferring everything.
	historyCacheDir := ""
	if cacheHome, err := common.GetSidekickCacheHome(); err == nil {
		historyCacheDir = filepath.Join(cacheHome, historyCacheDirName)
	}

	for _, wf := range filtered {
		wg.Add(1)
		go func(wf listedWorkflow) {
			defer wg.Done()

			result := &historyResult{id: wf.id}
			defer func() {
				mu.Lock()
				histories[wf.id] = result
				mu.Unlock()
			}()

			fetchSem <- struct{}{}
			var hist *history.History
			var resumedFromCache bool
			err := retryTransient(3, 2*time.Second, func() error {
				var fetchErr error
				hist, resumedFromCache, fetchErr = fetchWorkflowHistoryCached(
					ctx, c.WorkflowService(), client.DefaultNamespace, historyCacheDir, wf.id, wf.runID)
				return fetchErr
			})
			<-fetchSem
			if err != nil {
				result.err = fmt.Errorf("history fetch: %w", err)
				return
			}
			if resumedFromCache {
				t.Logf("Workflow %s: history fetch resumed from local cache", wf.id)
			}
			if events := hist.Events; len(events) > 0 && terminalEventTypes[events[len(events)-1].EventType] {
				result.skipReason = "completed before replay"
				return
			}
			// Drop any in-flight WorkflowTask tail to avoid the
			// "extra replay command" false positive caused by reading
			// history mid workflow-task on a running workflow.
			hist.Events = dropInFlightWFTTail(hist.Events)
			if len(hist.Events) == 0 {
				result.skipReason = "no replayable events"
				return
			}

			// Resolve payloads the codec offloaded to KV storage up front:
			// unresolvable references (e.g. histories read over the read-only
			// proxy of a host that does not inline them) would otherwise
			// derail replay with misleading non-determinism errors.
			codec := common.NewPayloadCodec(storage, common.DefaultCodecThreshold)
			err = proxy.VisitPayloads(ctx, hist, proxy.VisitPayloadsOptions{
				SkipSearchAttributes: true,
				Visitor: func(_ *proxy.VisitPayloadsContext, payloads []*commonpb.Payload) ([]*commonpb.Payload, error) {
					return codec.Decode(payloads)
				},
			})
			if err != nil {
				if errors.Is(err, common.ErrCodecPayloadMissing) {
					result.skipReason = fmt.Sprintf("offloaded payloads unavailable in this environment: %v", err)
					return
				}
				result.err = fmt.Errorf("payload decode: %w", err)
				return
			}
			replayerOptions := utils.TestReplayerOptions()
			replayerOptions.DataConverter = clientOptions.DataConverter
			replayer, err := worker.NewWorkflowReplayerWithOptions(replayerOptions)
			if err != nil {
				result.err = fmt.Errorf("replayer setup: %w", err)
				return
			}
			sidekick_worker.RegisterWorkflows(replayer)
			replaySem <- struct{}{}
			replayErr := replayer.ReplayWorkflowHistory(nil, hist)
			<-replaySem
			if replayErr == nil {
				return
			}
			buildID, excuse := buildFilter.excuse(ctx, c.DescribeWorkflowExecution, wf.id, wf.runID)
			if excuse != "" {
				result.skipReason = fmt.Sprintf("replay failure excused (%s): %v", excuse, replayErr)
				return
			}
			result.err = fmt.Errorf("replay (latest worker build %q): %w", buildID, replayErr)
		}(wf)
	}

	wg.Wait()

	pruneHistoryCache(historyCacheDir, historyCacheMaxBytes, historyCacheMaxAge)

	conclusive := 0
	for _, wf := range filtered {
		result := histories[wf.id]
		if result.skipReason == "" {
			conclusive++
		}
		t.Run(wf.id, func(t *testing.T) {
			t.Parallel()
			if result.skipReason != "" {
				t.Skipf("Workflow %s: %s", result.id, result.skipReason)
			}
			if result.err != nil {
				t.Errorf("Replay failed for workflow %s: %v", result.id, result.err)
			}
		})
	}
	// A run where every workflow is skipped verifies nothing and must not pass.
	if conclusive == 0 {
		t.Errorf("None of the %d running workflows were conclusively replayed; see subtest skip reasons", len(filtered))
	}
}

// retryTransient runs fn up to attempts times, retrying only errors that
// isTransientHistoryFetchErr classifies as transient and sleeping between
// attempts. It returns fn's last error.
func retryTransient(attempts int, sleep time.Duration, fn func() error) error {
	var err error
	for i := 0; i < attempts; i++ {
		if i > 0 {
			time.Sleep(sleep)
		}
		err = fn()
		if err == nil || !isTransientHistoryFetchErr(err) {
			return err
		}
	}
	return err
}

// isTransientHistoryFetchErr reports whether fetching a workflow history
// failed for environmental reasons (slow or unavailable transport) rather than
// anything replay-related, e.g. RPC deadlines exceeded while reading over the
// reverse-forwarded read-only Temporal proxy in sandboxes.
func isTransientHistoryFetchErr(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var deadline *serviceerror.DeadlineExceeded
	var unavailable *serviceerror.Unavailable
	return errors.As(err, &deadline) || errors.As(err, &unavailable)
}

// dropInFlightWFTTail removes a trailing in-flight WorkflowTask
// (WorkflowTaskScheduled, optionally followed by WorkflowTaskStarted, with no
// terminator yet) from a fetched history. This avoids the "extra replay
// command" false positive that occurs when a running workflow's history is
// read mid workflow-task: replay would advance workflow code past
// WorkflowTaskStarted and generate pending commands that do not yet appear
// as events.
//
// Crucially, it does NOT cut the events that follow the last completed WFT
// (ActivityTaskScheduled, TimerStarted, ...), since those events are
// materialized by the server as part of that WFT's completion and are
// required for the replayer to match the commands it regenerates.
func dropInFlightWFTTail(events []*history.HistoryEvent) []*history.HistoryEvent {
	lastScheduledIdx := -1
	for i, e := range events {
		if e.EventType == enums.EVENT_TYPE_WORKFLOW_TASK_SCHEDULED {
			lastScheduledIdx = i
		}
	}
	if lastScheduledIdx < 0 {
		return events
	}
	for _, e := range events[lastScheduledIdx+1:] {
		switch e.EventType {
		case enums.EVENT_TYPE_WORKFLOW_TASK_COMPLETED,
			enums.EVENT_TYPE_WORKFLOW_TASK_FAILED,
			enums.EVENT_TYPE_WORKFLOW_TASK_TIMED_OUT:
			return events
		}
	}
	return events[:lastScheduledIdx]
}

func TestIsTransientHistoryFetchErr(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"wrapped context deadline", fmt.Errorf("fetching history: %w", context.DeadlineExceeded), true},
		{"serviceerror deadline exceeded", serviceerror.NewDeadlineExceeded("context deadline exceeded"), true},
		{"serviceerror unavailable", serviceerror.NewUnavailable("proxy unreachable"), true},
		{"unrelated error", errors.New("non-determinism detected"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := isTransientHistoryFetchErr(tc.err); got != tc.want {
				t.Errorf("isTransientHistoryFetchErr(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestRetryTransient(t *testing.T) {
	t.Parallel()

	t.Run("retries transient errors until success", func(t *testing.T) {
		t.Parallel()
		calls := 0
		err := retryTransient(3, 0, func() error {
			calls++
			if calls < 3 {
				return context.DeadlineExceeded
			}
			return nil
		})
		if err != nil {
			t.Errorf("expected success, got %v", err)
		}
		if calls != 3 {
			t.Errorf("expected 3 calls, got %d", calls)
		}
	})

	t.Run("does not retry non-transient errors", func(t *testing.T) {
		t.Parallel()
		calls := 0
		wantErr := errors.New("non-determinism detected")
		err := retryTransient(3, 0, func() error {
			calls++
			return wantErr
		})
		if !errors.Is(err, wantErr) {
			t.Errorf("expected %v, got %v", wantErr, err)
		}
		if calls != 1 {
			t.Errorf("expected 1 call, got %d", calls)
		}
	})

	t.Run("returns last transient error after exhausting attempts", func(t *testing.T) {
		t.Parallel()
		calls := 0
		err := retryTransient(3, 0, func() error {
			calls++
			return context.DeadlineExceeded
		})
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("expected deadline exceeded, got %v", err)
		}
		if calls != 3 {
			t.Errorf("expected 3 calls, got %d", calls)
		}
	})
}

// latestBuildFilter excuses replay failures of workflows last run by a worker
// built from code HEAD lacks, since those may legitimately depend on it. It
// only excuses provably divergent builds: excusing unidentifiable ones too
// would let real nondeterminism pass silently. The latest worker build is a
// practical signal, not an audit of earlier workers.
type latestBuildFilter struct {
	head      string
	mu        sync.Mutex
	divergent map[string]bool
}

func newLatestBuildFilter(head string) *latestBuildFilter {
	return &latestBuildFilter{head: head, divergent: make(map[string]bool)}
}

func (f *latestBuildFilter) excuse(
	ctx context.Context,
	describe func(context.Context, string, string) (*workflowservice.DescribeWorkflowExecutionResponse, error),
	id, runID string,
) (buildID, excuse string) {
	description, err := describe(ctx, id, runID)
	if err != nil {
		return "", ""
	}
	buildID = description.GetWorkflowExecutionInfo().GetMostRecentWorkerVersionStamp().GetBuildId()
	f.mu.Lock()
	defer f.mu.Unlock()
	divergent, known := f.divergent[buildID]
	if !known {
		divergent = isDivergentBuild(f.head, buildID)
		f.divergent[buildID] = divergent
	}
	if divergent {
		return buildID, fmt.Sprintf("latest worker build %q is neither HEAD nor an ancestor of it", buildID)
	}
	return buildID, ""
}
