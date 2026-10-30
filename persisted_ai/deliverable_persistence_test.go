package persisted_ai

import (
	"context"
	"encoding/json"
	"testing"

	"sidekick/llm2"
	"sidekick/srv/sqlite"

	"github.com/stretchr/testify/require"
)

func TestDeliverableMetadataPersistsAndHydrates(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	storage := sqlite.NewTestSqliteStorage(t, "deliverable-persistence")
	history := NewLlm2ChatHistory("flow", "workspace")
	descriptors := []DeliverableMetadata{
		{ID: "dev_requirements", Operation: "record", Snapshot: true},
		{ID: "dev_requirements", Outcome: "success"},
		{ID: "dev_plan", Operation: "update"},
		{ID: "dev_plan", Outcome: "success", Snapshot: true},
	}
	for i, descriptor := range descriptors {
		role := llm2.RoleAssistant
		if i%2 == 1 {
			role = llm2.RoleUser
		}
		message := textMsg(role, "complete deliverable content")
		SetDeliverable(&message.Content[0], descriptor)
		history.Append(&message)
	}
	require.NoError(t, history.Persist(ctx, storage, NewKsuidGenerator()))
	encoded, err := json.Marshal(&ChatHistoryContainer{History: history})
	require.NoError(t, err)
	var restored ChatHistoryContainer
	require.NoError(t, json.Unmarshal(encoded, &restored))

	activities := &ChatHistoryActivities{Storage: storage}
	output, err := activities.ManageV4(ctx, ManageInput{
		ChatHistory: &restored, WorkspaceId: "workspace", RequestedKeepLength: 100000,
	})
	require.NoError(t, err)
	managed := output.ChatHistory.History.(*Llm2ChatHistory)
	fresh := NewLlm2ChatHistory("flow", "workspace")
	fresh.SetRefs(managed.Refs())
	require.NoError(t, fresh.Hydrate(ctx, storage))
	messages := fresh.Llm2Messages()
	require.Len(t, messages, len(descriptors))
	for i, message := range messages {
		block := message.Content[0]
		SetContextType(&block, ContextTypeDeliverable)
		require.Equal(t, &descriptors[i], GetDeliverable(block))
		require.Equal(t, "complete deliverable content", block.Text)
	}
}
