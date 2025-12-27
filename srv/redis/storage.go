package redis

import (
	"context"
	"encoding/json"
	"fmt"
	"sidekick/utils"
	"sort"

	"github.com/kelindar/binary"
	"github.com/redis/go-redis/v9"
)

type Storage struct {
	Client *redis.Client
}

func NewStorage() *Storage {
	return &Storage{Client: setupClient()}
}

func (s Storage) CheckConnection(ctx context.Context) error {
	_, err := s.Client.Ping(context.Background()).Result()
	return err
}

func (s Storage) MGet(ctx context.Context, workspaceId string, keys []string) ([][]byte, error) {
	prefixedKeys := make([]string, len(keys))
	for i, key := range keys {
		prefixedKeys[i] = fmt.Sprintf("%s:%s", workspaceId, key)
	}
	values, err := s.Client.MGet(ctx, prefixedKeys...).Result()
	if err != nil {
		return nil, err
	}
	byteValues := utils.Map(values, func(value interface{}) []byte {
		if value == nil {
			return nil
		}
		return []byte(value.(string))
	})
	return byteValues, nil
}

func (s Storage) MSet(ctx context.Context, workspaceId string, values map[string]interface{}) error {
	prefixedValues := make(map[string]interface{})
	for key, value := range values {
		bytes, err := binary.Marshal(value)
		if err != nil {
			return fmt.Errorf("redis mset failed to marshal value: %w", err)
		}
		prefixedValues[fmt.Sprintf("%s:%s", workspaceId, key)] = bytes
	}
	return s.Client.MSet(ctx, prefixedValues).Err()
}

func (s Storage) MSetRaw(ctx context.Context, workspaceId string, values map[string][]byte) error {
	prefixedValues := make(map[string]interface{})
	for key, value := range values {
		prefixedValues[fmt.Sprintf("%s:%s", workspaceId, key)] = value
	}
	return s.Client.MSet(ctx, prefixedValues).Err()
}

func (s Storage) DeletePrefix(ctx context.Context, workspaceId string, prefix string) error {
	pattern := fmt.Sprintf("%s:%s*", workspaceId, prefix)
	var cursor uint64
	for {
		keys, nextCursor, err := s.Client.Scan(ctx, cursor, pattern, 100).Result()
		if err != nil {
			return fmt.Errorf("redis scan failed: %w", err)
		}

		if len(keys) > 0 {
			pipe := s.Client.Pipeline()
			for _, key := range keys {
				pipe.Del(ctx, key)
			}
			_, err = pipe.Exec(ctx)
			if err != nil {
				return fmt.Errorf("redis pipeline del failed: %w", err)
			}
		}

		cursor = nextCursor
		if cursor == 0 {
			break
		}
	}
	return nil
}

func (s Storage) GetKeysWithPrefix(ctx context.Context, workspaceId string, prefix string) ([]string, error) {
	pattern := fmt.Sprintf("%s:%s*", workspaceId, prefix)
	var allKeys []string
	var cursor uint64
	for {
		keys, nextCursor, err := s.Client.Scan(ctx, cursor, pattern, 100).Result()
		if err != nil {
			return nil, fmt.Errorf("redis scan failed: %w", err)
		}
		// Strip workspace prefix from keys
		for _, key := range keys {
			stripped := key[len(workspaceId)+1:] // Remove "workspaceId:" prefix
			allKeys = append(allKeys, stripped)
		}
		cursor = nextCursor
		if cursor == 0 {
			break
		}
	}
	sort.Strings(allKeys)
	return allKeys, nil
}

func toMap(something interface{}) (map[string]interface{}, error) {
	// Convert the thing to JSON
	jsonData, err := json.Marshal(something)
	if err != nil {
		return nil, fmt.Errorf("failed to convert something to JSON: %w", err)
	}

	// Convert the JSON data to a map
	var dataMap map[string]interface{}
	err = json.Unmarshal(jsonData, &dataMap)
	if err != nil {
		return nil, fmt.Errorf("failed to convert JSON to map: %w", err)
	}

	return dataMap, nil
}
