package flow_action

import (
	"encoding/json"
	"errors"
	"fmt"
	"sidekick/domain"
	"sidekick/utils"
	"time"

	"go.temporal.io/sdk/workflow"
)

// TrackOptions configures how flow actions are tracked
type TrackOptions struct {
	FailuresOnly bool
}

/*
Track wraps an anonymous func with flow action tracking. It does this:

 1. Create a "started" flow action for the given scope
 2. Execute the given function, which should perform the action being tracked
 3. Updates the status/result of the flow action based on the result of the function

Note: instead of a method on FlowActionScope, this is defined as a function
to allow for generic return type
TODO: /gen write a set of tests for Track. use a real redis db instance via
the existing newTestRedisDatabase fucntion and confirm started/failed/completed statuses.
*/
func Track[T any](actionCtx ActionContext, f func(flowAction *domain.FlowAction) (T, error)) (defaultT T, err error) {
	if actionCtx.ExecContext.FlowScope == nil {
		panic("Missing FlowScope in ExecContext when tracking flow action")
	}
	return trackFlowAction(actionCtx.ExecContext, false, actionCtx.ActionType, actionCtx.ActionParams, f)
}

func TrackFailureOnly[T any](actionCtx ActionContext, f func(flowAction *domain.FlowAction) (T, error)) (defaultT T, err error) {
	if actionCtx.ExecContext.FlowScope == nil {
		panic("Missing FlowScope in ExecContext when tracking flow action")
	}
	return trackFlowActionFailureOnly(actionCtx.ExecContext, actionCtx.ActionType, actionCtx.ActionParams, f)
}

// TrackWithOptions wraps an anonymous func with flow action tracking based on the provided options
func TrackWithOptions[T any](actionCtx ActionContext, options TrackOptions, f func(flowAction *domain.FlowAction) (T, error)) (defaultT T, err error) {
	if actionCtx.ExecContext.FlowScope == nil {
		panic("Missing FlowScope in ExecContext when tracking flow action")
	}

	if options.FailuresOnly {
		return trackFlowActionFailureOnly(actionCtx.ExecContext, actionCtx.ActionType, actionCtx.ActionParams, f)
	}

	return trackFlowAction(actionCtx.ExecContext, false, actionCtx.ActionType, actionCtx.ActionParams, f)
}

/*
Just like Track, but for human actions. This sets up the required metadata to
ensure the human can complete the action.
*/
func TrackHuman[T any](actionCtx ActionContext, f func(flowAction *domain.FlowAction) (T, error)) (T, error) {
	if actionCtx.ExecContext.FlowScope == nil {
		panic("Missing FlowScope in ExecContext when tracking flow action")
	}
	return trackFlowAction(actionCtx.ExecContext, true, actionCtx.ActionType, actionCtx.ActionParams, f)
}

func TrackSubflow[T any](eCtx ExecContext, subflowType, subflowName string, f func(subflow domain.Subflow) (T, error)) (T, error) {
	if eCtx.FlowScope == nil {
		panic("Missing FlowScope in ExecContext when tracking subflow")
	}
	return trackSubflow(eCtx, false, subflowType, subflowName, f)
}

func TrackDetachedSubflow[T any](eCtx ExecContext, subflowType, subflowName string, f func(subflow domain.Subflow) (T, error)) (T, error) {
	if eCtx.FlowScope == nil {
		panic("Missing FlowScope in ExecContext when tracking subflow")
	}
	return trackSubflow(eCtx, true, subflowType, subflowName, f)
}

func TrackSubflowFailureOnly[T any](eCtx ExecContext, subflowType, subflowName string, f func(subflow domain.Subflow) (T, error)) (T, error) {
	if eCtx.FlowScope == nil {
		panic("Missing FlowScope in ExecContext when tracking subflow")
	}
	return trackSubflowFailureOnly(eCtx, subflowType, subflowName, f)
}

