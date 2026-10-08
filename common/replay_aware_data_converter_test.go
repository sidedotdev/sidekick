package common_test

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"sidekick/common"
	"sidekick/srv/sqlite"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	commonpb "go.temporal.io/api/common/v1"
	enumspb "go.temporal.io/api/enums/v1"
	historypb "go.temporal.io/api/history/v1"
	taskqueuepb "go.temporal.io/api/taskqueue/v1"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	replayTestThreshold  = 100
	replayTestWorkflow   = "ReplayAwareProbeWorkflow"
	replayTestActivity   = "ReplayAwareProbeActivity"
	replayTestLargeInput = "large-input-"
)

var taskQueue = taskqueuepb.TaskQueue{Name: "replay-aware-probe"}

func replayTestInput() string {
	return replayTestLargeInput + strings.Repeat("x", 4*replayTestThreshold)
}

func replayProbeWorkflow(ctx workflow.Context) error {
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{StartToCloseTimeout: time.Minute})
	return workflow.ExecuteActivity(ctx, replayTestActivity, replayTestInput()).Get(ctx, nil)
}

type codecWriteCounter struct {
	*sqlite.Storage
	writes atomic.Int32
}

func (s *codecWriteCounter) MSetRaw(ctx context.Context, workspaceId string, values map[string][]byte) error {
	s.writes.Add(int32(len(values)))
	return s.Storage.MSetRaw(ctx, workspaceId, values)
}

// probeHistory builds the history a live run of replayProbeWorkflow produces,
// with the activity input offloaded through codec as the live run would have.
func probeHistory(t *testing.T, codecDC converter.DataConverter) *historypb.History {
	t.Helper()
	input, err := codecDC.ToPayloads(replayTestInput())
	require.NoError(t, err)
	require.Contains(t, input.Payloads[0].Metadata, common.CodecMetadataKey, "live encode should offload")
	result, err := converter.GetDefaultDataConverter().ToPayloads(nil)
	require.NoError(t, err)

	now := timestamppb.Now()
	event := func(id int64, typ enumspb.EventType, attrs any) *historypb.HistoryEvent {
		e := &historypb.HistoryEvent{EventId: id, EventTime: now, EventType: typ}
		switch a := attrs.(type) {
		case *historypb.HistoryEvent_WorkflowExecutionStartedEventAttributes:
			e.Attributes = a
		case *historypb.HistoryEvent_WorkflowTaskScheduledEventAttributes:
			e.Attributes = a
		case *historypb.HistoryEvent_WorkflowTaskStartedEventAttributes:
			e.Attributes = a
		case *historypb.HistoryEvent_WorkflowTaskCompletedEventAttributes:
			e.Attributes = a
		case *historypb.HistoryEvent_ActivityTaskScheduledEventAttributes:
			e.Attributes = a
		case *historypb.HistoryEvent_ActivityTaskStartedEventAttributes:
			e.Attributes = a
		case *historypb.HistoryEvent_ActivityTaskCompletedEventAttributes:
			e.Attributes = a
		case *historypb.HistoryEvent_WorkflowExecutionCompletedEventAttributes:
			e.Attributes = a
		default:
			t.Fatalf("unsupported history event attributes %T", attrs)
		}
		return e
	}
	return &historypb.History{Events: []*historypb.HistoryEvent{
		event(1, enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_STARTED, &historypb.HistoryEvent_WorkflowExecutionStartedEventAttributes{
			WorkflowExecutionStartedEventAttributes: &historypb.WorkflowExecutionStartedEventAttributes{
				WorkflowType: &commonpb.WorkflowType{Name: replayTestWorkflow},
				TaskQueue:    &taskQueue,
			},
		}),
		event(2, enumspb.EVENT_TYPE_WORKFLOW_TASK_SCHEDULED, &historypb.HistoryEvent_WorkflowTaskScheduledEventAttributes{
			WorkflowTaskScheduledEventAttributes: &historypb.WorkflowTaskScheduledEventAttributes{TaskQueue: &taskQueue},
		}),
		event(3, enumspb.EVENT_TYPE_WORKFLOW_TASK_STARTED, &historypb.HistoryEvent_WorkflowTaskStartedEventAttributes{
			WorkflowTaskStartedEventAttributes: &historypb.WorkflowTaskStartedEventAttributes{ScheduledEventId: 2},
		}),
		event(4, enumspb.EVENT_TYPE_WORKFLOW_TASK_COMPLETED, &historypb.HistoryEvent_WorkflowTaskCompletedEventAttributes{
			WorkflowTaskCompletedEventAttributes: &historypb.WorkflowTaskCompletedEventAttributes{ScheduledEventId: 2, StartedEventId: 3},
		}),
		event(5, enumspb.EVENT_TYPE_ACTIVITY_TASK_SCHEDULED, &historypb.HistoryEvent_ActivityTaskScheduledEventAttributes{
			ActivityTaskScheduledEventAttributes: &historypb.ActivityTaskScheduledEventAttributes{
				ActivityId:                   "5",
				ActivityType:                 &commonpb.ActivityType{Name: replayTestActivity},
				TaskQueue:                    &taskQueue,
				Input:                        input,
				WorkflowTaskCompletedEventId: 4,
			},
		}),
		event(6, enumspb.EVENT_TYPE_ACTIVITY_TASK_STARTED, &historypb.HistoryEvent_ActivityTaskStartedEventAttributes{
			ActivityTaskStartedEventAttributes: &historypb.ActivityTaskStartedEventAttributes{ScheduledEventId: 5},
		}),
		event(7, enumspb.EVENT_TYPE_ACTIVITY_TASK_COMPLETED, &historypb.HistoryEvent_ActivityTaskCompletedEventAttributes{
			ActivityTaskCompletedEventAttributes: &historypb.ActivityTaskCompletedEventAttributes{ScheduledEventId: 5, StartedEventId: 6, Result: result},
		}),
		event(8, enumspb.EVENT_TYPE_WORKFLOW_TASK_SCHEDULED, &historypb.HistoryEvent_WorkflowTaskScheduledEventAttributes{
			WorkflowTaskScheduledEventAttributes: &historypb.WorkflowTaskScheduledEventAttributes{TaskQueue: &taskQueue},
		}),
		event(9, enumspb.EVENT_TYPE_WORKFLOW_TASK_STARTED, &historypb.HistoryEvent_WorkflowTaskStartedEventAttributes{
			WorkflowTaskStartedEventAttributes: &historypb.WorkflowTaskStartedEventAttributes{ScheduledEventId: 8},
		}),
		event(10, enumspb.EVENT_TYPE_WORKFLOW_TASK_COMPLETED, &historypb.HistoryEvent_WorkflowTaskCompletedEventAttributes{
			WorkflowTaskCompletedEventAttributes: &historypb.WorkflowTaskCompletedEventAttributes{ScheduledEventId: 8, StartedEventId: 9},
		}),
		event(11, enumspb.EVENT_TYPE_WORKFLOW_EXECUTION_COMPLETED, &historypb.HistoryEvent_WorkflowExecutionCompletedEventAttributes{
			WorkflowExecutionCompletedEventAttributes: &historypb.WorkflowExecutionCompletedEventAttributes{WorkflowTaskCompletedEventId: 10},
		}),
	}}
}

