package redis

import (
	"context"
	"sidekick/srv"

	"log"

	"github.com/redis/go-redis/v9"
)

func NewTestRedisService() (*srv.Delegator, *redis.Client) {
	storage := newTestRedisStorage()
	streamer := NewTestRedisStreamer()
	streamer.Client = storage.Client
	return srv.NewDelegator(storage, streamer), storage.Client
}

func newTestRedisStorage() *Storage {
	db := &Storage{Client: NewTestRedisClient()}

	// Flush the database synchronously to ensure a clean state for each test
	_, err := db.Client.FlushDB(context.Background()).Result()
	if err != nil {
		log.Panicf("failed to flush redis database: %v", err)
	}

	return db
}

func NewTestRedisStreamer() *Streamer {
	streamer := &Streamer{Client: NewTestRedisClient()}

	// Flush the database synchronously to ensure a clean state for each test
	_, err := streamer.Client.FlushDB(context.Background()).Result()
	if err != nil {
		log.Panicf("failed to flush redis database: %v", err)
	}

	return streamer
}

func NewTestRedisClient() *redis.Client {
	return redis.NewClient(&redis.Options{
		Addr:     "localhost:6379",
		Password: "",
		DB:       1,
	})
}
