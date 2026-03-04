package srv

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"sidekick/domain"
)

type CascadeDeleteTaskInput struct {
	WorkspaceId string
	TaskId      string
}

type CascadeDeleteTaskActivities struct {
	Service        Service
	TemporalClient client.Client
}

type taskSnapshot struct {
	Task  domain.Task
	Flows []domain.Flow
}

type flowSnapshot struct {
	Flow domain.Flow
}

type FlowActionsPage struct {
	FlowId  string
	Actions []domain.FlowAction
	HasMore bool
}

type SubflowsPage struct {
	FlowId   string
	Subflows []domain.Subflow
	HasMore  bool
}

const (
	maxFlowActionsPerPage = 25
	maxSubflowsPerPage    = 100
	maxBlocksPerPage      = 50
)

type BlocksPage struct {
	FlowId  string
	Keys    []string
	Values  [][]byte
	HasMore bool
}

func CascadeDeleteTaskWorkflow(ctx workflow.Context, input CascadeDeleteTaskInput) error {
	logger := workflow.GetLogger(ctx)

	ao := workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    time.Second,
			BackoffCoefficient: 2.0,
			MaximumInterval:    time.Minute,
			MaximumAttempts:    3,
		},
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	var activities *CascadeDeleteTaskActivities

	// Step 1: Build base snapshot (task + flows only)
	var snapshot taskSnapshot
	err := workflow.ExecuteActivity(ctx, activities.BuildSnapshot, input.WorkspaceId, input.TaskId).Get(ctx, &snapshot)
	if err != nil {
		logger.Error("Failed to build snapshot", "error", err)
		return err
	}

	// Initialize compensation data structures - populated incrementally just before each delete
	allFlowActions := make(map[string][]domain.FlowAction)
	allSubflows := make(map[string][]domain.Subflow)
	allBlocks := make(map[string]map[string][]byte)

	// Step 2: Terminate all flow workflows (best-effort, do this first to stop new data from being created)
	for _, flow := range snapshot.Flows {
		var terminated bool
		err := workflow.ExecuteActivity(ctx, activities.TerminateFlowWorkflow, flow.Id).Get(ctx, &terminated)
		if err != nil {
			logger.Warn("Failed to terminate flow workflow", "flowId", flow.Id, "error", err)
		}
	}

	// Step 3: For each flow, snapshot and delete flow actions immediately
	for _, flow := range snapshot.Flows {
		// Snapshot flow actions for this flow
		afterId := ""
		for {
			var page FlowActionsPage
			err := workflow.ExecuteActivity(ctx, activities.GetFlowActionsPage, input.WorkspaceId, flow.Id, afterId).Get(ctx, &page)
			if err != nil {
				logger.Error("Failed to get flow actions page", "flowId", flow.Id, "error", err)
				compensate(ctx, activities, snapshot, allFlowActions, allSubflows, allBlocks)
				return err
			}
			allFlowActions[flow.Id] = append(allFlowActions[flow.Id], page.Actions...)
			if !page.HasMore || len(page.Actions) == 0 {
				break
			}
			afterId = page.Actions[len(page.Actions)-1].Id
		}

		// Delete flow actions immediately after snapshotting
		err := workflow.ExecuteActivity(ctx, activities.DeleteFlowActions, input.WorkspaceId, flow.Id).Get(ctx, nil)
		if err != nil {
			logger.Error("Failed to delete flow actions, compensating", "flowId", flow.Id, "error", err)
			compensate(ctx, activities, snapshot, allFlowActions, allSubflows, allBlocks)
			return err
		}
	}

	// Step 4: For each flow, snapshot and delete subflows immediately
	for _, flow := range snapshot.Flows {
		// Snapshot subflows for this flow
		afterId := ""
		for {
			var page SubflowsPage
			err := workflow.ExecuteActivity(ctx, activities.GetSubflowsPage, input.WorkspaceId, flow.Id, afterId).Get(ctx, &page)
			if err != nil {
				logger.Error("Failed to get subflows page", "flowId", flow.Id, "error", err)
				compensate(ctx, activities, snapshot, allFlowActions, allSubflows, allBlocks)
				return err
			}
			allSubflows[flow.Id] = append(allSubflows[flow.Id], page.Subflows...)
			if !page.HasMore || len(page.Subflows) == 0 {
				break
			}
			afterId = page.Subflows[len(page.Subflows)-1].Id
		}

		// Delete subflows immediately after snapshotting
		err := workflow.ExecuteActivity(ctx, activities.DeleteSubflows, input.WorkspaceId, flow.Id).Get(ctx, nil)
		if err != nil {
			logger.Error("Failed to delete subflows, compensating", "flowId", flow.Id, "error", err)
			compensate(ctx, activities, snapshot, allFlowActions, allSubflows, allBlocks)
			return err
		}
	}

	// Step 5: Snapshot and delete each flow
	for i, flow := range snapshot.Flows {
		// Re-snapshot the flow immediately before deletion
		var flowSnap flowSnapshot
		err := workflow.ExecuteActivity(ctx, activities.SnapshotFlow, input.WorkspaceId, flow.Id).Get(ctx, &flowSnap)
		if err != nil {
			logger.Warn("Failed to snapshot flow before deletion", "flowId", flow.Id, "error", err)
			// Use the original snapshot if re-snapshot fails (flow may already be deleted)
		} else {
			snapshot.Flows[i] = flowSnap.Flow
		}

		err = workflow.ExecuteActivity(ctx, activities.DeleteFlow, input.WorkspaceId, flow.Id).Get(ctx, nil)
		if err != nil {
			logger.Error("Failed to delete flow, compensating", "flowId", flow.Id, "error", err)
			compensate(ctx, activities, snapshot, allFlowActions, allSubflows, allBlocks)
			return err
		}
	}

	// Step 6: Snapshot and delete task
	var taskSnap domain.Task
	err = workflow.ExecuteActivity(ctx, activities.SnapshotTask, input.WorkspaceId, input.TaskId).Get(ctx, &taskSnap)
	if err != nil {
		logger.Warn("Failed to snapshot task before deletion", "taskId", input.TaskId, "error", err)
		// Use the original snapshot if re-snapshot fails
	} else {
		snapshot.Task = taskSnap
	}

	err = workflow.ExecuteActivity(ctx, activities.DeleteTask, input.WorkspaceId, input.TaskId).Get(ctx, nil)
	if err != nil {
		logger.Error("Failed to delete task, compensating", "error", err)
		compensate(ctx, activities, snapshot, allFlowActions, allSubflows, allBlocks)
		return err
	}

	// Step 7: For each flow, snapshot and delete KV blocks immediately (last, as specified)
	for _, flow := range snapshot.Flows {
		allBlocks[flow.Id] = make(map[string][]byte)
		prefix := flow.Id + ":msg:"

		// Snapshot blocks for this flow
		afterKey := ""
		for {
			var page BlocksPage
			err := workflow.ExecuteActivity(ctx, activities.GetBlocksPage, input.WorkspaceId, prefix, afterKey).Get(ctx, &page)
			if err != nil {
				logger.Error("Failed to get blocks page", "flowId", flow.Id, "error", err)
				compensate(ctx, activities, snapshot, allFlowActions, allSubflows, allBlocks)
				return err
			}
			for i, key := range page.Keys {
				allBlocks[flow.Id][key] = page.Values[i]
			}
			if !page.HasMore || len(page.Keys) == 0 {
				break
			}
			afterKey = page.Keys[len(page.Keys)-1]
		}

		// Delete KV prefix immediately after snapshotting
		err := workflow.ExecuteActivity(ctx, activities.DeleteKVPrefix, input.WorkspaceId, prefix).Get(ctx, nil)
		if err != nil {
			logger.Error("Failed to delete KV prefix, compensating", "flowId", flow.Id, "error", err)
			compensate(ctx, activities, snapshot, allFlowActions, allSubflows, allBlocks)
			return err
		}
	}

	return nil
}

