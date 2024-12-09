package db

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sidekick/flow_event"
	"time"

	"github.com/redis/go-redis/v9"
)

type RedisFlowEventAccessor struct {
	Client *redis.Client
}

var _ FlowEventAccessor = &RedisFlowEventAccessor{}

func (db *RedisFlowEventAccessor) AddFlowEvent(ctx context.Context, workspaceId string, flowId string, flowEvent flow_event.FlowEvent) error {
	streamKey := fmt.Sprintf("%s:%s:stream:%s", workspaceId, flowId, flowEvent.GetParentId())

	// we explicitly serialize since we have a deeply nested struct (chat message delta) that redis doesn't auto-serialize
	serializedEvent, err := json.Marshal(flowEvent)
	if err != nil {
		return fmt.Errorf("failed to serialize flow event: %v", err)
	}

	db.Client.TTL(ctx, streamKey).SetVal(time.Hour * 24)
	err = db.Client.XAdd(ctx, &redis.XAddArgs{
		Stream: streamKey,
		Values: map[string]interface{}{"event": serializedEvent},
	}).Err()
	if err != nil {
		return fmt.Errorf("failed to add flow event to stream: %v", err)
	}

	return nil
}

func (db *RedisFlowEventAccessor) EndFlowEventStream(ctx context.Context, workspaceId, flowId, eventStreamParentId string) error {
	streamKey := fmt.Sprintf("%s:%s:stream:%s", workspaceId, flowId, eventStreamParentId)
	serializedEvent, err := json.Marshal(flow_event.EndStream{
		EventType: flow_event.EndStreamEventType,
		ParentId:  eventStreamParentId,
	})
	if err != nil {
		return fmt.Errorf("failed to serialize flow event: %v", err)
	}

	db.Client.TTL(ctx, streamKey).SetVal(time.Hour * 24)
	err = db.Client.XAdd(ctx, &redis.XAddArgs{
		Stream: streamKey,
		Values: map[string]interface{}{"event": serializedEvent},
	}).Err()

	if err != nil {
		return fmt.Errorf("failed to write end message to Redis stream: %v", err)
	}

	return nil
}

func (db *RedisFlowEventAccessor) GetFlowEvents(ctx context.Context, workspaceId string, streamKeys map[string]string, maxCount int64, blockDuration time.Duration) ([]flow_event.FlowEvent, map[string]string, error) {
	updatedStreamKeys := make(map[string]string)
	var events []flow_event.FlowEvent

	var streamArgs []string
	var lastIds []string
	for key, lastId := range streamKeys {
		streamArgs = append(streamArgs, key)
		lastIds = append(lastIds, lastId)
		updatedStreamKeys[key] = lastId
	}
	streamArgs = append(streamArgs, lastIds...)
	if maxCount == 0 {
		maxCount = 100
	}

	streams, err := db.Client.XRead(ctx, &redis.XReadArgs{
		Streams: streamArgs,
		Count:   maxCount,
		Block:   blockDuration,
	}).Result()

	if err != nil {
		if errors.Is(err, redis.Nil) {
			return events, updatedStreamKeys, nil
		}
		return nil, nil, fmt.Errorf("failed to read from Redis streams: %v", err)
	}

	// Process each stream's messages
	for _, stream := range streams {
		if len(stream.Messages) > 0 {
			lastMessage := stream.Messages[len(stream.Messages)-1]
			updatedStreamKeys[stream.Stream] = lastMessage.ID
			for _, message := range stream.Messages {
				jsonEvent := message.Values["event"]
				event, err := flow_event.UnmarshalFlowEvent([]byte(jsonEvent.(string)))
				if err != nil {
					return nil, nil, fmt.Errorf("failed to deserialize flow event: %v", err)
				}
				events = append(events, event)
			}
		}
	}
	return events, updatedStreamKeys, nil
}
