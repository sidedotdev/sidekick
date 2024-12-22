package flow_action

import (
	"encoding/json"
	"fmt"
	"sidekick/models"
	"time"

	"github.com/segmentio/ksuid"
	"go.temporal.io/sdk/workflow"
)

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
func Track[T any](actionCtx ActionContext, f func(flowAction models.FlowAction) (T, error)) (defaultT T, err error) {
	if actionCtx.ExecContext.FlowScope == nil {
		panic("Missing FlowScope in ExecContext when tracking flow action")
	}
	return trackFlowAction(actionCtx.ExecContext, false, actionCtx.ActionType, actionCtx.ActionParams, f)
}

func TrackFailureOnly[T any](actionCtx ActionContext, f func(flowAction models.FlowAction) (T, error)) (defaultT T, err error) {
	if actionCtx.ExecContext.FlowScope == nil {
		panic("Missing FlowScope in ExecContext when tracking flow action")
	}
	return trackFlowActionFailureOnly(actionCtx.ExecContext, actionCtx.ActionType, actionCtx.ActionParams, f)
}

/*
Just like Track, but for human actions. This sets up the required metadata to
ensure the human can complete the action.
*/
func TrackHuman[T any](actionCtx ActionContext, f func(flowAction models.FlowAction) (T, error)) (T, error) {
	if actionCtx.ExecContext.FlowScope == nil {
		panic("Missing FlowScope in ExecContext when tracking flow action")
	}
	return trackFlowAction(actionCtx.ExecContext, true, actionCtx.ActionType, actionCtx.ActionParams, f)
}

func TrackSubflow[T any](eCtx ExecContext, subflowName string, f func(subflow models.Subflow) (T, error)) (T, error) {
	if eCtx.FlowScope == nil {
		panic("Missing FlowScope in ExecContext when tracking subflow")
	}
	return trackSubflow(eCtx, subflowName, f)
}

func TrackSubflowFailureOnly[T any](eCtx ExecContext, subflowName string, f func(subflow models.Subflow) (T, error)) (T, error) {
	if eCtx.FlowScope == nil {
		panic("Missing FlowScope in ExecContext when tracking subflow")
	}
	return trackSubflowFailureOnly(eCtx, subflowName, f)
}

func TrackSubflowWithoutResult(eCtx ExecContext, subflowName string, f func(subflow models.Subflow) error) error {
	if eCtx.FlowScope == nil {
		panic("Missing FlowScope in ExecContext when tracking subflow")
	}
	_, err := trackSubflow(eCtx, subflowName, func(subflow models.Subflow) (_ string, err error) {
		return "", f(subflow)
	})
	return err
}

// use an uncommon separator so we can split on it later reliably without a risk
// of splitting up a scope name
// FIXME this is legacy and is being replaced by the Subflow model, but we're
// keep this around for now until we update the frontend to not rely on it
const legacySubflowNameSeparator = ":|:"

func trackSubflow[T any](eCtx ExecContext, subflowName string, f func(subflow models.Subflow) (T, error)) (defaultT T, err error) {
	parentSubflow := eCtx.FlowScope.Subflow
	subflow, err := putSubflow(eCtx, setupSubflow(eCtx, subflowName))
	if err != nil {
		return defaultT, err
	}
	eCtx.FlowScope.Subflow = &subflow

	// handle legacy subflow name value
	originalSubflowName := eCtx.FlowScope.SubflowName
	if originalSubflowName == "" {
		eCtx.FlowScope.SubflowName = subflow.Name
	} else {
		eCtx.FlowScope.SubflowName = fmt.Sprintf("%s%s%s", originalSubflowName, legacySubflowNameSeparator, subflow.Name)
	}

	defer func() {
		eCtx.FlowScope.Subflow = parentSubflow

		// handle legacy subflow name value
		eCtx.FlowScope.SubflowName = originalSubflowName
	}()

	val, err := f(subflow)

	if err != nil {
		subflow.Status = models.SubflowStatusFailed
		subflow.Result = fmt.Sprintf("failed: %v", err)
		_, err2 := putSubflow(eCtx, subflow)
		if err2 != nil {
			return defaultT, fmt.Errorf("failed to mark subflow as failed: %v\noriginal failure: %v", err2, err)
		}
		return defaultT, err
	}

	jsonVal, err := json.Marshal(val)
	if err != nil {
		return defaultT, fmt.Errorf("failed to convert val to json: %v", err)
	}
	subflow.Result = string(jsonVal)
	subflow.Status = models.SubflowStatusComplete
	_, err = putSubflow(eCtx, subflow)
	if err != nil {
		return defaultT, fmt.Errorf("failed to mark subflow as complete: %v", err)
	}

	return val, nil
}

