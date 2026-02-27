package main

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"sidekick/common"
	sidekick_worker "sidekick/worker"

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
	maxWorkflowsToReplay = 30
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

func listRecentRunningWorkflows(ctx context.Context, c client.Client, limit int) ([]string, error) {
	var workflowIDs []string
	var nextPageToken []byte

	for {
		resp, err := c.ListWorkflow(ctx, &workflowservice.ListWorkflowExecutionsRequest{
			Query:         "ExecutionStatus = 'Running'",
			NextPageToken: nextPageToken,
		})
		if err != nil {
			return nil, err
		}
		for _, exec := range resp.Executions {
			workflowIDs = append(workflowIDs, exec.Execution.WorkflowId)
			if len(workflowIDs) >= limit {
				return workflowIDs, nil
			}
		}
		if len(resp.NextPageToken) == 0 {
			break
		}
		nextPageToken = resp.NextPageToken
	}
	return workflowIDs, nil
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

	c, err := client.Dial(client.Options{
		Logger:   logur.LoggerToKV(zerologadapter.New(log.Logger)),
		HostPort: common.GetTemporalServerHostPort(),
	})
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
	workflowIDs, err := listRecentRunningWorkflows(ctx, c, fetchLimit)
	if err != nil {
		t.Fatalf("Failed to list running workflows: %v", err)
	}

	var filtered []string
	for _, id := range workflowIDs {
		if _, ok := blacklist[id]; ok {
			t.Logf("Skipping blacklisted workflow: %s", id)
			continue
		}
		filtered = append(filtered, id)
		if len(filtered) >= maxWorkflowsToReplay {
			break
		}
	}

	if len(filtered) == 0 {
		t.Logf("No running workflows to replay (fetched: %d, all blacklisted or none found)", len(workflowIDs))
		return
	}

	t.Logf("Replaying %d most recent running workflows", len(filtered))

	// Fetch all histories concurrently, then replay concurrently in subtests.
	type historyResult struct {
		id  string
		err error
	}

	var mu sync.Mutex
	histories := make(map[string]*historyResult)
	var wg sync.WaitGroup

	for _, id := range filtered {
		wg.Add(1)
		go func(workflowID string) {
			defer wg.Done()
			hist, err := GetWorkflowHistory(ctx, c, workflowID, "")
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				histories[workflowID] = &historyResult{id: workflowID, err: err}
				return
			}
			replayer := worker.NewWorkflowReplayer()
			sidekick_worker.RegisterWorkflows(replayer)
			replayErr := replayer.ReplayWorkflowHistory(nil, hist)
			histories[workflowID] = &historyResult{id: workflowID, err: replayErr}
		}(id)
	}

	wg.Wait()

	for _, id := range filtered {
		result := histories[id]
		t.Run(id, func(t *testing.T) {
			t.Parallel()
			if result.err != nil {
				t.Errorf("Replay failed for workflow %s: %v", result.id, result.err)
			}
		})
	}
}