func TrackSubflowWithoutResult(eCtx ExecContext, subflowType, subflowName string, f func(subflow domain.Subflow) error) error {
	if eCtx.FlowScope == nil {
		panic("Missing FlowScope in ExecContext when tracking subflow")
	}
	_, err := trackSubflow(eCtx, false, subflowType, subflowName, func(subflow domain.Subflow) (_ string, err error) {
		return "", f(subflow)
	})
	return err
}

// use an uncommon separator so we can split on it later reliably without a risk
// of splitting up a scope name
// FIXME this is legacy and is being replaced by the Subflow model, but we're
// keep this around for now until we update the frontend to not rely on it
const legacySubflowNameSeparator = ":|:"

func trackSubflow[T any](eCtx ExecContext, detached bool, subflowType, subflowName string, f func(subflow domain.Subflow) (T, error)) (defaultT T, err error) {
	parentSubflow := eCtx.FlowScope.Subflow
	subflow, err := putSubflow(eCtx, setupSubflow(eCtx, detached, subflowType, subflowName))
	if err != nil {
		return defaultT, err
	}
	(*eCtx.FlowScope).Subflow = &subflow

	// handle legacy subflow name value
	originalSubflowName := eCtx.FlowScope.SubflowName
	if originalSubflowName == "" {
		(*eCtx.FlowScope).SubflowName = subflow.Name
	} else {
		(*eCtx.FlowScope).SubflowName = fmt.Sprintf("%s%s%s", originalSubflowName, legacySubflowNameSeparator, subflow.Name)
	}

	defer func() {
		(*eCtx.FlowScope).Subflow = parentSubflow

		// handle legacy subflow name value
		(*eCtx.FlowScope).SubflowName = originalSubflowName
	}()

	val, err := f(subflow)

	if err != nil {
		if errors.Is(err, PendingActionError) {
			subflow.Status = domain.SubflowStatusCanceled
			subflow.Result = fmt.Sprintf("canceled: %v", err)
		} else {
			subflow.Status = domain.SubflowStatusFailed
			subflow.Result = fmt.Sprintf("failed: %v", err)
		}
		updateECtx := eCtx
		if v := workflow.GetVersion(eCtx, "disconnected-context", workflow.DefaultVersion, 1); v == 1 {
			disconnectedWorkflowCtx, _ := workflow.NewDisconnectedContext(eCtx.Context)
			updateECtx.Context = disconnectedWorkflowCtx
		}
		_, err2 := putSubflow(updateECtx, subflow)
		if err2 != nil {
			return defaultT, fmt.Errorf("failed to mark subflow as %s: %v\noriginal error: %v", subflow.Status, err2, err)
		}
		return defaultT, err
	}

	jsonVal, err := json.Marshal(val)
	if err != nil {
		return defaultT, fmt.Errorf("failed to convert val to json: %v", err)
	}
	subflow.Result = string(jsonVal)
	subflow.Status = domain.SubflowStatusComplete
	updateECtx := eCtx
	if v := workflow.GetVersion(eCtx, "disconnected-context", workflow.DefaultVersion, 1); v == 1 {
		disconnectedWorkflowCtx, _ := workflow.NewDisconnectedContext(eCtx.Context)
		updateECtx.Context = disconnectedWorkflowCtx
	}
	_, err = putSubflow(updateECtx, subflow)
	if err != nil {
		return defaultT, fmt.Errorf("failed to mark subflow as complete: %v", err)
	}

	return val, nil
}

