package flow_action

import (
	"context"
	"encoding/json"
	"testing"

	"sidekick/domain"
	"sidekick/srv/redis"

	"github.com/stretchr/testify/assert"
)

func TestPersistFlowAction(t *testing.T) {
	service, client := redis.NewTestRedisService()
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

	key := "test_workspace_id:test_id"
	val, err := client.Get(context.Background(), key).Result()
	assert.NoError(t, err)

	var persistedFlowAction domain.FlowAction
	err = json.Unmarshal([]byte(val), &persistedFlowAction)
	assert.NoError(t, err)

	assert.Equal(t, flowAction, persistedFlowAction)
}
