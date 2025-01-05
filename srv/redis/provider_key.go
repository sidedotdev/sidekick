package redis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sidekick/domain"
	"sidekick/srv"
	"strings"

	"github.com/redis/go-redis/v9"
)

func (s Storage) PersistProviderKey(ctx context.Context, key domain.ProviderKey) error {
	keyJson, err := json.Marshal(key)
	if err != nil {
		log.Println("Failed to convert provider key to JSON: ", err)
		return err
	}
	redisKey := fmt.Sprintf("provider_key:%s", key.Id)
	err = s.Client.Set(ctx, redisKey, keyJson, 0).Err()
	if err != nil {
		log.Println("Failed to persist provider key to Redis: ", err)
		return err
	}

	// Add provider key to sorted set by nickname/id
	sortKey := key.Id
	if key.Nickname != nil {
		sortKey = *key.Nickname + ":" + key.Id
	}
	err = s.Client.ZAdd(ctx, "global:provider_keys", redis.Z{
		Score:  0, // score is not used, we rely on the lexographical ordering of the member
		Member: sortKey,
	}).Err()
	if err != nil {
		log.Println("Failed to add provider key to sorted set: ", err)
		return err
	}

	return nil
}

func (s Storage) GetProviderKey(ctx context.Context, keyId string) (domain.ProviderKey, error) {
	redisKey := fmt.Sprintf("provider_key:%s", keyId)
	keyJson, err := s.Client.Get(ctx, redisKey).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return domain.ProviderKey{}, srv.ErrNotFound
		}
		return domain.ProviderKey{}, fmt.Errorf("failed to get provider key from Redis: %w", err)
	}
	var key domain.ProviderKey
	err = json.Unmarshal([]byte(keyJson), &key)
	if err != nil {
		return domain.ProviderKey{}, fmt.Errorf("failed to unmarshal provider key JSON: %w", err)
	}
	return key, nil
}

func (s Storage) GetAllProviderKeys(ctx context.Context) ([]domain.ProviderKey, error) {
	var keys []domain.ProviderKey

	// Retrieve all provider key IDs from the Redis sorted set
	keyIds, err := s.Client.ZRange(ctx, "global:provider_keys", 0, -1).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to get provider key IDs from Redis sorted set: %w", err)
	}

	for _, sortKey := range keyIds {
		// Extract the ID from the sort key (either "id" or "nickname:id")
		id := sortKey
		if strings.Contains(sortKey, ":") {
			id = sortKey[strings.LastIndex(sortKey, ":")+1:]
		}

		// Fetch provider key details using the ID
		keyJson, err := s.Client.Get(ctx, fmt.Sprintf("provider_key:%s", id)).Result()
		if err != nil {
			return nil, fmt.Errorf("failed to get provider key data for ID %s: %w", id, err)
		}
		var key domain.ProviderKey
		err = json.Unmarshal([]byte(keyJson), &key)
		if err != nil {
			return nil, fmt.Errorf("failed to unmarshal provider key JSON: %w", err)
		}
		keys = append(keys, key)
	}

	return keys, nil
}

func (s Storage) DeleteProviderKey(ctx context.Context, keyId string) error {
	// First get the key to get its nickname - ignore if not found
	key, err := s.GetProviderKey(ctx, keyId)
	if err != nil {
		if errors.Is(err, srv.ErrNotFound) {
			return nil // Provider key already deleted - that's fine
		}
		return fmt.Errorf("failed to get provider key before deletion: %w", err)
	}

	// Remove from sorted set
	sortKey := keyId
	if key.Nickname != nil {
		sortKey = *key.Nickname + ":" + keyId
	}
	err = s.Client.ZRem(ctx, "global:provider_keys", sortKey).Err()
	if err != nil {
		return fmt.Errorf("failed to remove provider key from sorted set: %w", err)
	}

	// Delete the provider key record
	redisKey := fmt.Sprintf("provider_key:%s", keyId)
	err = s.Client.Del(ctx, redisKey).Err()
	if err != nil {
		return fmt.Errorf("failed to delete provider key record: %w", err)
	}

	return nil
}