func trackSubflowFailureOnly[T any](eCtx ExecContext, subflowType, subflowName string, f func(subflow domain.Subflow) (T, error)) (defaultT T, err error) {
	parentSubflow := eCtx.FlowScope.Subflow
	subflow := setupSubflow(eCtx, false, subflowType, subflowName) // don't persist the subflow yet, only do it if & when it fails

	(*eCtx.FlowScope).Subflow = &subflow

	// handle legacy subflow name value
	originalSubflowName := eCtx.FlowScope.SubflowName
	if originalSubflowName == "" {
		(*eCtx.FlowScope).SubflowName = subflow.Name
	} else {
		(*eCtx.FlowScope).SubflowName = fmt.Sprintf("%s%s%s", originalSubflowName, legacySubflowNameSeparator, subflow.Name)
	}

	defer func() {
		(*eCtx.FlowScope).Subflow = parentSubflow

		// handle legacy subflow name value
		(*eCtx.FlowScope).SubflowName = originalSubflowName
	}()

	val, err := f(subflow)

	if err != nil {
		if errors.Is(err, PendingActionError) {
			subflow.Status = domain.SubflowStatusCanceled
			subflow.Result = fmt.Sprintf("canceled: %v", err)
		} else {
			subflow.Status = domain.SubflowStatusFailed
			subflow.Result = fmt.Sprintf("failed: %v", err)
		}
		updateECtx := eCtx
		if v := workflow.GetVersion(eCtx, "disconnected-context", workflow.DefaultVersion, 1); v == 1 {
			disconnectedWorkflowCtx, _ := workflow.NewDisconnectedContext(eCtx.Context)
			updateECtx.Context = disconnectedWorkflowCtx
		}
		_, err2 := putSubflow(updateECtx, subflow)
		if err2 != nil {
			return defaultT, fmt.Errorf("failed to mark subflow as %s: %v\noriginal error: %v", subflow.Status, err2, err)
		}
		return defaultT, err
	}

	return val, nil
}

func trackFlowAction[T any](eCtx ExecContext, isHumanAction bool, actionType string, actionParams map[string]any, f func(flowAction *domain.FlowAction) (T, error)) (defaultT T, err error) {
	initialStatus := domain.ActionStatusStarted
	if isHumanAction {
		initialStatus = domain.ActionStatusPending
	}

	flowAction := &domain.FlowAction{
		WorkspaceId:        eCtx.WorkspaceId,
		SubflowName:        eCtx.FlowScope.SubflowName,
		SubflowDescription: eCtx.FlowScope.subflowDescription,
		ActionType:         actionType,
		ActionStatus:       initialStatus,
		ActionParams:       actionParams,
		IsHumanAction:      isHumanAction,
		IsCallbackAction:   isHumanAction, // human actions are always callback actions and the only ones for now
	}
	persisted, err := putFlowAction(eCtx, *flowAction)
	if err != nil {
		return defaultT, err
	}
	*flowAction = persisted

	// perform the actual action being tracked
	val, err := f(flowAction)

	if err != nil {
		flowAction.ActionStatus = domain.ActionStatusFailed
		flowAction.ActionResult = fmt.Sprintf("failed: %v", err)
		var err2 error
		updateECtx := eCtx
		if v := workflow.GetVersion(eCtx, "disconnected-context", workflow.DefaultVersion, 1); v == 1 {
			disconnectedWorkflowCtx, _ := workflow.NewDisconnectedContext(eCtx.Context)
			updateECtx.Context = disconnectedWorkflowCtx
		}
		var persistedFailure domain.FlowAction
		persistedFailure, err2 = putFlowAction(updateECtx, *flowAction)
		if err2 != nil {
			return defaultT, fmt.Errorf("failed to mark flow action as failed: %v\noriginal failure: %v", err2, err)
		}
		*flowAction = persistedFailure
		return defaultT, err
	}

	flowAction.ActionStatus = domain.ActionStatusComplete
	jsonVal, err := json.Marshal(val)
	if err != nil {
		return defaultT, fmt.Errorf("failed to convert val to json: %v", err)
	}
	flowAction.ActionResult = string(jsonVal)
	updateECtx := eCtx
	if v := workflow.GetVersion(eCtx, "disconnected-context", workflow.DefaultVersion, 1); v == 1 {
		disconnectedWorkflowCtx, _ := workflow.NewDisconnectedContext(eCtx.Context)
		updateECtx.Context = disconnectedWorkflowCtx
	}
	persisted, err = putFlowAction(updateECtx, *flowAction)
	if err != nil {
		return val, fmt.Errorf("failed to mark flow action as completed: %v", err)
	}
	*flowAction = persisted

	return val, nil
}