func compensate(ctx workflow.Context, activities *CascadeDeleteTaskActivities, snapshot taskSnapshot, flowActions map[string][]domain.FlowAction, subflows map[string][]domain.Subflow, blocks map[string]map[string][]byte) {
	logger := workflow.GetLogger(ctx)

	compensateCtx, _ := workflow.NewDisconnectedContext(ctx)
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    time.Second,
			BackoffCoefficient: 2.0,
			MaximumInterval:    time.Minute,
			MaximumAttempts:    5,
		},
	}
	compensateCtx = workflow.WithActivityOptions(compensateCtx, ao)

	// Restore task
	err := workflow.ExecuteActivity(compensateCtx, activities.RestoreTask, snapshot.Task).Get(compensateCtx, nil)
	if err != nil {
		logger.Error("Failed to restore task during compensation", "error", err)
	}

	// Restore flows
	for _, flow := range snapshot.Flows {
		err := workflow.ExecuteActivity(compensateCtx, activities.RestoreFlow, flow).Get(compensateCtx, nil)
		if err != nil {
			logger.Error("Failed to restore flow during compensation", "flowId", flow.Id, "error", err)
		}
	}

	// Restore flow actions (paginated)
	for flowId, actions := range flowActions {
		for i := 0; i < len(actions); i += maxFlowActionsPerPage {
			end := i + maxFlowActionsPerPage
			if end > len(actions) {
				end = len(actions)
			}
			batch := actions[i:end]
			err := workflow.ExecuteActivity(compensateCtx, activities.RestoreFlowActions, batch).Get(compensateCtx, nil)
			if err != nil {
				logger.Error("Failed to restore flow actions during compensation", "flowId", flowId, "error", err)
			}
		}
	}

	// Restore subflows (paginated)
	for flowId, subs := range subflows {
		for i := 0; i < len(subs); i += maxSubflowsPerPage {
			end := i + maxSubflowsPerPage
			if end > len(subs) {
				end = len(subs)
			}
			batch := subs[i:end]
			err := workflow.ExecuteActivity(compensateCtx, activities.RestoreSubflows, batch).Get(compensateCtx, nil)
			if err != nil {
				logger.Error("Failed to restore subflows during compensation", "flowId", flowId, "error", err)
			}
		}
	}

	// Restore blocks (paginated)
	for flowId, flowBlocks := range blocks {
		keys := make([]string, 0, len(flowBlocks))
		values := make([][]byte, 0, len(flowBlocks))
		for k, v := range flowBlocks {
			keys = append(keys, k)
			values = append(values, v)
		}
		for i := 0; i < len(keys); i += maxBlocksPerPage {
			end := i + maxBlocksPerPage
			if end > len(keys) {
				end = len(keys)
			}
			batchKeys := keys[i:end]
			batchValues := values[i:end]
			err := workflow.ExecuteActivity(compensateCtx, activities.RestoreBlocks, snapshot.Task.WorkspaceId, batchKeys, batchValues).Get(compensateCtx, nil)
			if err != nil {
				logger.Error("Failed to restore blocks during compensation", "flowId", flowId, "error", err)
			}
		}
	}
}

