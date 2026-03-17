package common

import (
	"context"
	"fmt"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/segmentio/ksuid"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/api/enums/v1"
	historypb "go.temporal.io/api/history/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/interceptor"
	"go.temporal.io/sdk/workflow"
)

const (
	CodecCleanupWorkflowID       = "codec-payload-cleanup"
	codecCleanupSignalName       = "codec-workflow-completed"
	codecCleanupResetSignalName  = "codec-workflow-reset"
	DefaultCodecCleanupRetention = 7 * 24 * time.Hour
	codecCleanupInterval         = 1 * time.Hour
	codecOrphanScanInterval      = 7 * 24 * time.Hour
	codecMaxHistoryBeforeRenew   = 5000
	codecDeleteBatchSize         = 10000
	codecDeleteBatchDelay        = 1 * time.Second
	codecDeleteTimeout           = 1 * time.Hour
)

// PendingDeletion holds codec KV keys collected from a completed workflow,
// along with the timestamp when they were collected.
type PendingDeletion struct {
	WorkflowID  string    `json:"workflowId"`
	Keys        []string  `json:"keys"`
	CollectedAt time.Time `json:"collectedAt"`
}

// CodecCleanupWorkflowInput carries state across ContinueAsNew boundaries.
type CodecCleanupWorkflowInput struct {
	Pending          []PendingDeletion `json:"pending"`
	LastOrphanScanAt time.Time         `json:"lastOrphanScanAt"`
	Retention        time.Duration     `json:"retention"`
}

func (i *CodecCleanupWorkflowInput) retention() time.Duration {
	if i.Retention <= 0 {
		return DefaultCodecCleanupRetention
	}
	return i.Retention
}

// CodecCleanupSignal is sent by the interceptor when a workflow completes.
type CodecCleanupSignal struct {
	WorkflowID string `json:"workflowId"`
	RunID      string `json:"runId"`
}

// CodecCleanupResetSignal is sent when a workflow is reset, so its pending
// keys are removed before the new run produces its own completion signal.
type CodecCleanupResetSignal struct {
	WorkflowID string `json:"workflowId"`
}

// CodecCleanupActivities provides activities for the cleanup workflow.
type CodecCleanupActivities struct {
	TemporalClient client.Client
	Storage        KeyValueStorage
}

// CollectCodecKeysFromHistory fetches a workflow's history and returns all
// codec reference keys found in payloads.
func (a *CodecCleanupActivities) CollectCodecKeysFromHistory(ctx context.Context, workflowID, runID string) ([]string, error) {
	iter := a.TemporalClient.GetWorkflowHistory(ctx, workflowID, runID, false, enums.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)

	var keys []string
	seen := map[string]struct{}{}

	for iter.HasNext() {
		event, err := iter.Next()
		if err != nil {
			return nil, fmt.Errorf("error iterating history for %s: %w", workflowID, err)
		}
		payloads := extractPayloadsFromEvent(event)
		for _, p := range payloads {
			if p == nil || p.Metadata == nil {
				continue
			}
			if keyBytes, ok := p.Metadata[codecMetadataKey]; ok {
				key := string(keyBytes)
				if _, dup := seen[key]; !dup {
					seen[key] = struct{}{}
					keys = append(keys, key)
				}
			}
		}
	}
	return keys, nil
}

// DeleteCodecKeys deletes the specified codec KV keys in batches with rate limiting.
func (a *CodecCleanupActivities) DeleteCodecKeys(ctx context.Context, keys []string) error {
	for i := 0; i < len(keys); i += codecDeleteBatchSize {
		end := i + codecDeleteBatchSize
		if end > len(keys) {
			end = len(keys)
		}
		batch := keys[i:end]
		values := make(map[string]interface{}, len(batch))
		for _, key := range batch {
			values[key] = nil
		}
		if err := a.Storage.MSet(ctx, codecWorkspaceID, values); err != nil {
			return err
		}
		if end < len(keys) {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(codecDeleteBatchDelay):
			}
		}
	}
	return nil
}

