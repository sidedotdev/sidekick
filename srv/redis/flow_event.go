package redis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sidekick/domain"
	"time"

	"github.com/redis/go-redis/v9"
)

func (db *Streamer) AddFlowEvent(ctx context.Context, workspaceId string, flowId string, flowEvent domain.FlowEvent) error {
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

func (db *Streamer) EndFlowEventStream(ctx context.Context, workspaceId, flowId, eventStreamParentId string) error {
	streamKey := fmt.Sprintf("%s:%s:stream:%s", workspaceId, flowId, eventStreamParentId)
	serializedEvent, err := json.Marshal(domain.EndStreamEvent{
		EventType: domain.EndStreamEventType,
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

func (db *Streamer) GetFlowEvents(ctx context.Context, workspaceId string, streamKeys map[string]string, maxCount int64, blockDuration time.Duration) ([]domain.FlowEvent, map[string]string, error) {
	updatedStreamKeys := make(map[string]string)
	var events []domain.FlowEvent

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
				event, err := domain.UnmarshalFlowEvent([]byte(jsonEvent.(string)))
				if err != nil {
					return nil, nil, fmt.Errorf("failed to deserialize flow event: %v", err)
				}
				events = append(events, event)
			}
		}
	}
	return events, updatedStreamKeys, nil
}

func (db *Streamer) StreamFlowEvents(ctx context.Context, workspaceId, flowId string, streamMessageStartId string, eventParentIdCh <-chan string) (<-chan domain.FlowEvent, <-chan error) {
	eventCh := make(chan domain.FlowEvent)
	errCh := make(chan error, 1)

	go func() {
		defer close(eventCh)
		defer close(errCh)

		streamKeys := make(map[string]string)
		for {
			select {
			case <-ctx.Done():
				return
			case eventParentId, ok := <-eventParentIdCh:
				if !ok {
					return
				}
				streamKey := fmt.Sprintf("%s:%s:stream:%s", workspaceId, flowId, eventParentId)
				streamKeys[streamKey] = streamMessageStartId
			default:
			}

			events, updatedStreamKeys, err := db.GetFlowEvents(ctx, workspaceId, streamKeys, 100, time.Second*1)
			if err != nil {
				errCh <- err
				return
			}

			for _, event := range events {
				select {
				case <-ctx.Done():
					return
				case eventCh <- event:
					if _, ok := event.(domain.EndStreamEvent); ok {
						delete(streamKeys, fmt.Sprintf("%s:%s:stream:%s", workspaceId, flowId, event.GetParentId()))
					}
				}
			}

			streamKeys = updatedStreamKeys
		}
	}()

	return eventCh, errCh
}
