package main

import (
	"bufio"
	"context"
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
	sidekick_worker "sidekick/worker"

	"go.temporal.io/api/enums/v1"
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
	clientOptions, err := common.NewTemporalClientOptions(service, common.GetTemporalServerHostPort())
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
		id      string
		err     error
		skipped bool
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

	perWorkflowTimeout := 30 * time.Second

	for _, id := range filtered {
		wg.Add(1)
		go func(workflowID string) {
			defer wg.Done()

			done := make(chan *historyResult, 1)
			go func() {
				hist, err := GetWorkflowHistory(ctx, c, workflowID, "")
				if err != nil {
					done <- &historyResult{id: workflowID, err: err}
					return
				}
				if events := hist.Events; len(events) > 0 && terminalEventTypes[events[len(events)-1].EventType] {
					done <- &historyResult{id: workflowID, skipped: true}
					return
				}
				replayer, err := worker.NewWorkflowReplayerWithOptions(worker.WorkflowReplayerOptions{
					DataConverter: clientOptions.DataConverter,
				})
				if err != nil {
					done <- &historyResult{id: workflowID, err: err}
					return
				}
				sidekick_worker.RegisterWorkflows(replayer)
				replayErr := replayer.ReplayWorkflowHistory(nil, hist)
				done <- &historyResult{id: workflowID, err: replayErr}
			}()

			var result *historyResult
			select {
			case result = <-done:
			case <-time.After(perWorkflowTimeout):
				result = &historyResult{id: workflowID, err: fmt.Errorf("replay timed out after %s", perWorkflowTimeout)}
			}
			mu.Lock()
			histories[workflowID] = result
			mu.Unlock()
		}(id)
	}

	wg.Wait()

	for _, id := range filtered {
		result := histories[id]
		t.Run(id, func(t *testing.T) {
			t.Parallel()
			if result.skipped {
				t.Skipf("Workflow %s completed before replay; skipping", result.id)
			}
			if result.err != nil {
				t.Errorf("Replay failed for workflow %s: %v", result.id, result.err)
			}
		})
	}
}
