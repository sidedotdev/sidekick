package common_test

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"sidekick/common"
	"sidekick/srv/sqlite"

	"github.com/segmentio/ksuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	commonpb "go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"
	historypb "go.temporal.io/api/history/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

// Large enough that SDK bookkeeping (local activity marker metadata) stays
// inline, so only the test's oversized strings are offloaded.
const integrationThreshold = 4096

func oversized(tag string) string {
	return tag + ":" + strings.Repeat(tag[:1], 4*integrationThreshold)
}

func echoLengthActivity(_ context.Context, input string) (string, error) {
	return fmt.Sprintf("%s=%d", input[:strings.IndexByte(input, ':')], len(input)), nil
}

func oversizedResultLocalActivity(_ context.Context, tag string) (string, error) {
	return oversized(tag), nil
}

// replayLiveWorkflow schedules an oversized activity input, records an
// oversized local activity result, idles on a signal, then schedules another
// oversized input. The test restarts the worker while the workflow idles, so
// the task that handles the signal replays the whole history and then runs
// the second activity live: the second input is encoded by a task that
// transitioned from replay to live.
func replayLiveWorkflow(ctx workflow.Context) (string, error) {
	actx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{StartToCloseTimeout: time.Minute})
	lctx := workflow.WithLocalActivityOptions(ctx, workflow.LocalActivityOptions{ScheduleToCloseTimeout: time.Minute})

	ready := false
	if err := workflow.SetQueryHandler(ctx, "ready", func() (bool, error) { return ready, nil }); err != nil {
		return "", err
	}

	var first, local, second string
	if err := workflow.ExecuteActivity(actx, echoLengthActivity, oversized("first")).Get(ctx, &first); err != nil {
		return "", err
	}
	if err := workflow.ExecuteLocalActivity(lctx, oversizedResultLocalActivity, "local").Get(ctx, &local); err != nil {
		return "", err
	}
	ready = true
	var signal string
	workflow.GetSignalChannel(ctx, "go").Receive(ctx, &signal)
	if err := workflow.ExecuteActivity(actx, echoLengthActivity, oversized("second")).Get(ctx, &second); err != nil {
		return "", err
	}
	return fmt.Sprintf("%s;local=%d;%s;signal=%s", first, len(local), second, signal), nil
}

type replayLiveRun struct {
	storedKeys     []string
	referencedKeys []string
	refsByEvent    map[string]int
	// decodedRefs holds the string value behind every referenced key, so a
	// dangling or corrupt reference surfaces as a decode error.
	decodedRefs  map[string]string
	decodeErrors []error
	result       string
	workflowErr  error
}

func startReplayLiveWorker(t *testing.T, c client.Client, taskQueue string) worker.Worker {
	t.Helper()
	w := worker.New(c, taskQueue, worker.Options{})
	w.RegisterWorkflow(replayLiveWorkflow)
	w.RegisterActivity(echoLengthActivity)
	w.RegisterActivity(oversizedResultLocalActivity)
	require.NoError(t, w.Start())
	return w
}