func trackSubflowFailureOnly[T any](eCtx ExecContext, subflowName string, f func(subflow models.Subflow) (T, error)) (defaultT T, err error) {
	parentSubflow := eCtx.FlowScope.Subflow
	subflow := setupSubflow(eCtx, subflowName) // don't persist the subflow yet, only do it if & when it fails

	eCtx.FlowScope.Subflow = &subflow

	// handle legacy subflow name value
	originalSubflowName := eCtx.FlowScope.SubflowName
	if originalSubflowName == "" {
		eCtx.FlowScope.SubflowName = subflow.Name
	} else {
		eCtx.FlowScope.SubflowName = fmt.Sprintf("%s%s%s", originalSubflowName, legacySubflowNameSeparator, subflow.Name)
	}

	defer func() {
		eCtx.FlowScope.Subflow = parentSubflow

		// handle legacy subflow name value
		eCtx.FlowScope.SubflowName = originalSubflowName
	}()

	val, err := f(subflow)

	if err != nil {
		subflow.Status = models.SubflowStatusFailed
		subflow.Result = fmt.Sprintf("failed: %v", err)
		_, err2 := putSubflow(eCtx, subflow)
		if err2 != nil {
			return defaultT, fmt.Errorf("failed to mark subflow as failed: %v\noriginal failure: %v", err2, err)
		}
		return defaultT, err
	}

	return val, nil
}

func trackFlowAction[T any](eCtx ExecContext, isHumanAction bool, actionType string, actionParams map[string]any, f func(streamId models.FlowAction) (T, error)) (defaultT T, err error) {
	initialStatus := models.ActionStatusStarted
	if isHumanAction {
		initialStatus = models.ActionStatusPending
	}

	flowAction, err := putFlowAction(eCtx, models.FlowAction{
		WorkspaceId:        eCtx.WorkspaceId,
		SubflowName:        eCtx.FlowScope.SubflowName,
		SubflowDescription: eCtx.FlowScope.subflowDescription,
		ActionType:         actionType,
		ActionStatus:       initialStatus,
		ActionParams:       actionParams,
		IsHumanAction:      isHumanAction,
		IsCallbackAction:   isHumanAction, // human actions are always callback actions and the only ones for now
	})
	if err != nil {
		return defaultT, err
	}

	// perform the actual action being tracked
	val, err := f(flowAction)

	if err != nil {
		flowAction.ActionStatus = models.ActionStatusFailed
		flowAction.ActionResult = fmt.Sprintf("failed: %v", err)
		var err2 error
		flowAction, err2 = putFlowAction(eCtx, flowAction)
		if err2 != nil {
			return defaultT, fmt.Errorf("failed to mark flow action as failed: %v\noriginal failure: %v", err2, err)
		}
		return defaultT, err
	}

	flowAction.ActionStatus = models.ActionStatusComplete
	jsonVal, err := json.Marshal(val)
	if err != nil {
		return defaultT, fmt.Errorf("failed to convert val to json: %v", err)
	}
	flowAction.ActionResult = string(jsonVal)
	flowAction, err = putFlowAction(eCtx, flowAction)
	if err != nil {
		return val, fmt.Errorf("failed to mark flow action as completed: %v", err)
	}

	return val, nil
}

func trackFlowActionFailureOnly[T any](eCtx ExecContext, actionType string, actionParams map[string]any, f func(streamId models.FlowAction) (T, error)) (defaultT T, err error) {
	initialStatus := models.ActionStatusStarted
	flowAction := models.FlowAction{
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
		flowAction.ActionStatus = models.ActionStatusFailed
		flowAction.ActionResult = fmt.Sprintf("failed: %v", err)
		var err2 error
		flowAction, err2 = putFlowAction(eCtx, flowAction)
		if err2 != nil {
			return defaultT, fmt.Errorf("failed to mark flow action as failed: %v\noriginal failure: %v", err2, err)
		}
		return defaultT, err
	}

	// if no err, we don't persist a flow action, just return early
	return val, nil
}

func putFlowAction(eCtx ExecContext, flowAction models.FlowAction) (models.FlowAction, error) {
	if flowAction.Id == "" {
		flowAction.Id = "fa_" + ksuid.New().String()
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
	err := workflow.ExecuteActivity(eCtx, fa.PersistFlowAction, flowAction).Get(eCtx, nil)
	if err != nil {
		return models.FlowAction{}, err
	}

	return flowAction, nil
}

func setupSubflow(eCtx ExecContext, subflowName string) models.Subflow {
	parentSubflowId := ""
	parentSubflow := eCtx.FlowScope.Subflow
	if parentSubflow != nil {
		parentSubflowId = parentSubflow.Id
	}

	subflow := models.Subflow{
		WorkspaceId:     eCtx.WorkspaceId,
		Name:            subflowName,
		ParentSubflowId: parentSubflowId,
		Status:          models.SubflowStatusStarted,
	}

	if subflow.Id == "" {
		subflow.Id = "sf_" + ksuid.New().String()
	}
	if subflow.FlowId == "" {
		subflow.FlowId = workflow.GetInfo(eCtx).WorkflowExecution.ID
	}

	return subflow
}

func putSubflow(eCtx ExecContext, subflow models.Subflow) (models.Subflow, error) {
	var fa *FlowActivities // nil struct pointer for struct-based activities
	err := workflow.ExecuteActivity(eCtx, fa.PersistSubflow, subflow).Get(eCtx, nil)
	if err != nil {
		return models.Subflow{}, err
	}
	return subflow, nil
}
