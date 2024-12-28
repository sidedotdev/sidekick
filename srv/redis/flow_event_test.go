package redis

import (
	"context"
	"encoding/json"
	"fmt"
	"sidekick/domain"
	"sidekick/llm"
	"testing"

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
