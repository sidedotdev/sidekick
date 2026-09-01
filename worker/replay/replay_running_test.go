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
	"go.temporal.io/sdk/converter"
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
	id              string
	runID           string
	status          enums.WorkflowExecutionStatus
	sidekickVersion string
}

func listRecentRunningWorkflows(ctx context.Context, c client.Client, limit int) ([]listedWorkflow, error) {
	var results []listedWorkflow
	var nextPageToken []byte

	for {
		resp, err := c.ListWorkflow(ctx, &workflowservice.ListWorkflowExecutionsRequest{
			Query:         "ExecutionStatus = 'Running'",
			NextPageToken: nextPageToken,
		})
		if err != nil {
			return nil, err
		}
		for _, wfExec := range resp.Executions {
			var version string
			if wfExec.Memo != nil {
				if payload, ok := wfExec.Memo.Fields["sidekickVersion"]; ok {
					_ = converter.GetDefaultDataConverter().FromPayload(payload, &version)
				}
			}
			results = append(results, listedWorkflow{
				id:              wfExec.Execution.WorkflowId,
				runID:           wfExec.Execution.RunId,
				status:          wfExec.Status,
				sidekickVersion: version,
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

// isReplayableVersion reports whether the workflow's version ref is in the
// current branch's history, meaning it is safe to replay. The refs can be
// commit SHAs, tags, or any other valid git ref. Returns true when either ref
// is empty (graceful fallback) or when the workflow ref is an ancestor of (or
// equal to) the current ref.
func isReplayableVersion(currentRef, workflowRef string) bool {
	if currentRef == "" || workflowRef == "" {
		return true
	}
	if currentRef == workflowRef {
		return true
	}
	cmd := exec.Command("git", "merge-base", "--is-ancestor", workflowRef, currentRef)
	return cmd.Run() == nil
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
	// Inside a remote container the host's Temporal server is not reachable;
	// only its read-only proxy is reverse-forwarded (see port_forwards in
	// side.yml), which suffices here since this test only lists workflows and
	// reads histories. The default port may instead belong to a
	// container-local Temporal instance started via `side start`.
	temporalHostPort := common.GetTemporalServerHostPort()
	if common.IsActiveEnvNonLocal() {
		if forwardedPort, ok := common.LookupForwardedPort(common.GetTemporalReadOnlyServerPort()); ok {
			temporalHostPort = fmt.Sprintf("127.0.0.1:%d", forwardedPort)
		}
	}
	clientOptions, err := common.NewTemporalClientOptions(storage, temporalHostPort)
	if err != nil {
		t.Fatalf("Failed to create Temporal client options: %v", err)
	}
	clientOptions.Logger = logur.LoggerToKV(zerologadapter.New(log.Logger))
	c, err := client.Dial(clientOptions)
	if err != nil {
		// TODO(temporal-upgrade): remove this skip once the read-only Temporal
		// proxy is reliably reachable in sandbox/CI. The forwarded read-only
		// port can currently resolve to a non-Temporal endpoint, so a Dial
		// connectivity failure means the backing server is unavailable here
		// rather than a genuine replay regression.
		if strings.Contains(err.Error(), "failed reaching server") {
			t.Skipf("Skipping: Temporal server unreachable at %s: %v", temporalHostPort, err)
		}
		t.Fatalf("Failed to create Temporal client: %v", err)
	}
	defer c.Close()

	blacklist := loadBlacklist()

	// Fetch more than needed so we can fill up to maxWorkflowsToReplay after filtering
	fetchLimit := maxWorkflowsToReplay + len(blacklist)
	if fetchLimit < maxWorkflowsToReplay*2 {
		fetchLimit = maxWorkflowsToReplay * 2
	}
	listed, err := listRecentRunningWorkflows(ctx, c, fetchLimit)
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

	currentSha := common.GetBuildCommitSha()
	if currentSha != "" {
		t.Logf("Current build commit: %s", currentSha)
	} else {
		t.Logf("Current build commit SHA unavailable; skipping version-based filtering")
	}

	var filtered []listedWorkflow
	for _, wf := range listed {
		if wf.status != enums.WORKFLOW_EXECUTION_STATUS_RUNNING {
			continue
		}
		if _, ok := blacklist[wf.id]; ok {
			t.Logf("Skipping blacklisted workflow: %s", wf.id)
			continue
		}
		if !isReplayableVersion(currentSha, wf.sidekickVersion) {
			t.Logf("Skipping workflow %s: version %s is not in current branch history", wf.id, wf.sidekickVersion)
			continue
		}
		t.Logf("Replaying workflow %s: sidekickVersion=%q", wf.id, wf.sidekickVersion)
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
			if replayErr := replayer.ReplayWorkflowHistory(nil, hist); replayErr != nil {
				result.err = fmt.Errorf("replay: %w", replayErr)
			}
			<-replaySem
		}(wf)
	}

	wg.Wait()

	pruneHistoryCache(historyCacheDir, historyCacheMaxBytes, historyCacheMaxAge)

	for _, wf := range filtered {
		result := histories[wf.id]
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