func trackFlowActionFailureOnly[T any](eCtx ExecContext, actionType string, actionParams map[string]any, f func(flowAction *domain.FlowAction) (T, error)) (defaultT T, err error) {
	initialStatus := domain.ActionStatusStarted
	flowAction := &domain.FlowAction{
		WorkspaceId:        eCtx.WorkspaceId,
		SubflowName:        eCtx.FlowScope.SubflowName,
		SubflowDescription: eCtx.FlowScope.subflowDescription,
		ActionType:         actionType,
		ActionStatus:       initialStatus,
		ActionParams:       actionParams,
	}

	// perform the actual action being tracked
	val, err := f(flowAction)

	if err != nil {
		flowAction.ActionStatus = domain.ActionStatusFailed
		flowAction.ActionResult = fmt.Sprintf("failed: %v", err)
		var err2 error
		updateECtx := eCtx
		if v := workflow.GetVersion(eCtx, "disconnected-context", workflow.DefaultVersion, 1); v == 1 {
			disconnectedWorkflowCtx, _ := workflow.NewDisconnectedContext(eCtx.Context)
			updateECtx.Context = disconnectedWorkflowCtx
		}
		var persistedFailure domain.FlowAction
		persistedFailure, err2 = putFlowAction(updateECtx, *flowAction)
		if err2 != nil {
			return defaultT, fmt.Errorf("failed to mark flow action as failed: %v\noriginal failure: %v", err2, err)
		}
		*flowAction = persistedFailure
		return defaultT, err
	}

	// if no err, we don't persist a flow action, just return early
	return val, nil
}

func putFlowAction(eCtx ExecContext, flowAction domain.FlowAction) (domain.FlowAction, error) {
	if flowAction.Id == "" {
		flowAction.Id = "fa_" + utils.KsuidSideEffect(eCtx)
	}

	if flowAction.FlowId == "" {
		flowAction.FlowId = workflow.GetInfo(eCtx).WorkflowExecution.ID
	}

	if flowAction.SubflowId == "" {
		if eCtx.FlowScope.Subflow != nil {
			flowAction.SubflowId = eCtx.FlowScope.Subflow.Id
		}
	}

	if flowAction.Created.IsZero() {
		flowAction.Created = time.Now()
	}
	flowAction.Updated = time.Now()

	var fa *FlowActivities // nil struct pointer for struct-based activities

	//fmt.Printf("putFlowAction with:\n")
	//utils.PrettyPrint(flowAction)
	err := workflow.ExecuteActivity(eCtx, fa.PersistFlowAction, flowAction).Get(eCtx, nil)
	if err != nil {
		return domain.FlowAction{}, err
	}

	return flowAction, nil
}

func setupSubflow(eCtx ExecContext, detached bool, subflowType, subflowName string) domain.Subflow {
	parentSubflowId := ""
	parentSubflow := eCtx.FlowScope.Subflow
	if parentSubflow != nil && !detached {
		parentSubflowId = parentSubflow.Id
	}

	subflow := domain.Subflow{
		WorkspaceId:     eCtx.WorkspaceId,
		Type:            &subflowType,
		Name:            subflowName,
		ParentSubflowId: parentSubflowId,
		Status:          domain.SubflowStatusStarted,
	}

	if subflow.Id == "" {
		subflow.Id = "sf_" + utils.KsuidSideEffect(eCtx)
	}
	if subflow.FlowId == "" {
		subflow.FlowId = workflow.GetInfo(eCtx).WorkflowExecution.ID
	}

	return subflow
}

func putSubflow(eCtx ExecContext, subflow domain.Subflow) (domain.Subflow, error) {
	var fa *FlowActivities // nil struct pointer for struct-based activities
	err := workflow.ExecuteActivity(eCtx, fa.PersistSubflow, subflow).Get(eCtx, nil)
	if err != nil {
		return domain.Subflow{}, err
	}
	return subflow, nil
}
