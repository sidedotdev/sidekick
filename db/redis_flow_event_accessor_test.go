package db

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sidekick/flow_event"
	"sidekick/llm"
	"testing"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
)

func newTestRedisFlowEventAccessor() *RedisFlowEventAccessor {
	db := &RedisFlowEventAccessor{}
	db.Client = redis.NewClient(&redis.Options{
		Addr:     "localhost:6379",
		Password: "",
		DB:       1,
	})

	// Flush the database synchronously to ensure a clean state for each test
	_, err := db.Client.FlushDB(context.Background()).Result()
	if err != nil {
		log.Panicf("failed to flush redis database: %v", err)
	}

	return db
}

func TestAddChatMessageDeltaFlowEvent(t *testing.T) {
	db := newTestRedisFlowEventAccessor()
	workspaceId := "TEST_WORKSPACE_ID"
	flowId := "TEST_FLOW_ID"
	flowEvent := flow_event.ChatMessageDelta{
		EventType:    flow_event.ChatMessageDeltaEventType,
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
	var streamedEvent flow_event.ChatMessageDelta
	err = json.Unmarshal([]byte(jsonEvent), &streamedEvent)
	assert.Nil(t, err)
	assert.Equal(t, flowEvent, streamedEvent)
}

func TestAddProgressTextFlowEvent(t *testing.T) {
	db := newTestRedisFlowEventAccessor()
	workspaceId := "TEST_WORKSPACE_ID"
	flowId := "TEST_FLOW_ID"
	flowEvent := flow_event.ProgressText{
		EventType: flow_event.ProgressTextEventType,
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
	var streamedEvent flow_event.ProgressText
	err = json.Unmarshal([]byte(jsonEvent), &streamedEvent)
	assert.Nil(t, err)
	assert.Equal(t, flowEvent, streamedEvent)
}