// ListAllCodecKeys returns all codec KV keys.
func (a *CodecCleanupActivities) ListAllCodecKeys(ctx context.Context) ([]string, error) {
	return a.Storage.GetKeysWithPrefix(ctx, codecWorkspaceID, codecKeyPrefix)
}

// ListClosedWorkflowIDs returns workflow executions that are not running.
func (a *CodecCleanupActivities) ListClosedWorkflowIDs(ctx context.Context) ([]CodecCleanupSignal, error) {
	var result []CodecCleanupSignal
	var nextPageToken []byte

	for {
		resp, err := a.TemporalClient.ListWorkflow(ctx, &workflowservice.ListWorkflowExecutionsRequest{
			Query:         "ExecutionStatus != 'Running'",
			NextPageToken: nextPageToken,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to list closed workflows: %w", err)
		}
		for _, exec := range resp.Executions {
			result = append(result, CodecCleanupSignal{
				WorkflowID: exec.Execution.WorkflowId,
				RunID:      exec.Execution.RunId,
			})
		}
		if len(resp.NextPageToken) == 0 {
			break
		}
		nextPageToken = resp.NextPageToken
	}
	return result, nil
}

// ListRunningWorkflowCodecKeys collects all codec keys from running workflows.
func (a *CodecCleanupActivities) ListRunningWorkflowCodecKeys(ctx context.Context) ([]string, error) {
	var allKeys []string
	seen := map[string]struct{}{}
	var nextPageToken []byte

	for {
		resp, err := a.TemporalClient.ListWorkflow(ctx, &workflowservice.ListWorkflowExecutionsRequest{
			Query:         "ExecutionStatus = 'Running'",
			NextPageToken: nextPageToken,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to list running workflows: %w", err)
		}
		for _, exec := range resp.Executions {
			keys, err := a.CollectCodecKeysFromHistory(ctx, exec.Execution.WorkflowId, exec.Execution.RunId)
			if err != nil {
				log.Warn().Err(err).Str("workflowId", exec.Execution.WorkflowId).Msg("Failed to collect codec keys from running workflow")
				continue
			}
			for _, k := range keys {
				if _, dup := seen[k]; !dup {
					seen[k] = struct{}{}
					allKeys = append(allKeys, k)
				}
			}
		}
		if len(resp.NextPageToken) == 0 {
			break
		}
		nextPageToken = resp.NextPageToken
	}
	return allKeys, nil
}

func CodecPayloadCleanupWorkflow(ctx workflow.Context, input CodecCleanupWorkflowInput) error {
	logger := workflow.GetLogger(ctx)
	retention := input.retention()

	ao := workflow.ActivityOptions{
		StartToCloseTimeout: 5 * time.Minute,
	}
	actCtx := workflow.WithActivityOptions(ctx, ao)

	signalCh := workflow.GetSignalChannel(ctx, codecCleanupSignalName)
	resetCh := workflow.GetSignalChannel(ctx, codecCleanupResetSignalName)
	eventCount := 0

	nextCleanupAt := workflow.Now(ctx).Add(codecCleanupInterval)
	nextOrphanScanAt := input.LastOrphanScanAt.Add(codecOrphanScanInterval)
	if input.LastOrphanScanAt.IsZero() {
		nextOrphanScanAt = workflow.Now(ctx).Add(codecOrphanScanInterval)
	}

	removePendingByWorkflowID := func(workflowID string) {
		var kept []PendingDeletion
		for _, pd := range input.Pending {
			if pd.WorkflowID != workflowID {
				kept = append(kept, pd)
			}
		}
		input.Pending = kept
	}

	continueAsNew := func() error {
		return workflow.NewContinueAsNewError(ctx, CodecPayloadCleanupWorkflow, CodecCleanupWorkflowInput{
			Pending:          input.Pending,
			LastOrphanScanAt: input.LastOrphanScanAt,
			Retention:        input.Retention,
		})
	}

	for {
		if eventCount >= codecMaxHistoryBeforeRenew {
			return continueAsNew()
		}

		now := workflow.Now(ctx)
		sleepDur := nextCleanupAt.Sub(now)
		if nextOrphanScanAt.Before(nextCleanupAt) {
			sleepDur = nextOrphanScanAt.Sub(now)
		}
		if sleepDur < 0 {
			sleepDur = 0
		}

		selector := workflow.NewSelector(ctx)
		timerFired := false

		timerCtx, cancelTimer := workflow.WithCancel(ctx)
		timerFuture := workflow.NewTimer(timerCtx, sleepDur)
		selector.AddFuture(timerFuture, func(f workflow.Future) {
			_ = f.Get(timerCtx, nil)
			timerFired = true
		})

		selector.AddReceive(signalCh, func(ch workflow.ReceiveChannel, more bool) {
			var signal CodecCleanupSignal
			ch.Receive(ctx, &signal)

			if signal.WorkflowID == CodecCleanupWorkflowID {
				return
			}

			removePendingByWorkflowID(signal.WorkflowID)

			var keys []string
			var activities *CodecCleanupActivities
			err := workflow.ExecuteActivity(actCtx, activities.CollectCodecKeysFromHistory, signal.WorkflowID, signal.RunID).Get(ctx, &keys)
			if err != nil {
				logger.Warn("Failed to collect codec keys from workflow history",
					"workflowId", signal.WorkflowID, "error", err)
				return
			}
			if len(keys) > 0 {
				input.Pending = append(input.Pending, PendingDeletion{
					WorkflowID:  signal.WorkflowID,
					Keys:        keys,
					CollectedAt: workflow.Now(ctx),
				})
			}
			eventCount++
		})

		selector.AddReceive(resetCh, func(ch workflow.ReceiveChannel, more bool) {
			var signal CodecCleanupResetSignal
			ch.Receive(ctx, &signal)
			removePendingByWorkflowID(signal.WorkflowID)
		})

		selector.Select(ctx)
		cancelTimer()

		if ctx.Err() != nil {
			return ctx.Err()
		}

		if !timerFired {
			continue
		}
		eventCount++

		now = workflow.Now(ctx)

		// Process pending deletions whose retention has expired
		var remaining []PendingDeletion
		var keysToDelete []string
		for _, pd := range input.Pending {
			if now.Sub(pd.CollectedAt) >= retention {
				keysToDelete = append(keysToDelete, pd.Keys...)
			} else {
				remaining = append(remaining, pd)
			}
		}
		if len(keysToDelete) > 0 {
			var activities *CodecCleanupActivities
			deleteCtx := workflow.WithActivityOptions(actCtx, workflow.ActivityOptions{
				StartToCloseTimeout: codecDeleteTimeout,
			})
			err := workflow.ExecuteActivity(deleteCtx, activities.DeleteCodecKeys, keysToDelete).Get(ctx, nil)
			if err != nil {
				logger.Warn("Failed to delete expired codec keys", "error", err)
			} else {
				input.Pending = remaining
			}
			eventCount++
		}
		nextCleanupAt = now.Add(codecCleanupInterval)

		// Orphan scan
		if now.After(nextOrphanScanAt) || now.Equal(nextOrphanScanAt) {
			scanEvents := orphanScan(ctx, actCtx, retention)
			input.LastOrphanScanAt = now
			nextOrphanScanAt = now.Add(codecOrphanScanInterval)
			eventCount += scanEvents
		}
	}
}

// orphanScan finds and deletes codec keys not referenced by any workflow.
// Returns the number of activity invocations for history size tracking.
func orphanScan(ctx workflow.Context, actCtx workflow.Context, retention time.Duration) int {
	logger := workflow.GetLogger(ctx)
	var activities *CodecCleanupActivities
	activityCount := 0

	var allKeys []string
	err := workflow.ExecuteActivity(actCtx, activities.ListAllCodecKeys).Get(ctx, &allKeys)
	activityCount++
	if err != nil {
		logger.Warn("Orphan scan: failed to list all codec keys", "error", err)
		return activityCount
	}
	if len(allKeys) == 0 {
		return activityCount
	}

	// Collect keys from closed workflows
	var closedWorkflows []CodecCleanupSignal
	err = workflow.ExecuteActivity(actCtx, activities.ListClosedWorkflowIDs).Get(ctx, &closedWorkflows)
	activityCount++
	if err != nil {
		logger.Warn("Orphan scan: failed to list closed workflows", "error", err)
		return activityCount
	}

	referencedKeys := map[string]struct{}{}
	for _, wf := range closedWorkflows {
		var keys []string
		err := workflow.ExecuteActivity(actCtx, activities.CollectCodecKeysFromHistory, wf.WorkflowID, wf.RunID).Get(ctx, &keys)
		activityCount++
		if err != nil {
			logger.Warn("Orphan scan: failed to collect keys from closed workflow",
				"workflowId", wf.WorkflowID, "error", err)
			continue
		}
		for _, k := range keys {
			referencedKeys[k] = struct{}{}
		}
	}

	// Collect keys from running workflows
	var runningKeys []string
	err = workflow.ExecuteActivity(actCtx, activities.ListRunningWorkflowCodecKeys).Get(ctx, &runningKeys)
	activityCount++
	if err != nil {
		logger.Warn("Orphan scan: failed to collect running workflow keys", "error", err)
	}
	for _, k := range runningKeys {
		referencedKeys[k] = struct{}{}
	}

	// Find orphans: keys not referenced by any workflow, and old enough based on KSUID timestamp
	now := workflow.Now(ctx)
	var orphans []string
	for _, key := range allKeys {
		if _, referenced := referencedKeys[key]; referenced {
			continue
		}
		ksuidStr := key[len(codecKeyPrefix):]
		id, err := ksuid.Parse(ksuidStr)
		if err != nil {
			continue
		}
		if now.Sub(id.Time()) >= retention {
			orphans = append(orphans, key)
		}
	}

	if len(orphans) > 0 {
		deleteCtx := workflow.WithActivityOptions(actCtx, workflow.ActivityOptions{
			StartToCloseTimeout: codecDeleteTimeout,
		})
		err = workflow.ExecuteActivity(deleteCtx, activities.DeleteCodecKeys, orphans).Get(ctx, nil)
		activityCount++
		if err != nil {
			logger.Warn("Orphan scan: failed to delete orphan keys", "error", err)
		}
	}
	return activityCount
}

// extractPayloadsFromEvent pulls all Payload objects from a history event.
func extractPayloadsFromEvent(event *historypb.HistoryEvent) []*commonpb.Payload {
	var result []*commonpb.Payload

	appendPayloads := func(ps *commonpb.Payloads) {
		if ps != nil {
			result = append(result, ps.Payloads...)
		}
	}

	switch event.EventType {
	case enums.EVENT_TYPE_WORKFLOW_EXECUTION_STARTED:
		if attrs := event.GetWorkflowExecutionStartedEventAttributes(); attrs != nil {
			appendPayloads(attrs.Input)
		}
	case enums.EVENT_TYPE_WORKFLOW_EXECUTION_COMPLETED:
		if attrs := event.GetWorkflowExecutionCompletedEventAttributes(); attrs != nil {
			appendPayloads(attrs.Result)
		}
	case enums.EVENT_TYPE_ACTIVITY_TASK_SCHEDULED:
		if attrs := event.GetActivityTaskScheduledEventAttributes(); attrs != nil {
			appendPayloads(attrs.Input)
		}
	case enums.EVENT_TYPE_ACTIVITY_TASK_COMPLETED:
		if attrs := event.GetActivityTaskCompletedEventAttributes(); attrs != nil {
			appendPayloads(attrs.Result)
		}
	case enums.EVENT_TYPE_CHILD_WORKFLOW_EXECUTION_STARTED:
		// No payloads in started event
	case enums.EVENT_TYPE_CHILD_WORKFLOW_EXECUTION_COMPLETED:
		if attrs := event.GetChildWorkflowExecutionCompletedEventAttributes(); attrs != nil {
			appendPayloads(attrs.Result)
		}
	case enums.EVENT_TYPE_START_CHILD_WORKFLOW_EXECUTION_INITIATED:
		if attrs := event.GetStartChildWorkflowExecutionInitiatedEventAttributes(); attrs != nil {
			appendPayloads(attrs.Input)
		}
	case enums.EVENT_TYPE_SIGNAL_EXTERNAL_WORKFLOW_EXECUTION_INITIATED:
		if attrs := event.GetSignalExternalWorkflowExecutionInitiatedEventAttributes(); attrs != nil {
			appendPayloads(attrs.Input)
		}
	case enums.EVENT_TYPE_MARKER_RECORDED:
		if attrs := event.GetMarkerRecordedEventAttributes(); attrs != nil {
			for _, p := range attrs.Details {
				appendPayloads(p)
			}
		}
	case enums.EVENT_TYPE_WORKFLOW_EXECUTION_SIGNALED:
		if attrs := event.GetWorkflowExecutionSignaledEventAttributes(); attrs != nil {
			appendPayloads(attrs.Input)
		}
	case enums.EVENT_TYPE_WORKFLOW_EXECUTION_CONTINUED_AS_NEW:
		if attrs := event.GetWorkflowExecutionContinuedAsNewEventAttributes(); attrs != nil {
			appendPayloads(attrs.Input)
		}
	}
	return result
}

// codecCleanupInterceptor signals the cleanup workflow when any workflow completes.
type codecCleanupInterceptor struct {
	interceptor.WorkerInterceptorBase
}

func NewCodecCleanupInterceptor() interceptor.WorkerInterceptor {
	return &codecCleanupInterceptor{}
}

func (c *codecCleanupInterceptor) InterceptWorkflow(ctx workflow.Context, next interceptor.WorkflowInboundInterceptor) interceptor.WorkflowInboundInterceptor {
	return &codecCleanupWorkflowInterceptor{
		WorkflowInboundInterceptorBase: interceptor.WorkflowInboundInterceptorBase{Next: next},
	}
}

type codecCleanupWorkflowInterceptor struct {
	interceptor.WorkflowInboundInterceptorBase
}

func (i *codecCleanupWorkflowInterceptor) ExecuteWorkflow(ctx workflow.Context, in *interceptor.ExecuteWorkflowInput) (interface{}, error) {
	result, err := i.Next.ExecuteWorkflow(ctx, in)

	info := workflow.GetInfo(ctx)
	if info.WorkflowExecution.ID == CodecCleanupWorkflowID {
		return result, err
	}

	disconnectedCtx, _ := workflow.NewDisconnectedContext(ctx)
	signal := CodecCleanupSignal{
		WorkflowID: info.WorkflowExecution.ID,
		RunID:      info.WorkflowExecution.RunID,
	}
	_ = workflow.SignalExternalWorkflow(disconnectedCtx, CodecCleanupWorkflowID, "", codecCleanupSignalName, signal).Get(disconnectedCtx, nil)

	return result, err
}

// StartCodecCleanupWorkflow idempotently starts the cleanup workflow.
func StartCodecCleanupWorkflow(ctx context.Context, temporalClient client.Client, taskQueue string) {
	_, err := temporalClient.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:                    CodecCleanupWorkflowID,
		TaskQueue:             taskQueue,
		WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE,
	}, CodecPayloadCleanupWorkflow, CodecCleanupWorkflowInput{})
	if err != nil {
		log.Debug().Err(err).Msg("Codec cleanup workflow start (may already be running)")
	}
}
