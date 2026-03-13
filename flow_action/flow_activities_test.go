package flow_action

import (
	"context"
	"testing"

	"sidekick/domain"
	"sidekick/srv"

	"github.com/stretchr/testify/assert"
)

func TestPersistFlowAction(t *testing.T) {
	service := srv.NewTestService(t)
	fa := FlowActivities{
		Service: service,
	}

	flowAction := domain.FlowAction{
		Id:          "test_id",
		FlowId:      "test_flow_id",
		WorkspaceId: "test_workspace_id",
	}

	err := fa.PersistFlowAction(context.Background(), flowAction)
	assert.NoError(t, err)

	persistedFlowAction, err := service.GetFlowAction(context.Background(), flowAction.WorkspaceId, flowAction.Id)
	assert.NoError(t, err)

	assert.Equal(t, flowAction, persistedFlowAction)
}
