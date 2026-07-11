package flow_action

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"

	"sidekick/utils"
)

// runReceiveUserResponseWorkflow drives ReceiveUserResponse from a workflow
// context for testing.
func runReceiveUserResponseWorkflow(expectedFlowActionId string) func(ctx workflow.Context) (UserResponse, error) {
	return func(ctx workflow.Context) (UserResponse, error) {
		return ReceiveUserResponse(ctx, expectedFlowActionId)
	}
}

func TestReceiveUserResponse_OnlyReceivesSignalForOwnFlowActionId(t *testing.T) {
	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestWorkflowEnvironment()
	env.SetWorkerOptions(utils.TestWorkerOptions())
	env.SetTestTimeout(10 * time.Second)

	expected := "action-current"
	approved := true

	// Stale signal targeted at a different flow action's signal name; the
	// workflow listens on a different channel and must not consume it.
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(UserResponseSignalName("action-stale"), UserResponse{
			FlowActionId: "action-stale",
			Approved:     &approved,
			Content:      "stale",
		})
	}, time.Millisecond)
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(UserResponseSignalName(expected), UserResponse{
			FlowActionId: expected,
			Approved:     &approved,
			Content:      "matched",
		})
	}, 2*time.Millisecond)

	env.ExecuteWorkflow(runReceiveUserResponseWorkflow(expected))
	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	var result UserResponse
	require.NoError(t, env.GetWorkflowResult(&result))
	assert.Equal(t, expected, result.FlowActionId)
	assert.Equal(t, "matched", result.Content)
}

func TestUserResponseSignalName(t *testing.T) {
	t.Parallel()
	assert.Equal(t, SignalNameUserResponse, UserResponseSignalName(""))
	assert.Equal(t, SignalNameUserResponse+"-abc123", UserResponseSignalName("abc123"))
}

func TestReceiveUserResponse_AcceptsAnyWhenExpectedIsEmpty(t *testing.T) {
	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestWorkflowEnvironment()
	env.SetWorkerOptions(utils.TestWorkerOptions())
	env.SetTestTimeout(10 * time.Second)

	approved := true
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(SignalNameUserResponse, UserResponse{
			FlowActionId: "anything",
			Approved:     &approved,
		})
	}, time.Millisecond)

	env.ExecuteWorkflow(runReceiveUserResponseWorkflow(""))
	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	var result UserResponse
	require.NoError(t, env.GetWorkflowResult(&result))
	assert.Equal(t, "anything", result.FlowActionId)
}
