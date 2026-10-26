package main

import (
	"bufio"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
	id            string
	runID         string
	status        enums.WorkflowExecutionStatus
	workerBuildID string
}

func listRecentRunningWorkflows(ctx context.Context, svc workflowservice.WorkflowServiceClient, namespace string, limit int) ([]listedWorkflow, error) {
	var results []listedWorkflow
	err := visitRunningWorkflowCandidates(ctx, svc, namespace, limit, func(wf listedWorkflow) bool {
		results = append(results, wf)
		return true
	})
	if err != nil {
		return nil, err
	}
	return results, nil
}

// isReplayableVersion requires both refs to resolve to commits and the worker
// revision to be an ancestor of, or equal to, the current revision.
func isReplayableVersion(currentRef, workflowRef string) bool {
	if currentRef == "" || workflowRef == "" {
		return false
	}
	cmd := exec.Command("git", "merge-base", "--is-ancestor", "--", workflowRef, currentRef)
	return cmd.Run() == nil
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

	head, err := exec.Command("git", "rev-parse", "--verify", "HEAD^{commit}").Output()
	if err != nil {
		t.Fatalf("Cannot resolve HEAD to establish replay ancestry: %v", err)
	}
	currentSha := strings.TrimSpace(string(head))
	t.Logf("Current HEAD: %s", currentSha)

	// Store outcomes rather than histories to avoid retaining decoded payloads.
	type historyResult struct {
		id         string
		err        error
		skipReason string
		replayed   bool
	}

	terminalEventTypes := map[enums.EventType]bool{
		enums.EVENT_TYPE_WORKFLOW_EXECUTION_COMPLETED:        true,
		enums.EVENT_TYPE_WORKFLOW_EXECUTION_FAILED:           true,
		enums.EVENT_TYPE_WORKFLOW_EXECUTION_TIMED_OUT:        true,
		enums.EVENT_TYPE_WORKFLOW_EXECUTION_CANCELED:         true,
		enums.EVENT_TYPE_WORKFLOW_EXECUTION_TERMINATED:       true,
		enums.EVENT_TYPE_WORKFLOW_EXECUTION_CONTINUED_AS_NEW: true,
	}

	var histories []*historyResult
	statusCounts := make(map[enums.WorkflowExecutionStatus]int)
	examined := 0

	// HEAD is fixed for this invocation; only ancestry results are cached.
	buildFilter := newLatestBuildFilter(currentSha)

	// Running-workflow histories are append-only per run and a full replay
	// set totals hundreds of MB, so fetched prefixes are persisted (on Modal
	// the cache home is a volume shared across sandboxes) and later runs
	// resume from the last page instead of re-transferring everything.
	historyCacheDir := ""
	if cacheHome, err := common.GetSidekickCacheHome(); err == nil {
		historyCacheDir = filepath.Join(cacheHome, historyCacheDirName)
	}

	// Histories and decoded payloads can occupy hundreds of MiB. Visiting
	// synchronously bounds their entire lifetime, not just network fetching.
	err = visitRunningWorkflowCandidates(ctx, c.WorkflowService(), client.DefaultNamespace, maxWorkflowsToReplay, func(wf listedWorkflow) (replayed bool) {
		examined++
		statusCounts[wf.status]++
		if reason := replayCandidateSkipReason(wf, blacklist); reason != "" {
			t.Logf("Skipping workflow %s: %s", wf.id, reason)
			return
		}

		result := &historyResult{id: wf.id}
		defer func() {
			histories = append(histories, result)
			replayed = result.replayed
		}()

		var hist *history.History
		var resumedFromCache bool
		var skipReason string
		err := retryTransient(3, 2*time.Second, func() error {
			var fetchErr error
			skipReason, fetchErr = replayHistoryPreflight(
				ctx, c.WorkflowService(), client.DefaultNamespace, wf.id, wf.runID, currentSha)
			if fetchErr != nil || skipReason != "" {
				return fetchErr
			}
			hist, resumedFromCache, fetchErr = fetchWorkflowHistoryCached(
				ctx, c.WorkflowService(), client.DefaultNamespace, historyCacheDir, wf.id, wf.runID)
			return fetchErr
		})
		if err != nil {
			result.err = fmt.Errorf("history fetch: %w", err)
			return
		}
		if skipReason != "" {
			result.skipReason = "history preflight: " + skipReason
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
		if reason := replayHistorySkipReason(currentSha, hist); reason != "" {
			result.skipReason = "fetched history: " + reason
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
		result.replayed = true
		replayErr := replayer.ReplayWorkflowHistory(nil, hist)
		if replayErr == nil {
			return
		}
		buildID, excuse := buildFilter.excuse(ctx, c.DescribeWorkflowExecution, wf.id, wf.runID)
		if excuse != "" {
			result.skipReason = fmt.Sprintf("replay failure excused (%s): %v", excuse, replayErr)
			return
		}
		result.err = fmt.Errorf("replay (latest worker build %q): %w", buildID, replayErr)
		return
	})
	if err != nil {
		t.Errorf("Failed to visit running workflows: %v", err)
	}
	t.Logf("Examined %d workflows from visibility query; status breakdown: %v", examined, statusCounts)
	if nonRunning := examined - statusCounts[enums.WORKFLOW_EXECUTION_STATUS_RUNNING]; nonRunning > 0 {
		t.Logf("WARNING: %d/%d workflows returned by Running query were not actually Running", nonRunning, examined)
	}

	pruneHistoryCache(historyCacheDir, historyCacheMaxBytes, historyCacheMaxAge)

	if len(histories) == 0 && err == nil {
		t.Skip("No histories replayed: no running, non-blacklisted candidates")
	}
	replayed, skipped, failedBeforeReplay := 0, 0, 0
	conclusive := 0
	for _, result := range histories {
		if result.replayed {
			replayed++
		}
		if result.skipReason != "" {
			skipped++
		} else {
			conclusive++
			if !result.replayed {
				failedBeforeReplay++
			}
		}
		t.Run(result.id, func(t *testing.T) {
			t.Parallel()
			if result.skipReason != "" {
				t.Skipf("Workflow %s: %s", result.id, result.skipReason)
			}
			if result.err != nil {
				t.Errorf("Replay failed for workflow %s: %v", result.id, result.err)
			}
		})
	}
	t.Logf("History replay: %d attempted, %d skipped, %d failed before replay",
		replayed, skipped, failedBeforeReplay)
	// A run where every workflow is skipped verifies nothing and must not pass.
	if conclusive == 0 {
		t.Errorf("None of the %d running workflows were conclusively replayed; see subtest skip reasons", len(histories))
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

func replayBuildSkipReason(currentRef, buildID string) string {
	if buildID == "" {
		return "worker build ID is missing"
	}
	if strings.HasSuffix(buildID, "-dirty") {
		return fmt.Sprintf("worker build %q includes uncommitted code whose ancestry cannot be established", buildID)
	}
	if len(buildID) != 40 && len(buildID) != 64 {
		return fmt.Sprintf("worker build %q is not a full Git commit SHA", buildID)
	}
	if _, err := hex.DecodeString(buildID); err != nil {
		return fmt.Sprintf("worker build %q is not a Git commit SHA", buildID)
	}
	if !isReplayableVersion(currentRef, buildID) {
		return fmt.Sprintf("worker build %q is unavailable or is not an ancestor of %s", buildID, currentRef)
	}
	return ""
}

// Visibility can lag behind the history fetched for replay. Only completed
// workflow tasks identify workers whose commands are present in that history.
func replayHistorySkipReason(currentRef string, hist *history.History) string {
	events := hist.GetEvents()
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].GetEventType() == enums.EVENT_TYPE_WORKFLOW_TASK_COMPLETED {
			buildID := events[i].GetWorkflowTaskCompletedEventAttributes().GetWorkerVersion().GetBuildId()
			return replayBuildSkipReason(currentRef, buildID)
		}
	}
	return "history has no completed workflow task"
}

func replayCandidateSkipReason(wf listedWorkflow, blacklist map[string]struct{}) string {
	if wf.status != enums.WORKFLOW_EXECUTION_STATUS_RUNNING {
		return "not running"
	}
	if _, ok := blacklist[wf.id]; ok {
		return "blacklisted"
	}
	return ""
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

func visitRunningWorkflowCandidates(ctx context.Context, svc workflowservice.WorkflowServiceClient, namespace string, limit int, visit func(listedWorkflow) bool) error {
	var nextPageToken []byte
	selected := 0
	for selected < limit {
		// ListWorkflow caps RPCs at ten seconds even with a longer parent
		// deadline. Reverse-forwarded visibility responses can exceed that.
		pageCtx, cancel := context.WithTimeout(ctx, time.Minute)
		resp, err := svc.ListWorkflowExecutions(pageCtx, &workflowservice.ListWorkflowExecutionsRequest{
			Namespace: namespace,
			// The server default can transfer hundreds of unneeded entries.
			PageSize:      int32(min(limit-selected, 100)),
			Query:         "ExecutionStatus = 'Running'",
			NextPageToken: nextPageToken,
		})
		cancel()
		if err != nil {
			return err
		}
		for _, wfExec := range resp.Executions {
			if err := ctx.Err(); err != nil {
				return err
			}
			wf := listedWorkflow{
				id:            wfExec.Execution.WorkflowId,
				runID:         wfExec.Execution.RunId,
				status:        wfExec.Status,
				workerBuildID: wfExec.GetMostRecentWorkerVersionStamp().GetBuildId(),
			}
			if visit(wf) {
				selected++
				if selected >= limit {
					return nil
				}
			}
		}
		if len(resp.NextPageToken) == 0 {
			break
		}
		nextPageToken = resp.NextPageToken
	}
	return nil
}