// BuildSnapshot loads task and flows into a snapshot for compensation
func (a *CascadeDeleteTaskActivities) BuildSnapshot(ctx context.Context, workspaceId, taskId string) (taskSnapshot, error) {
	task, err := a.Service.GetTask(ctx, workspaceId, taskId)
	if err != nil {
		return taskSnapshot{}, fmt.Errorf("failed to get task: %w", err)
	}

	flows, err := a.Service.GetFlowsForTask(ctx, workspaceId, taskId)
	if err != nil {
		return taskSnapshot{}, fmt.Errorf("failed to get flows: %w", err)
	}

	return taskSnapshot{
		Task:  task,
		Flows: flows,
	}, nil
}

// GetFlowActionsPage returns a paginated list of flow actions
func (a *CascadeDeleteTaskActivities) GetFlowActionsPage(ctx context.Context, workspaceId, flowId, afterId string) (FlowActionsPage, error) {
	allActions, err := a.Service.GetFlowActions(ctx, workspaceId, flowId)
	if err != nil {
		return FlowActionsPage{}, fmt.Errorf("failed to get flow actions: %w", err)
	}

	// Sort by ID for stable pagination
	sort.Slice(allActions, func(i, j int) bool {
		return allActions[i].Id < allActions[j].Id
	})

	// Filter by afterId if provided
	startIdx := 0
	if afterId != "" {
		for i, action := range allActions {
			if action.Id == afterId {
				startIdx = i + 1
				break
			}
		}
	}

	// Get page
	endIdx := startIdx + maxFlowActionsPerPage
	hasMore := endIdx < len(allActions)
	if endIdx > len(allActions) {
		endIdx = len(allActions)
	}

	return FlowActionsPage{
		FlowId:  flowId,
		Actions: allActions[startIdx:endIdx],
		HasMore: hasMore,
	}, nil
}

