package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sidekick/common"
	"sidekick/dev"
	"sidekick/domain"
	"sidekick/mocks"
	"testing"

	"github.com/segmentio/ksuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
)

// updateOutcomeHandle mimics a completed Temporal update handle: Get reports
// the handler's failure, or decodes its result through the default data
// converter the way the real client does.
type updateOutcomeHandle struct {
	result interface{}
	err    error
}

var _ client.WorkflowUpdateHandle = updateOutcomeHandle{}

func (h updateOutcomeHandle) RunID() string      { return "update-outcome-run-id" }
func (h updateOutcomeHandle) WorkflowID() string { return "update-outcome-workflow-id" }
func (h updateOutcomeHandle) UpdateID() string   { return "update-outcome-update-id" }
func (h updateOutcomeHandle) Get(_ context.Context, valuePtr interface{}) error {
	if h.err != nil || valuePtr == nil {
		return h.err
	}
	dc := converter.GetDefaultDataConverter()
	payload, err := dc.ToPayload(h.result)
	if err != nil {
		return err
	}
	return dc.FromPayload(payload, valuePtr)
}

func newPortForwardsTestController(t *testing.T) (Controller, *mocks.Client, string, string) {
	t.Helper()
	ctrl := NewMockController(t)
	mockTemporalClient := ctrl.temporalClient.(*mocks.Client)
	mockTemporalClient.On("UpdateWorkflow", mock.Anything, mock.Anything).Unset()

	workspaceId := "ws-port-forwards-" + ksuid.New().String()
	flowId := "flow-port-forwards-" + ksuid.New().String()
	require.NoError(t, ctrl.service.PersistWorkspace(context.Background(), domain.Workspace{Id: workspaceId}))
	require.NoError(t, ctrl.service.PersistFlow(context.Background(), domain.Flow{Id: flowId, WorkspaceId: workspaceId}))
	return ctrl, mockTemporalClient, workspaceId, flowId
}

func putPortForwards(t *testing.T, ctrl Controller, workspaceId, flowId, payload string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(
		http.MethodPut,
		fmt.Sprintf("/api/v1/workspaces/%s/flows/%s/port_forwards", workspaceId, flowId),
		bytes.NewReader([]byte(payload)),
	)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	DefineRoutes(ctrl, TestAllowedOrigins()).ServeHTTP(rr, req)
	return rr
}

func TestUpdateFlowPortForwardsHandler_ReturnsEffectiveMappings(t *testing.T) {
	t.Parallel()

	ctrl, mockTemporalClient, workspaceId, flowId := newPortForwardsTestController(t)

	requested := []common.PortForwardConfig{
		{HostPort: 3000},
		{HostPort: 5432, ContainerPort: 15432},
	}
	// The workflow reports what actually took effect, including defaulted
	// container ports, which is what the response must carry back.
	effective := []common.PortForwardConfig{
		{HostPort: 3000, ContainerPort: 3000},
		{HostPort: 5432, ContainerPort: 15432},
	}
	mockTemporalClient.On(
		"UpdateWorkflow",
		mock.Anything,
		mock.MatchedBy(func(options client.UpdateWorkflowOptions) bool {
			return options.WorkflowID == flowId &&
				options.RunID == "" &&
				options.UpdateName == dev.UpdateNamePortForwards &&
				options.WaitForStage == client.WorkflowUpdateStageCompleted &&
				len(options.Args) == 1 &&
				assert.ObjectsAreEqual(dev.PortForwardsConfig{PortForwards: requested}, options.Args[0])
		}),
	).Return(updateOutcomeHandle{result: dev.PortForwardsConfig{PortForwards: effective}}, nil).Once()

	payload, err := json.Marshal(PortForwardsUpdateRequest{PortForwards: requested})
	require.NoError(t, err)
	rr := putPortForwards(t, ctrl, workspaceId, flowId, string(payload))

	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	assert.JSONEq(t, `{"portForwards":[{"hostPort":3000,"containerPort":3000},{"hostPort":5432,"containerPort":15432}]}`, rr.Body.String())
	mockTemporalClient.AssertExpectations(t)
}

func TestUpdateFlowPortForwardsHandler_ClearsMappings(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		payload string
	}{
		{name: "empty list", payload: `{"portForwards":[]}`},
		{name: "omitted list", payload: `{}`},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			ctrl, mockTemporalClient, workspaceId, flowId := newPortForwardsTestController(t)
			mockTemporalClient.On(
				"UpdateWorkflow",
				mock.Anything,
				mock.MatchedBy(func(options client.UpdateWorkflowOptions) bool {
					if options.UpdateName != dev.UpdateNamePortForwards || len(options.Args) != 1 {
						return false
					}
					config, ok := options.Args[0].(dev.PortForwardsConfig)
					return ok && len(config.PortForwards) == 0
				}),
			).Return(updateOutcomeHandle{result: dev.PortForwardsConfig{}}, nil).Once()

			rr := putPortForwards(t, ctrl, workspaceId, flowId, test.payload)

			require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
			assert.JSONEq(t, `{"portForwards":[]}`, rr.Body.String())
			mockTemporalClient.AssertExpectations(t)
		})
	}
}

