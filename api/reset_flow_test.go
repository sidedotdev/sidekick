package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/segmentio/ksuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	workflowpb "go.temporal.io/api/workflow/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"

	"sidekick/dev"
	"sidekick/domain"
	"sidekick/mocks"
)

type resetFixture struct {
	ctrl      Controller
	temporal  *mocks.Client
	workspace string
	task      domain.Task
	iddFlow   domain.Flow
	subtask   domain.Flow
	taskWfId  string
	iddStatus string
}

// newResetFixture persists a blocked task whose IDD flow has one sub-task
// flow. Both flows carry the task as ParentId, exactly as the IDD workflow
// records them, so only Temporal parentage tells the two apart.
func newResetFixture(t *testing.T) *resetFixture {
	ctrl := NewMockController(t)
	ctx := context.Background()
	f := &resetFixture{
		ctrl:      ctrl,
		temporal:  ctrl.temporalClient.(*mocks.Client),
		workspace: "ws_" + ksuid.New().String(),
		iddStatus: "in_progress",
	}
	require.NoError(t, ctrl.service.PersistWorkspace(ctx, domain.Workspace{Id: f.workspace}))

	f.task = domain.Task{
		WorkspaceId: f.workspace,
		Id:          "task_" + ksuid.New().String(),
		Status:      domain.TaskStatusBlocked,
		AgentType:   domain.AgentTypeHuman,
	}
	require.NoError(t, ctrl.service.PersistTask(ctx, f.task))
	f.taskWfId = dev.TaskWorkflowId(f.task.Id)

	f.iddFlow = domain.Flow{
		WorkspaceId: f.workspace,
		Id:          "flow_" + ksuid.New().String(),
		Type:        domain.FlowTypeIdd,
		ParentId:    f.task.Id,
		Status:      f.iddStatus,
	}
	require.NoError(t, ctrl.service.PersistFlow(ctx, f.iddFlow))

	f.subtask = domain.Flow{
		WorkspaceId: f.workspace,
		Id:          "flow_" + ksuid.New().String(),
		Type:        domain.FlowTypeBasicDev,
		ParentId:    f.task.Id,
		Status:      "blocked",
	}
	require.NoError(t, ctrl.service.PersistFlow(ctx, f.subtask))
	return f
}

func (f *resetFixture) describe(workflowId string, status enums.WorkflowExecutionStatus, parentWorkflowId string) {
	info := &workflowpb.WorkflowExecutionInfo{Status: status}
	if parentWorkflowId != "" {
		info.ParentExecution = &commonpb.WorkflowExecution{WorkflowId: parentWorkflowId}
	}
	f.temporal.On("DescribeWorkflowExecution", mock.Anything, workflowId, "").
		Return(&workflowservice.DescribeWorkflowExecutionResponse{WorkflowExecutionInfo: info}, nil)
}

// monitorStarts returns the ExistingFlowId of every task monitor started, so
// tests can assert which flow a restarted monitor adopted, or that none did.
func (f *resetFixture) monitorStarts() []string {
	var adopted []string
	for _, call := range f.temporal.Calls {
		if call.Method != "ExecuteWorkflow" {
			continue
		}
		opts := call.Arguments.Get(1).(client.StartWorkflowOptions)
		if opts.ID != f.taskWfId {
			continue
		}
		adopted = append(adopted, call.Arguments.Get(3).(dev.TaskWorkflowInput).ExistingFlowId)
	}
	return adopted
}

func (f *resetFixture) currentStatuses(t *testing.T) (task domain.TaskStatus, idd, subtask string) {
	ctx := context.Background()
	gotTask, err := f.ctrl.service.GetTask(ctx, f.workspace, f.task.Id)
	require.NoError(t, err)
	gotIdd, err := f.ctrl.service.GetFlow(ctx, f.workspace, f.iddFlow.Id)
	require.NoError(t, err)
	gotSubtask, err := f.ctrl.service.GetFlow(ctx, f.workspace, f.subtask.Id)
	require.NoError(t, err)
	return gotTask.Status, gotIdd.Status, gotSubtask.Status
}

