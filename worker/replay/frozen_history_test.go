package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"sidekick"
	"sidekick/common"
	"sidekick/utils"
	sidekickworker "sidekick/worker"

	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/api/history/v1"
	"go.temporal.io/api/proxy"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/worker"
	"google.golang.org/protobuf/encoding/protojson"
)

// TestFrozenWorkflowHistory replays a captured history so the same input can
// be replayed across revisions while the live workflow keeps advancing.
// It omits ancestry filtering because histories from other builds can still
// exercise compatible code and be useful diagnostic inputs; failures may
// instead reflect build differences.
//
// Capture (set SIDE_REPLAY_CAPTURE_WORKFLOW, optionally SIDE_REPLAY_CAPTURE_RUN):
//
//	SIDE_REPLAY_HISTORY_FILE="$PWD/.side/tmp/replay-history.json" \
//	SIDE_REPLAY_CAPTURE_WORKFLOW=<workflow-id> \
//	go test -count=1 -run '^TestFrozenWorkflowHistory$' ./worker/replay
//
// Offline replay: same command with SIDE_REPLAY_CAPTURE_WORKFLOW unset.
//
// The path must be absolute because tests run from the package directory. An
// existing file is never overwritten. Captured histories have offloaded
// payloads resolved, so they can contain secrets: keep them in .side/tmp.
//
// SIDE_REPLAY_PRINT_FROM/SIDE_REPLAY_PRINT_THROUGH log an event-ID range,
// SIDE_REPLAY_THROUGH_EVENT replays only through that event, and
// SIDE_REPLAY_TRIM_IN_FLIGHT=true drops an in-flight workflow-task tail.
func TestFrozenWorkflowHistory(t *testing.T) {
	path := os.Getenv("SIDE_REPLAY_HISTORY_FILE")
	if path == "" {
		t.Skip("SIDE_REPLAY_HISTORY_FILE is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	var hist *history.History
	if workflowID := os.Getenv("SIDE_REPLAY_CAPTURE_WORKFLOW"); workflowID != "" {
		storage, err := sidekick.GetStorage()
		if err != nil {
			t.Fatal(err)
		}
		hostPort := common.GetTemporalServerHostPort()
		if common.IsActiveEnvNonLocal() {
			if port, ok := common.LookupForwardedPort(common.GetTemporalReadOnlyServerPort()); ok {
				hostPort = fmt.Sprintf("127.0.0.1:%d", port)
			}
		}
		options, err := common.NewTemporalClientOptions(storage, hostPort)
		if err != nil {
			t.Fatal(err)
		}
		c, err := client.Dial(options)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		runID := os.Getenv("SIDE_REPLAY_CAPTURE_RUN")
		if runID == "" {
			description, err := c.DescribeWorkflowExecution(ctx, workflowID, "")
			if err != nil {
				t.Fatal(err)
			}
			runID = description.WorkflowExecutionInfo.Execution.RunId
		}
		cacheHome, err := common.GetSidekickCacheHome()
		if err != nil {
			t.Fatal(err)
		}
		err = retryTransient(3, 2*time.Second, func() error {
			var fetchErr error
			hist, _, fetchErr = fetchWorkflowHistoryCached(ctx, c.WorkflowService(),
				client.DefaultNamespace, filepath.Join(cacheHome, historyCacheDirName), workflowID, runID)
			return fetchErr
		})
		if err != nil {
			t.Fatal(err)
		}
		codec := common.NewPayloadCodec(storage, common.DefaultCodecThreshold)
		err = proxy.VisitPayloads(ctx, hist, proxy.VisitPayloadsOptions{
			SkipSearchAttributes: true,
			Visitor: func(_ *proxy.VisitPayloadsContext, payloads []*commonpb.Payload) ([]*commonpb.Payload, error) {
				return codec.Decode(payloads)
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		data, err := protojson.Marshal(hist)
		if err != nil {
			t.Fatal(err)
		}
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			t.Fatal(err)
		}
		_, writeErr := f.Write(data)
		closeErr := f.Close()
		if writeErr != nil {
			t.Fatal(writeErr)
		}
		if closeErr != nil {
			t.Fatal(closeErr)
		}
	} else {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		hist = &history.History{}
		if err := protojson.Unmarshal(data, hist); err != nil {
			t.Fatal(err)
		}
	}
	if len(hist.Events) == 0 {
		t.Fatal("history has no events")
	}
	t.Logf("frozen history: %d events, final event %d (%s)", len(hist.Events),
		hist.Events[len(hist.Events)-1].EventId, hist.Events[len(hist.Events)-1].EventType)

	first := replayDiagnosticEventID(t, "SIDE_REPLAY_PRINT_FROM")
	last := replayDiagnosticEventID(t, "SIDE_REPLAY_PRINT_THROUGH")
	if first != 0 {
		if last < first {
			t.Fatal("SIDE_REPLAY_PRINT_THROUGH must be >= SIDE_REPLAY_PRINT_FROM")
		}
		for _, event := range hist.Events {
			if event.EventId >= first && event.EventId <= last {
				data, err := protojson.Marshal(event)
				if err != nil {
					t.Fatal(err)
				}
				t.Logf("%s", data)
			}
		}
	}
	if through := replayDiagnosticEventID(t, "SIDE_REPLAY_THROUGH_EVENT"); through != 0 {
		found := false
		for i, event := range hist.Events {
			if event.EventId == through {
				hist.Events = hist.Events[:i+1]
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("event %d is not in the history", through)
		}
	}
	if os.Getenv("SIDE_REPLAY_TRIM_IN_FLIGHT") == "true" {
		hist.Events = dropInFlightWFTTail(hist.Events)
	}
	options := utils.TestReplayerOptions()
	options.DataConverter = converter.GetDefaultDataConverter()
	replayer, err := worker.NewWorkflowReplayerWithOptions(options)
	if err != nil {
		t.Fatal(err)
	}
	sidekickworker.RegisterWorkflows(replayer)
	if err := replayer.ReplayWorkflowHistory(nil, hist); err != nil {
		t.Fatal(err)
	}
}

func replayDiagnosticEventID(t *testing.T, name string) int64 {
	t.Helper()
	value := os.Getenv(name)
	if value == "" {
		return 0
	}
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil || id <= 0 {
		t.Fatalf("%s must be a positive event ID, got %q", name, value)
	}
	return id
}
