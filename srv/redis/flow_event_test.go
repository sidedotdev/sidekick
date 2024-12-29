package redis

import (
	"context"
	"encoding/json"
	"fmt"
	"sidekick/domain"
	"sidekick/llm"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestAddChatMessageDeltaFlowEvent(t *testing.T) {
	db := NewTestRedisStreamer()
	workspaceId := "TEST_WORKSPACE_ID"
	flowId := "TEST_FLOW_ID"
	flowEvent := domain.ChatMessageDeltaEvent{
		EventType:    domain.ChatMessageDeltaEventType,
		FlowActionId: "parentId",
		ChatMessageDelta: llm.ChatMessageDelta{
			Role:    llm.ChatMessageRole("User"),
			Content: "This is a test content",
			ToolCalls: []llm.ToolCall{
				{
					Id:        "1",
					Name:      "TestTool",
					Arguments: "TestInput",
				},
			},
			Usage: llm.Usage{
				InputTokens:  1,
				OutputTokens: 2,
			},
		},
	}

	err := db.AddFlowEvent(context.Background(), workspaceId, flowId, flowEvent)
	streamKey := fmt.Sprintf("%s:%s:stream:%s", workspaceId, flowId, flowEvent.GetParentId())
	assert.Nil(t, err)

	// Check that the event was added to the stream
	streams, err := db.Client.XRange(context.Background(), streamKey, "-", "+").Result()
	assert.Nil(t, err)
	assert.NotNil(t, streams)
	assert.NotEmpty(t, streams)

	// Verify the values in the stream
	stream := streams[0] // Assuming the event is the first entry
	jsonEvent := stream.Values["event"].(string)
	var streamedEvent domain.ChatMessageDeltaEvent
	err = json.Unmarshal([]byte(jsonEvent), &streamedEvent)
	assert.Nil(t, err)
	assert.Equal(t, flowEvent, streamedEvent)
}

func TestAddProgressTextFlowEvent(t *testing.T) {
	db := NewTestRedisStreamer()
	workspaceId := "TEST_WORKSPACE_ID"
	flowId := "TEST_FLOW_ID"
	flowEvent := domain.ProgressTextEvent{
		EventType: domain.ProgressTextEventType,
		ParentId:  "parentId",
		Text:      "Test Flow Event",
	}

	err := db.AddFlowEvent(context.Background(), workspaceId, flowId, flowEvent)
	streamKey := fmt.Sprintf("%s:%s:stream:%s", workspaceId, flowId, flowEvent.GetParentId())
	assert.Nil(t, err)

	// Check that the event was added to the stream
	streams, err := db.Client.XRange(context.Background(), streamKey, "-", "+").Result()
	assert.Nil(t, err)
	assert.NotNil(t, streams)
	assert.NotEmpty(t, streams)

	// Verify the values in the stream
	stream := streams[0] // Assuming the event is the first entry
	jsonEvent := stream.Values["event"].(string)
	var streamedEvent domain.ProgressTextEvent
	err = json.Unmarshal([]byte(jsonEvent), &streamedEvent)
	assert.Nil(t, err)
	assert.Equal(t, flowEvent, streamedEvent)
}

func TestStreamFlowEvents(t *testing.T) {
	db := NewTestRedisStreamer()
	workspaceId := "TEST_WORKSPACE_ID"
	flowId := "TEST_FLOW_ID"
	eventParentId := "TEST_PARENT_ID"

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	eventParentIdCh := make(chan string, 1)
	eventCh, errCh := db.StreamFlowEvents(ctx, workspaceId, flowId, "0", eventParentIdCh)

	// Send an event parent ID
	eventParentIdCh <- eventParentId

	// Add a flow event
	flowEvent := domain.ProgressTextEvent{
		EventType: domain.ProgressTextEventType,
		ParentId:  eventParentId,
		Text:      "Test Flow Event",
	}
	err := db.AddFlowEvent(ctx, workspaceId, flowId, flowEvent)
	assert.Nil(t, err)

	// Check if we receive the event
	select {
	case receivedEvent := <-eventCh:
		assert.Equal(t, flowEvent, receivedEvent)
	case err := <-errCh:
		assert.Fail(t, "Received error instead of event", err)
	case <-time.After(time.Second * 5):
		assert.Fail(t, "Timed out waiting for event")
	}

	// Test cancellation
	cancel()
	_, ok := <-eventCh
	assert.False(t, ok, "Event channel should be closed after cancellation")
}