// A flow started directly by the task workflow is bound to it through a child
// future that a reset breaks, so the monitor must be re-executed against the
// reset flow, and a rollback must undo that.
func TestResetParentTaskWorkflow_TopLevelFlowReplacesMonitor(t *testing.T) {
	t.Parallel()
	f := newResetFixture(t)
	f.describe(f.iddFlow.Id, enums.WORKFLOW_EXECUTION_STATUS_RUNNING, f.taskWfId)
	f.describe(f.taskWfId, enums.WORKFLOW_EXECUTION_STATUS_RUNNING, "")
	f.temporal.On("TerminateWorkflow", mock.Anything, f.taskWfId, "", "re-executing for flow reset").Return(nil).Once()

	result, err := f.ctrl.resetParentTaskWorkflow(context.Background(), f.workspace, f.iddFlow.Id)
	require.NoError(t, err)
	assert.True(t, result.didReset)
	assert.Equal(t, []string{f.iddFlow.Id}, f.monitorStarts())

	task, idd, _ := f.currentStatuses(t)
	assert.Equal(t, domain.TaskStatusInProgress, task)
	assert.Equal(t, "in_progress", idd)

	f.temporal.On("TerminateWorkflow", mock.Anything, f.taskWfId, "", "rolling back parent reset after child reset failure").Return(nil).Once()
	result.rollback(context.Background(), &f.ctrl)
	task, idd, _ = f.currentStatuses(t)
	assert.Equal(t, domain.TaskStatusBlocked, task)
	assert.Equal(t, f.iddStatus, idd)
}

