package main

import (
	"testing"

	"sidekick/common"

	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/api/enums/v1"
	historypb "go.temporal.io/api/history/v1"
	taskqueuepb "go.temporal.io/api/taskqueue/v1"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/workflow"
)

func TestReplayIncludesWorkflowCleanup(t *testing.T) {
	t.Parallel()

	const workflowName = "cleanupReplayProbe"
	execution := &commonpb.WorkflowExecution{WorkflowId: common.CodecCleanupWorkflowID}
	history := &historypb.History{Events: []*historypb.HistoryEvent{
		{
			EventId: 1, EventType: enums.EVENT_TYPE_WORKFLOW_EXECUTION_STARTED,
			Attributes: &historypb.HistoryEvent_WorkflowExecutionStartedEventAttributes{
				WorkflowExecutionStartedEventAttributes: &historypb.WorkflowExecutionStartedEventAttributes{
					WorkflowType: &commonpb.WorkflowType{Name: workflowName},
					TaskQueue:    &taskqueuepb.TaskQueue{Name: "replay-test"},
				},
			},
		},
		{
			EventId: 2, EventType: enums.EVENT_TYPE_WORKFLOW_TASK_SCHEDULED,
			Attributes: &historypb.HistoryEvent_WorkflowTaskScheduledEventAttributes{
				WorkflowTaskScheduledEventAttributes: &historypb.WorkflowTaskScheduledEventAttributes{},
			},
		},
		{
			EventId: 3, EventType: enums.EVENT_TYPE_WORKFLOW_TASK_STARTED,
			Attributes: &historypb.HistoryEvent_WorkflowTaskStartedEventAttributes{
				WorkflowTaskStartedEventAttributes: &historypb.WorkflowTaskStartedEventAttributes{ScheduledEventId: 2},
			},
		},
		{
			EventId: 4, EventType: enums.EVENT_TYPE_WORKFLOW_TASK_COMPLETED,
			Attributes: &historypb.HistoryEvent_WorkflowTaskCompletedEventAttributes{
				WorkflowTaskCompletedEventAttributes: &historypb.WorkflowTaskCompletedEventAttributes{
					ScheduledEventId: 2, StartedEventId: 3,
				},
			},
		},
		{
			EventId: 5, EventType: enums.EVENT_TYPE_SIGNAL_EXTERNAL_WORKFLOW_EXECUTION_INITIATED,
			Attributes: &historypb.HistoryEvent_SignalExternalWorkflowExecutionInitiatedEventAttributes{
				SignalExternalWorkflowExecutionInitiatedEventAttributes: &historypb.SignalExternalWorkflowExecutionInitiatedEventAttributes{
					WorkflowTaskCompletedEventId: 4,
					WorkflowExecution:            execution,
					SignalName:                   "codec-workflow-completed",
					Control:                      "5",
				},
			},
		},
		{
			EventId: 6, EventType: enums.EVENT_TYPE_EXTERNAL_WORKFLOW_EXECUTION_SIGNALED,
			Attributes: &historypb.HistoryEvent_ExternalWorkflowExecutionSignaledEventAttributes{
				ExternalWorkflowExecutionSignaledEventAttributes: &historypb.ExternalWorkflowExecutionSignaledEventAttributes{
					InitiatedEventId: 5, WorkflowExecution: execution,
				},
			},
		},
		{
			EventId: 7, EventType: enums.EVENT_TYPE_WORKFLOW_TASK_SCHEDULED,
			Attributes: &historypb.HistoryEvent_WorkflowTaskScheduledEventAttributes{
				WorkflowTaskScheduledEventAttributes: &historypb.WorkflowTaskScheduledEventAttributes{},
			},
		},
		{
			EventId: 8, EventType: enums.EVENT_TYPE_WORKFLOW_TASK_STARTED,
			Attributes: &historypb.HistoryEvent_WorkflowTaskStartedEventAttributes{
				WorkflowTaskStartedEventAttributes: &historypb.WorkflowTaskStartedEventAttributes{ScheduledEventId: 7},
			},
		},
		{
			EventId: 9, EventType: enums.EVENT_TYPE_WORKFLOW_TASK_COMPLETED,
			Attributes: &historypb.HistoryEvent_WorkflowTaskCompletedEventAttributes{
				WorkflowTaskCompletedEventAttributes: &historypb.WorkflowTaskCompletedEventAttributes{
					ScheduledEventId: 7, StartedEventId: 8,
				},
			},
		},
		{
			EventId: 10, EventType: enums.EVENT_TYPE_WORKFLOW_EXECUTION_COMPLETED,
			Attributes: &historypb.HistoryEvent_WorkflowExecutionCompletedEventAttributes{
				WorkflowExecutionCompletedEventAttributes: &historypb.WorkflowExecutionCompletedEventAttributes{
					WorkflowTaskCompletedEventId: 9,
				},
			},
		},
	}}
	replayer, err := newWorkflowReplayer(converter.GetDefaultDataConverter())
	if err != nil {
		t.Fatal(err)
	}
	replayer.RegisterWorkflowWithOptions(func(workflow.Context) error { return nil }, workflow.RegisterOptions{Name: workflowName})
	if err := replayer.ReplayWorkflowHistory(nil, history); err != nil {
		t.Fatalf("replay a workflow that signaled codec cleanup: %v", err)
	}
}
