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

	"sidekick/common"
	"sidekick/utils"
	sidekick_worker "sidekick/worker"

	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/api/enums/v1"
	"go.temporal.io/api/history/v1"
	"go.temporal.io/api/proxy"
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
// $SIDE_CACHE_HOME/replay_blacklist.txt (one ID per line) are skipped.
func TestReplayRunningWorkflows(t *testing.T) {
	t.Parallel()
	if os.Getenv("SIDE_INTEGRATION_TEST") != "true" {
		t.Skip("Skipping integration test; SIDE_INTEGRATION_TEST not set")
	}

	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})

	ctx := context.Background()

	service, err := sidekick.GetService()
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
	clientOptions, err := common.NewTemporalClientOptions(service, temporalHostPort)
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

	var filtered []string
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
		filtered = append(filtered, wf.id)
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

	for _, id := range filtered {
		wg.Add(1)
		go func(workflowID string) {
			defer wg.Done()

			result := &historyResult{id: workflowID}
			defer func() {
				mu.Lock()
				histories[workflowID] = result
				mu.Unlock()
			}()

			hist, err := GetWorkflowHistory(ctx, c, workflowID, "")
			if err != nil {
				result.err = err
				return
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
			codec := common.NewPayloadCodec(service, common.DefaultCodecThreshold)
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
				result.err = err
				return
			}
			replayerOptions := utils.TestReplayerOptions()
			replayerOptions.DataConverter = clientOptions.DataConverter
			replayer, err := worker.NewWorkflowReplayerWithOptions(replayerOptions)
			if err != nil {
				result.err = err
				return
			}
			sidekick_worker.RegisterWorkflows(replayer)
			replaySem <- struct{}{}
			result.err = replayer.ReplayWorkflowHistory(nil, hist)
			<-replaySem
		}(id)
	}

	wg.Wait()

	for _, id := range filtered {
		result := histories[id]
		t.Run(id, func(t *testing.T) {
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
