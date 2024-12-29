package redis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sidekick/domain"
	"sync"
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
		// default to starting from the start of the stream for flow events
		if lastId == "" {
			lastId = "0"
		}
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

func lenSyncMap(m *sync.Map) int {
	var i int
	m.Range(func(_, _ any) bool {
		i++
		return true
	})
	return i
}

func (db *Streamer) StreamFlowEvents(ctx context.Context, workspaceId, flowId string, streamMessageStartId string, eventParentIdCh <-chan string) (<-chan domain.FlowEvent, <-chan error) {
	eventCh := make(chan domain.FlowEvent)
	errCh := make(chan error, 1)

	// default to starting from the start of the stream for flow events
	if streamMessageStartId == "" {
		streamMessageStartId = "0"
	}

	go func() {
		defer close(eventCh)
		defer close(errCh)

		streamKeys := sync.Map{}
		for {
			select {
			case <-ctx.Done():
				return
			case eventParentId, ok := <-eventParentIdCh:
				if !ok {
					return
				}
				streamKey := fmt.Sprintf("%s:%s:stream:%s", workspaceId, flowId, eventParentId)
				streamKeys.Store(streamKey, streamMessageStartId)
			default:
				if lenSyncMap(&streamKeys) == 0 {
					// spin until we have at least one stream key
					time.Sleep(100 * time.Millisecond)
					continue
				}

				// Convert sync.Map to a regular map for `GetFlowEvents`
				keysMap := make(map[string]string)
				streamKeys.Range(func(key, value interface{}) bool {
					keysMap[key.(string)] = value.(string)
					return true
				})

				// wait until we have at least one stream key to fetch
				if len(keysMap) == 0 {
					time.Sleep(time.Millisecond * 20)
					continue
				}

				blockDuration := 250 * time.Millisecond
				events, updatedStreamKeys, err := db.GetFlowEvents(ctx, workspaceId, keysMap, 100, blockDuration)
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
							streamKeys.Delete(fmt.Sprintf("%s:%s:stream:%s", workspaceId, flowId, event.GetParentId()))
						}
					}
				}

				// Update the stream keys for subsequent fetches
				for key, lastId := range updatedStreamKeys {
					streamKeys.Store(key, lastId)
				}

			}
		}
	}()

	return eventCh, errCh
}