// GetSubflowsPage returns a paginated list of subflows
func (a *CascadeDeleteTaskActivities) GetSubflowsPage(ctx context.Context, workspaceId, flowId, afterId string) (SubflowsPage, error) {
	allSubflows, err := a.Service.GetSubflows(ctx, workspaceId, flowId)
	if err != nil {
		return SubflowsPage{}, fmt.Errorf("failed to get subflows: %w", err)
	}

	// Sort by ID for stable pagination
	sort.Slice(allSubflows, func(i, j int) bool {
		return allSubflows[i].Id < allSubflows[j].Id
	})

	// Filter by afterId if provided
	startIdx := 0
	if afterId != "" {
		for i, subflow := range allSubflows {
			if subflow.Id == afterId {
				startIdx = i + 1
				break
			}
		}
	}

	// Get page
	endIdx := startIdx + maxSubflowsPerPage
	hasMore := endIdx < len(allSubflows)
	if endIdx > len(allSubflows) {
		endIdx = len(allSubflows)
	}

	return SubflowsPage{
		FlowId:   flowId,
		Subflows: allSubflows[startIdx:endIdx],
		HasMore:  hasMore,
	}, nil
}

// GetBlocksPage returns a paginated list of KV blocks for a prefix
func (a *CascadeDeleteTaskActivities) GetBlocksPage(ctx context.Context, workspaceId, prefix, afterKey string) (BlocksPage, error) {
	allKeys, err := a.Service.GetKeysWithPrefix(ctx, workspaceId, prefix)
	if err != nil {
		return BlocksPage{}, fmt.Errorf("failed to get keys with prefix: %w", err)
	}

	// Filter by afterKey if provided
	startIdx := 0
	if afterKey != "" {
		for i, key := range allKeys {
			if key == afterKey {
				startIdx = i + 1
				break
			}
		}
	}

	// Get page
	endIdx := startIdx + maxBlocksPerPage
	hasMore := endIdx < len(allKeys)
	if endIdx > len(allKeys) {
		endIdx = len(allKeys)
	}

	pageKeys := allKeys[startIdx:endIdx]
	if len(pageKeys) == 0 {
		return BlocksPage{HasMore: false}, nil
	}

	// Fetch values for the keys
	values, err := a.Service.MGet(ctx, workspaceId, pageKeys)
	if err != nil {
		return BlocksPage{}, fmt.Errorf("failed to get block values: %w", err)
	}

	return BlocksPage{
		Keys:    pageKeys,
		Values:  values,
		HasMore: hasMore,
	}, nil
}

// RestoreBlocks re-persists KV blocks (for compensation)
func (a *CascadeDeleteTaskActivities) RestoreBlocks(ctx context.Context, workspaceId string, keys []string, values [][]byte) error {
	if len(keys) == 0 {
		return nil
	}
	kvMap := make(map[string][]byte, len(keys))
	for i, key := range keys {
		if values[i] != nil {
			kvMap[key] = values[i]
		}
	}
	if len(kvMap) == 0 {
		return nil
	}
	return a.Service.MSetRaw(ctx, workspaceId, kvMap)
}

const temporalLiteNotFoundError1 = "no rows in result set"
const temporalLiteNotFoundError2 = "sql: no rows"
const temporalLiteAlreadyCompletedError = "workflow execution already completed"
const temporalWorkflowNotFoundForId = "workflow not found for ID"

