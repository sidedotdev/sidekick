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
	eventParentId1 := "TEST_PARENT_ID_1"
	eventParentId2 := "TEST_PARENT_ID_2"

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	eventParentIdCh := make(chan string, 2)
	eventCh, errCh := db.StreamFlowEvents(ctx, workspaceId, flowId, "0", eventParentIdCh)

	// Send event parent IDs
	eventParentIdCh <- eventParentId1
	eventParentIdCh <- eventParentId2

	// Add flow events for both parent IDs
	flowEvent1 := domain.ProgressTextEvent{
		EventType: domain.ProgressTextEventType,
		ParentId:  eventParentId1,
		Text:      "Test Flow Event 1",
	}
	flowEvent2 := domain.ProgressTextEvent{
		EventType: domain.ProgressTextEventType,
		ParentId:  eventParentId2,
		Text:      "Test Flow Event 2",
	}
	endEvent1 := domain.EndStreamEvent{
		EventType: domain.EndStreamEventType,
		ParentId:  eventParentId1,
	}
	endEvent2 := domain.EndStreamEvent{
		EventType: domain.EndStreamEventType,
		ParentId:  eventParentId2,
	}

	err := db.AddFlowEvent(ctx, workspaceId, flowId, flowEvent1)
	assert.Nil(t, err)
	err = db.AddFlowEvent(ctx, workspaceId, flowId, flowEvent2)
	assert.Nil(t, err)
	err = db.AddFlowEvent(ctx, workspaceId, flowId, endEvent1)
	assert.Nil(t, err)
	err = db.AddFlowEvent(ctx, workspaceId, flowId, endEvent2)
	assert.Nil(t, err)

	// Check if we receive the events
	receivedEvents := make([]domain.FlowEvent, 0)
	for i := 0; i < 4; i++ {
		select {
		case event := <-eventCh:
			receivedEvents = append(receivedEvents, event)
		case err := <-errCh:
			assert.Fail(t, "Received error instead of event", err)
		case <-time.After(time.Second * 5):
			assert.Fail(t, "Timed out waiting for event")
		}
	}

	// Verify received events
	assert.Equal(t, 4, len(receivedEvents))
	assert.Contains(t, receivedEvents, flowEvent1)
	assert.Contains(t, receivedEvents, flowEvent2)
	assert.Contains(t, receivedEvents, endEvent1)
	assert.Contains(t, receivedEvents, endEvent2)

	// Test cancellation
	cancel()
	_, ok := <-eventCh
	assert.False(t, ok, "Event channel should be closed after cancellation")
}