func TestUpdateFlowPortForwardsHandler_RejectsInvalidRequestsBeforeUpdatingWorkflow(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		payload string
		error   string
	}{
		{
			name:    "malformed json",
			payload: `{"portForwards":[`,
			error:   "Invalid request payload",
		},
		{
			name:    "host port out of range",
			payload: `{"portForwards":[{"hostPort":70000}]}`,
			error:   "host_port must be between 1 and 65535",
		},
		{
			name:    "container port out of range",
			payload: `{"portForwards":[{"hostPort":3000,"containerPort":-1}]}`,
			error:   "container_port must be between 1 and 65535",
		},
		{
			name:    "duplicate effective container ports",
			payload: `{"portForwards":[{"hostPort":3000},{"hostPort":4000,"containerPort":3000}]}`,
			error:   "container_port 3000 is used by both host_port 3000 and host_port 4000",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			ctrl, mockTemporalClient, workspaceId, flowId := newPortForwardsTestController(t)

			rr := putPortForwards(t, ctrl, workspaceId, flowId, test.payload)

			require.Equal(t, http.StatusBadRequest, rr.Code, rr.Body.String())
			var response map[string]string
			require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &response))
			assert.Contains(t, response["error"], test.error)
			mockTemporalClient.AssertNotCalled(t, "UpdateWorkflow", mock.Anything, mock.Anything)
		})
	}
}

func TestUpdateFlowPortForwardsHandler_FlowNotFound(t *testing.T) {
	t.Parallel()

	ctrl, mockTemporalClient, workspaceId, _ := newPortForwardsTestController(t)

	rr := putPortForwards(t, ctrl, workspaceId, "flow-missing-"+ksuid.New().String(), `{"portForwards":[{"hostPort":3000}]}`)

	require.Equal(t, http.StatusNotFound, rr.Code)
	assert.JSONEq(t, `{"error":"Flow not found"}`, rr.Body.String())
	mockTemporalClient.AssertNotCalled(t, "UpdateWorkflow", mock.Anything, mock.Anything)
}

func TestUpdateFlowPortForwardsHandler_WorkflowMissingInTemporal(t *testing.T) {
	t.Parallel()

	ctrl, mockTemporalClient, workspaceId, flowId := newPortForwardsTestController(t)
	mockTemporalClient.On("UpdateWorkflow", mock.Anything, mock.Anything).
		Return(nil, serviceerror.NewNotFound("workflow execution not found")).Once()

	rr := putPortForwards(t, ctrl, workspaceId, flowId, `{"portForwards":[{"hostPort":3000}]}`)

	require.Equal(t, http.StatusNotFound, rr.Code)
	assert.JSONEq(t, fmt.Sprintf(`{"error":"Flow with ID %s not found"}`, flowId), rr.Body.String())
	mockTemporalClient.AssertExpectations(t)
}

func TestUpdateFlowPortForwardsHandler_SurfacesUpdateOutcomeFailure(t *testing.T) {
	t.Parallel()

	ctrl, mockTemporalClient, workspaceId, flowId := newPortForwardsTestController(t)
	// The update request itself succeeds; the failure only shows up when the
	// handle's outcome is inspected.
	mockTemporalClient.On("UpdateWorkflow", mock.Anything, mock.Anything).
		Return(updateOutcomeHandle{err: errors.New("failed to bind container port 3000")}, nil).Once()

	rr := putPortForwards(t, ctrl, workspaceId, flowId, `{"portForwards":[{"hostPort":3000}]}`)

	require.Equal(t, http.StatusInternalServerError, rr.Code)
	var response map[string]string
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &response))
	assert.Contains(t, response["error"], "Failed to update port forwards")
	assert.Contains(t, response["error"], "failed to bind container port 3000")
	mockTemporalClient.AssertExpectations(t)
}

func TestUpdateFlowModalConfigHandler_SurfacesUpdateOutcomeFailure(t *testing.T) {
	t.Parallel()

	ctrl, mockTemporalClient, workspaceId, flowId := newPortForwardsTestController(t)
	mockTemporalClient.On("UpdateWorkflow", mock.Anything, mock.Anything).
		Return(updateOutcomeHandle{err: errors.New("sandbox recreation failed")}, nil).Once()

	req := httptest.NewRequest(
		http.MethodPut,
		fmt.Sprintf("/api/v1/workspaces/%s/flows/%s/modal_config", workspaceId, flowId),
		bytes.NewReader([]byte(`{"config":{"memory":2048}}`)),
	)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	DefineRoutes(ctrl, TestAllowedOrigins()).ServeHTTP(rr, req)

	require.Equal(t, http.StatusInternalServerError, rr.Code)
	var response map[string]string
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &response))
	assert.Contains(t, response["error"], "Failed to update workflow Modal configuration")
	assert.Contains(t, response["error"], "sandbox recreation failed")
	mockTemporalClient.AssertExpectations(t)
}