// dumpWorkflowState logs the workflow's status, pending activities and event
// types so a stalled run can be diagnosed from the test output.
func dumpWorkflowState(t *testing.T, ctx context.Context, c client.Client, workflowID, runID string) {
	t.Helper()
	desc, err := c.DescribeWorkflowExecution(ctx, workflowID, runID)
	if err != nil {
		t.Logf("describe %s/%s: %v", workflowID, runID, err)
		return
	}
	t.Logf("workflow %s/%s status=%s historyLength=%d pendingActivities=%d",
		workflowID, runID, desc.WorkflowExecutionInfo.Status, desc.WorkflowExecutionInfo.HistoryLength, len(desc.PendingActivities))
	for _, pa := range desc.PendingActivities {
		t.Logf("  pending activity %s state=%s attempt=%d lastFailure=%v", pa.ActivityType.GetName(), pa.State, pa.Attempt, pa.LastFailure.GetMessage())
	}
	iter := c.GetWorkflowHistory(ctx, workflowID, runID, false, enumspb.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
	for iter.HasNext() {
		event, err := iter.Next()
		if err != nil {
			t.Logf("  history read error: %v", err)
			return
		}
		line := fmt.Sprintf("  event %d %s", event.EventId, event.EventType)
		if a, ok := event.Attributes.(*historypb.HistoryEvent_WorkflowTaskFailedEventAttributes); ok {
			line += " cause=" + a.WorkflowTaskFailedEventAttributes.Cause.String() + " failure=" + a.WorkflowTaskFailedEventAttributes.Failure.GetMessage()
		}
		t.Log(line)
	}
}

func runReplayLiveWorkflow(t *testing.T, dc converter.DataConverter, storage *sqlite.Storage) replayLiveRun {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	t.Cleanup(cancel)

	c, err := client.Dial(client.Options{HostPort: common.GetTemporalClientHostPort(), DataConverter: dc})
	require.NoError(t, err)
	t.Cleanup(c.Close)

	taskQueue := "replay-aware-" + ksuid.New().String()
	w := startReplayLiveWorker(t, c, taskQueue)
	workerStopped := false
	defer func() {
		if !workerStopped {
			w.Stop()
		}
	}()

	run, err := c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{ID: "replay-aware-" + ksuid.New().String(), TaskQueue: taskQueue}, replayLiveWorkflow)
	require.NoError(t, err)
	// Registered at test scope so assertions made after this helper returns
	// still trigger diagnostics and cleanup.
	t.Cleanup(func() {
		if t.Failed() {
			dumpWorkflowState(t, ctx, c, run.GetID(), run.GetRunID())
			// Its offloaded payloads live in a test DB, so the run can never
			// be resumed; don't leave it running on the shared server.
			if err := c.TerminateWorkflow(context.Background(), run.GetID(), run.GetRunID(), "replay-aware integration test failed"); err != nil {
				t.Logf("terminate %s/%s: %v", run.GetID(), run.GetRunID(), err)
			}
		}
	})

	require.Eventually(t, func() bool {
		resp, err := c.QueryWorkflow(ctx, run.GetID(), run.GetRunID(), "ready")
		if err != nil {
			return false
		}
		var ready bool
		return resp.Get(&ready) == nil && ready
	}, time.Minute, 100*time.Millisecond, "workflow never reached the signal wait")

	// Restarting the worker drops the sticky cache (it is released when the
	// last worker stops and purged explicitly for certainty), so the task
	// that handles the signal must replay the full history first.
	w.Stop()
	workerStopped = true
	worker.PurgeStickyWorkflowCache()
	w2 := startReplayLiveWorker(t, c, taskQueue)
	defer w2.Stop()

	require.NoError(t, c.SignalWorkflow(ctx, run.GetID(), run.GetRunID(), "go", "now"))

	out := replayLiveRun{refsByEvent: map[string]int{}, decodedRefs: map[string]string{}}
	out.workflowErr = run.Get(ctx, &out.result)

	out.storedKeys, err = storage.GetKeysWithPrefix(ctx, common.CodecWorkspaceID, common.CodecKeyPrefix)
	require.NoError(t, err)
	sort.Strings(out.storedKeys)

	iter := c.GetWorkflowHistory(ctx, run.GetID(), run.GetRunID(), false, enumspb.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
	for iter.HasNext() {
		event, err := iter.Next()
		require.NoError(t, err)
		for _, p := range historyPayloads(event) {
			keyBytes, ok := p.Metadata[common.CodecMetadataKey]
			if !ok {
				continue
			}
			key := string(keyBytes)
			out.referencedKeys = append(out.referencedKeys, key)
			out.refsByEvent[event.EventType.String()]++
			var value string
			if err := dc.FromPayload(p, &value); err != nil {
				out.decodeErrors = append(out.decodeErrors, fmt.Errorf("%s %s: %w", event.EventType, key, err))
				continue
			}
			out.decodedRefs[key] = value
		}
	}
	sort.Strings(out.referencedKeys)
	return out
}

// historyPayloads returns the payloads of the event types this workflow
// produces that carry user data.
func historyPayloads(event *historypb.HistoryEvent) []*commonpb.Payload {
	var out []*commonpb.Payload
	switch a := event.Attributes.(type) {
	case *historypb.HistoryEvent_ActivityTaskScheduledEventAttributes:
		out = a.ActivityTaskScheduledEventAttributes.GetInput().GetPayloads()
	case *historypb.HistoryEvent_ActivityTaskCompletedEventAttributes:
		out = a.ActivityTaskCompletedEventAttributes.GetResult().GetPayloads()
	case *historypb.HistoryEvent_MarkerRecordedEventAttributes:
		for _, d := range a.MarkerRecordedEventAttributes.GetDetails() {
			out = append(out, d.GetPayloads()...)
		}
	case *historypb.HistoryEvent_WorkflowExecutionCompletedEventAttributes:
		out = a.WorkflowExecutionCompletedEventAttributes.GetResult().GetPayloads()
	case *historypb.HistoryEvent_WorkflowExecutionSignaledEventAttributes:
		out = a.WorkflowExecutionSignaledEventAttributes.GetInput().GetPayloads()
	}
	return out
}

// Runs against the local Temporal server and restarts the worker mid-run so
// the final workflow task is a full replay followed by live execution. Not
// parallel: the sticky cache is process-global and gets purged.
func TestReplayAwareDataConverter_WorkerReplayThenLive(t *testing.T) {
	if os.Getenv("SIDE_INTEGRATION_TEST") != "true" {
		t.Skip("Skipping integration test; SIDE_INTEGRATION_TEST not set")
	}

	expectedResult := fmt.Sprintf("first=%d;local=%d;second=%d;signal=now",
		len(oversized("first")), len(oversized("local")), len(oversized("second")))
	// first input, local activity result marker, second input
	expectedRefsByEvent := map[string]int{"ActivityTaskScheduled": 2, "MarkerRecorded": 1}
	expectedDecoded := []string{oversized("first"), oversized("local"), oversized("second")}

	t.Run("control: codec-only converter re-stores inputs on every replay", func(t *testing.T) {
		storage := sqlite.NewTestSqliteStorage(t, "codec")
		dc := converter.NewCodecDataConverter(converter.GetDefaultDataConverter(), common.NewPayloadCodec(storage, integrationThreshold))
		run := runReplayLiveWorkflow(t, dc, storage)
		require.NoError(t, run.workflowErr)
		assert.Equal(t, expectedResult, run.result)
		assert.Equal(t, expectedRefsByEvent, run.refsByEvent)
		assert.Greater(t, len(run.storedKeys), len(run.referencedKeys), "replays should have written unreferenced copies")
	})

	t.Run("replay-aware converter stores exactly the referenced payloads", func(t *testing.T) {
		storage := sqlite.NewTestSqliteStorage(t, "codec")
		dc := common.NewReplayAwareDataConverter(converter.GetDefaultDataConverter(), common.NewPayloadCodec(storage, integrationThreshold))
		run := runReplayLiveWorkflow(t, dc, storage)
		require.NoError(t, run.workflowErr)
		assert.Equal(t, expectedResult, run.result)
		assert.Equal(t, expectedRefsByEvent, run.refsByEvent)
		assert.Equal(t, run.referencedKeys, run.storedKeys, "stored keys must be exactly the keys history references")
		assert.Empty(t, run.decodeErrors)
		decoded := make([]string, 0, len(run.decodedRefs))
		for _, v := range run.decodedRefs {
			decoded = append(decoded, v)
		}
		sort.Strings(decoded)
		assert.Equal(t, expectedDecoded, decoded)
	})
}