// An IDD sub-task is a child of the IDD workflow, not of the task workflow, so
// resetting it leaves the task workflow's own child future intact. The running
// monitor is already correct and must be left alone; replacing it with one
// adopting the sub-task would have it record the IDD flow's eventual closure
// on the sub-task's flow record.
func TestResetParentTaskWorkflow_SubtaskLeavesRunningMonitorAlone(t *testing.T) {
	t.Parallel()
	f := newResetFixture(t)
	f.describe(f.subtask.Id, enums.WORKFLOW_EXECUTION_STATUS_RUNNING, f.iddFlow.Id)
	f.describe(f.iddFlow.Id, enums.WORKFLOW_EXECUTION_STATUS_RUNNING, f.taskWfId)
	f.describe(f.taskWfId, enums.WORKFLOW_EXECUTION_STATUS_RUNNING, "")

	result, err := f.ctrl.resetParentTaskWorkflow(context.Background(), f.workspace, f.subtask.Id)
	require.NoError(t, err)
	assert.False(t, result.didReset)
	assert.Empty(t, f.monitorStarts())
	f.temporal.AssertNotCalled(t, "TerminateWorkflow", mock.Anything, mock.Anything, mock.Anything, mock.Anything)

	task, idd, subtask := f.currentStatuses(t)
	assert.Equal(t, domain.TaskStatusBlocked, task)
	assert.Equal(t, f.iddStatus, idd)
	assert.Equal(t, "blocked", subtask)

	// the no-op handle must stay a no-op when the child reset then fails
	result.rollback(context.Background(), &f.ctrl)
	f.temporal.AssertNotCalled(t, "TerminateWorkflow", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

// When the monitor for an IDD sub-task's task is gone, the replacement must
// adopt the IDD flow the task workflow actually tracks, not the sub-task.
func TestResetParentTaskWorkflow_SubtaskRestartsMissingMonitorOnIddFlow(t *testing.T) {
	t.Parallel()
	f := newResetFixture(t)
	f.describe(f.subtask.Id, enums.WORKFLOW_EXECUTION_STATUS_RUNNING, f.iddFlow.Id)
	f.describe(f.iddFlow.Id, enums.WORKFLOW_EXECUTION_STATUS_RUNNING, f.taskWfId)
	f.describe(f.taskWfId, enums.WORKFLOW_EXECUTION_STATUS_TERMINATED, "")

	result, err := f.ctrl.resetParentTaskWorkflow(context.Background(), f.workspace, f.subtask.Id)
	require.NoError(t, err)
	assert.True(t, result.didReset)
	assert.Equal(t, []string{f.iddFlow.Id}, f.monitorStarts())
	f.temporal.AssertNotCalled(t, "TerminateWorkflow", mock.Anything, mock.Anything, mock.Anything, mock.Anything)

	task, idd, subtask := f.currentStatuses(t)
	assert.Equal(t, domain.TaskStatusInProgress, task)
	assert.Equal(t, f.iddStatus, idd)
	assert.Equal(t, "in_progress", subtask)
}

// A task workflow that no longer exists at all is the same situation as a
// closed one: it must be recreated against the flow the task tracks, and the
// rollback for that recreation must still work for a nested flow.
func TestResetParentTaskWorkflow_SubtaskRecreatesNotFoundMonitorOnIddFlow(t *testing.T) {
	t.Parallel()
	f := newResetFixture(t)
	f.describe(f.subtask.Id, enums.WORKFLOW_EXECUTION_STATUS_RUNNING, f.iddFlow.Id)
	f.describe(f.iddFlow.Id, enums.WORKFLOW_EXECUTION_STATUS_RUNNING, f.taskWfId)
	f.temporal.On("DescribeWorkflowExecution", mock.Anything, f.taskWfId, "").
		Return(nil, serviceerror.NewNotFound("workflow not found"))

	result, err := f.ctrl.resetParentTaskWorkflow(context.Background(), f.workspace, f.subtask.Id)
	require.NoError(t, err)
	assert.True(t, result.didReset)
	assert.Equal(t, []string{f.iddFlow.Id}, f.monitorStarts())

	task, idd, subtask := f.currentStatuses(t)
	assert.Equal(t, domain.TaskStatusInProgress, task)
	assert.Equal(t, f.iddStatus, idd)
	assert.Equal(t, "in_progress", subtask)

	f.temporal.On("TerminateWorkflow", mock.Anything, f.taskWfId, "", "rolling back parent reset after child reset failure").Return(nil).Once()
	result.rollback(context.Background(), &f.ctrl)
	task, idd, subtask = f.currentStatuses(t)
	assert.Equal(t, domain.TaskStatusBlocked, task)
	assert.Equal(t, f.iddStatus, idd)
	assert.Equal(t, "blocked", subtask)
}

// Anything other than a definitive "not found" leaves the monitor's state
// unknown, so the reset must not proceed on a guess.
func TestResetParentTaskWorkflow_TaskWorkflowLookupErrorRefusesReset(t *testing.T) {
	t.Parallel()
	f := newResetFixture(t)
	f.describe(f.iddFlow.Id, enums.WORKFLOW_EXECUTION_STATUS_RUNNING, f.taskWfId)
	f.temporal.On("DescribeWorkflowExecution", mock.Anything, f.taskWfId, "").
		Return(nil, serviceerror.NewUnavailable("temporal down"))

	result, err := f.ctrl.resetParentTaskWorkflow(context.Background(), f.workspace, f.iddFlow.Id)
	require.Error(t, err)
	assert.False(t, result.didReset)
	assert.Empty(t, f.monitorStarts())
	f.temporal.AssertNotCalled(t, "TerminateWorkflow", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

// The monitor is only re-pointed at an enclosing flow when that flow is what
// the task workflow started; any other nesting is not something reset knows
// how to supervise.
func TestResetParentTaskWorkflow_RejectsEnclosingFlowNotUnderTaskWorkflow(t *testing.T) {
	t.Parallel()
	f := newResetFixture(t)
	f.describe(f.subtask.Id, enums.WORKFLOW_EXECUTION_STATUS_RUNNING, f.iddFlow.Id)
	f.describe(f.iddFlow.Id, enums.WORKFLOW_EXECUTION_STATUS_RUNNING, "task_someone_else")

	result, err := f.ctrl.resetParentTaskWorkflow(context.Background(), f.workspace, f.subtask.Id)
	require.Error(t, err)
	assert.False(t, result.didReset)
	assert.Empty(t, f.monitorStarts())
	f.temporal.AssertNotCalled(t, "TerminateWorkflow", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func (f *resetFixture) postReset(t *testing.T, flowId string, eventId int64) *httptest.ResponseRecorder {
	body, err := json.Marshal(ResetFlowRequest{EventId: eventId})
	require.NoError(t, err)
	resp := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(resp)
	c.Request = httptest.NewRequest(http.MethodPost, "/reset", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Params = []gin.Param{{Key: "workspaceId", Value: f.workspace}, {Key: "id", Value: flowId}}
	f.ctrl.ResetFlowHandler(c)
	return resp
}

// Resetting a sub-task through the handler still resets exactly that
// sub-task's execution while its supervising workflows carry on untouched.
func TestResetFlowHandler_SubtaskResetsChildOnly(t *testing.T) {
	t.Parallel()
	f := newResetFixture(t)
	f.describe(f.subtask.Id, enums.WORKFLOW_EXECUTION_STATUS_RUNNING, f.iddFlow.Id)
	f.describe(f.iddFlow.Id, enums.WORKFLOW_EXECUTION_STATUS_RUNNING, f.taskWfId)
	f.describe(f.taskWfId, enums.WORKFLOW_EXECUTION_STATUS_RUNNING, "")
	f.temporal.On("ResetWorkflowExecution", mock.Anything, mock.MatchedBy(func(req *workflowservice.ResetWorkflowExecutionRequest) bool {
		return req.WorkflowExecution.GetWorkflowId() == f.subtask.Id && req.WorkflowTaskFinishEventId == 42
	})).Return(&workflowservice.ResetWorkflowExecutionResponse{RunId: "run_new"}, nil).Once()

	resp := f.postReset(t, f.subtask.Id, 42)
	assert.Equal(t, http.StatusOK, resp.Code)
	assert.Contains(t, resp.Body.String(), "run_new")
	assert.Empty(t, f.monitorStarts())
	f.temporal.AssertNotCalled(t, "TerminateWorkflow", mock.Anything, mock.Anything, mock.Anything, mock.Anything)

	task, idd, subtask := f.currentStatuses(t)
	assert.Equal(t, domain.TaskStatusBlocked, task)
	assert.Equal(t, f.iddStatus, idd)
	assert.Equal(t, "blocked", subtask)
}

// With the IDD workflow gone, the handler refuses before touching Temporal
// state, so no orphaned sub-task run is ever created.
func TestResetFlowHandler_SubtaskRefusedWhenIddFlowStopped(t *testing.T) {
	t.Parallel()
	f := newResetFixture(t)
	f.describe(f.subtask.Id, enums.WORKFLOW_EXECUTION_STATUS_RUNNING, f.iddFlow.Id)
	f.describe(f.iddFlow.Id, enums.WORKFLOW_EXECUTION_STATUS_CANCELED, f.taskWfId)

	resp := f.postReset(t, f.subtask.Id, 42)
	assert.Equal(t, http.StatusInternalServerError, resp.Code)
	assert.Contains(t, resp.Body.String(), f.iddFlow.Id)
	f.temporal.AssertNotCalled(t, "ResetWorkflowExecution", mock.Anything, mock.Anything)
	f.temporal.AssertNotCalled(t, "TerminateWorkflow", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	assert.Empty(t, f.monitorStarts())
}

// A sub-task cannot be reset under a parent flow that is no longer running:
// nothing would supervise the new run, so the reset is refused outright rather
// than guessing at a recovery.
func TestResetParentTaskWorkflow_SubtaskRefusedWhenIddFlowNotRunning(t *testing.T) {
	t.Parallel()
	f := newResetFixture(t)
	f.describe(f.subtask.Id, enums.WORKFLOW_EXECUTION_STATUS_RUNNING, f.iddFlow.Id)
	f.describe(f.iddFlow.Id, enums.WORKFLOW_EXECUTION_STATUS_COMPLETED, f.taskWfId)

	result, err := f.ctrl.resetParentTaskWorkflow(context.Background(), f.workspace, f.subtask.Id)
	require.Error(t, err)
	assert.Contains(t, err.Error(), f.iddFlow.Id)
	assert.False(t, result.didReset)
	assert.Empty(t, f.monitorStarts())
	f.temporal.AssertNotCalled(t, "TerminateWorkflow", mock.Anything, mock.Anything, mock.Anything, mock.Anything)

	task, idd, subtask := f.currentStatuses(t)
	assert.Equal(t, domain.TaskStatusBlocked, task)
	assert.Equal(t, f.iddStatus, idd)
	assert.Equal(t, "blocked", subtask)
}