func replayProbe(t *testing.T, dc converter.DataConverter, hist *historypb.History) {
	t.Helper()
	replayer, err := worker.NewWorkflowReplayerWithOptions(worker.WorkflowReplayerOptions{DataConverter: dc})
	require.NoError(t, err)
	replayer.RegisterWorkflowWithOptions(replayProbeWorkflow, workflow.RegisterOptions{Name: replayTestWorkflow})
	require.NoError(t, replayer.ReplayWorkflowHistory(nil, hist))
}

func TestReplayAwareDataConverter_ReplayDoesNotWriteCodecPayloads(t *testing.T) {
	t.Parallel()
	storage := &codecWriteCounter{Storage: sqlite.NewTestSqliteStorage(t, "codec")}
	codec := common.NewPayloadCodec(storage, replayTestThreshold)
	codecOnly := converter.NewCodecDataConverter(converter.GetDefaultDataConverter(), codec)
	hist := probeHistory(t, codecOnly)
	require.Equal(t, int32(1), storage.writes.Load(), "building the history offloads the live input once")

	t.Run("control: codec without replay awareness writes on replay", func(t *testing.T) {
		before := storage.writes.Load()
		replayProbe(t, codecOnly, hist)
		assert.Equal(t, before+1, storage.writes.Load())
	})

	t.Run("replay-aware converter writes nothing on replay", func(t *testing.T) {
		before := storage.writes.Load()
		replayProbe(t, common.NewReplayAwareDataConverter(converter.GetDefaultDataConverter(), codec), hist)
		assert.Equal(t, before, storage.writes.Load())
	})
}

func TestReplayAwareDataConverter_OffloadsOutsideWorkflowContext(t *testing.T) {
	t.Parallel()
	storage := &codecWriteCounter{Storage: sqlite.NewTestSqliteStorage(t, "codec")}
	codec := common.NewPayloadCodec(storage, replayTestThreshold)
	dc := common.NewReplayAwareDataConverter(converter.GetDefaultDataConverter(), codec)

	ctxDC := dc.(workflow.ContextAware).WithContext(context.Background())
	payloads, err := ctxDC.ToPayloads(replayTestInput())
	require.NoError(t, err)
	assert.Contains(t, payloads.Payloads[0].Metadata, common.CodecMetadataKey)
	assert.Equal(t, int32(1), storage.writes.Load())

	var decoded string
	require.NoError(t, dc.FromPayloads(payloads, &decoded))
	assert.Equal(t, replayTestInput(), decoded)
}