// TerminateFlowWorkflow terminates a flow's Temporal workflow (best-effort)
func (a *CascadeDeleteTaskActivities) TerminateFlowWorkflow(ctx context.Context, flowId string) (bool, error) {
	reason := "CascadeDeleteTask cleanup"
	err := a.TemporalClient.TerminateWorkflow(ctx, flowId, "", reason)
	if err != nil {
		errStr := err.Error()
		if strings.Contains(errStr, temporalWorkflowNotFoundForId) ||
			strings.Contains(errStr, temporalLiteNotFoundError1) ||
			strings.Contains(errStr, temporalLiteNotFoundError2) ||
			strings.Contains(errStr, temporalLiteAlreadyCompletedError) {
			log.Debug().Str("flowId", flowId).Msg("Workflow already terminated or not found")
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// DeleteFlowActions deletes all flow actions for a flow
func (a *CascadeDeleteTaskActivities) DeleteFlowActions(ctx context.Context, workspaceId, flowId string) error {
	return a.Service.DeleteFlowActionsForFlow(ctx, workspaceId, flowId)
}

// DeleteSubflows deletes all subflows for a flow
func (a *CascadeDeleteTaskActivities) DeleteSubflows(ctx context.Context, workspaceId, flowId string) error {
	return a.Service.DeleteSubflowsForFlow(ctx, workspaceId, flowId)
}

// SnapshotFlow loads a single flow for compensation
func (a *CascadeDeleteTaskActivities) SnapshotFlow(ctx context.Context, workspaceId, flowId string) (flowSnapshot, error) {
	flow, err := a.Service.GetFlow(ctx, workspaceId, flowId)
	if err != nil {
		return flowSnapshot{}, fmt.Errorf("failed to get flow: %w", err)
	}
	return flowSnapshot{Flow: flow}, nil
}

// SnapshotTask loads a task for compensation
func (a *CascadeDeleteTaskActivities) SnapshotTask(ctx context.Context, workspaceId, taskId string) (domain.Task, error) {
	task, err := a.Service.GetTask(ctx, workspaceId, taskId)
	if err != nil {
		return domain.Task{}, fmt.Errorf("failed to get task: %w", err)
	}
	return task, nil
}

// DeleteFlow deletes a flow
func (a *CascadeDeleteTaskActivities) DeleteFlow(ctx context.Context, workspaceId, flowId string) error {
	return a.Service.DeleteFlow(ctx, workspaceId, flowId)
}

// DeleteTask deletes a task
func (a *CascadeDeleteTaskActivities) DeleteTask(ctx context.Context, workspaceId, taskId string) error {
	return a.Service.DeleteTask(ctx, workspaceId, taskId)
}

// DeleteKVPrefix deletes all KV entries with the given prefix
func (a *CascadeDeleteTaskActivities) DeleteKVPrefix(ctx context.Context, workspaceId, prefix string) error {
	return a.Service.DeletePrefix(ctx, workspaceId, prefix)
}

// RestoreTask re-persists a task (for compensation)
func (a *CascadeDeleteTaskActivities) RestoreTask(ctx context.Context, task domain.Task) error {
	return a.Service.PersistTask(ctx, task)
}

// RestoreFlow re-persists a flow (for compensation)
func (a *CascadeDeleteTaskActivities) RestoreFlow(ctx context.Context, flow domain.Flow) error {
	return a.Service.PersistFlow(ctx, flow)
}

// RestoreFlowActions re-persists a batch of flow actions (for compensation)
func (a *CascadeDeleteTaskActivities) RestoreFlowActions(ctx context.Context, actions []domain.FlowAction) error {
	for _, action := range actions {
		if err := a.Service.PersistFlowAction(ctx, action); err != nil {
			return err
		}
	}
	return nil
}

// RestoreSubflows re-persists a batch of subflows (for compensation)
func (a *CascadeDeleteTaskActivities) RestoreSubflows(ctx context.Context, subflows []domain.Subflow) error {
	for _, subflow := range subflows {
		if err := a.Service.PersistSubflow(ctx, subflow); err != nil {
			return err
		}
	}
	return nil
}
