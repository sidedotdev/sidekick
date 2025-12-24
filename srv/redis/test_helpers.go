package redis

import (
	"context"
	"sidekick/srv"
	"testing"

	"github.com/redis/go-redis/v9"
)

func newTestRedisService(t *testing.T) *srv.Delegator {
	t.Helper()
	storage := newTestRedisStorage(t)
	streamer := newTestRedisStreamer(t)
	return srv.NewDelegator(storage, streamer)
}

func newTestRedisStorage(t *testing.T) *Storage {
	t.Helper()
	db := &Storage{Client: newTestRedisClient()}

	_, err := db.Client.FlushDB(context.Background()).Result()
	if err != nil {
		t.Skipf("Skipping test; Redis not available: %v", err)
	}

	return db
}

func newTestRedisStreamer(t *testing.T) *Streamer {
	t.Helper()
	streamer := &Streamer{Client: newTestRedisClient()}

	_, err := streamer.Client.FlushDB(context.Background()).Result()
	if err != nil {
		t.Skipf("Skipping test; Redis not available: %v", err)
	}

	return streamer
}

func newTestRedisClient() *redis.Client {
	return redis.NewClient(&redis.Options{
		Addr:     "localhost:6379",
		Password: "",
		DB:       1,
	})
}
