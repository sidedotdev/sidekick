package redis

import (
	"context"
	"sidekick/srv"

	"log"

	"github.com/redis/go-redis/v9"
)

func newTestRedisService() (*srv.Delegator, *redis.Client) {
	storage := newTestRedisStorage()
	streamer := newTestRedisStreamer()
	streamer.Client = storage.Client
	return srv.NewDelegator(storage, streamer), storage.Client
}

func newTestRedisStorage() *Storage {
	db := &Storage{Client: newTestRedisClient()}

	// Flush the database synchronously to ensure a clean state for each test
	_, err := db.Client.FlushDB(context.Background()).Result()
	if err != nil {
		log.Panicf("failed to flush redis database: %v", err)
	}

	return db
}

func newTestRedisStreamer() *Streamer {
	streamer := &Streamer{Client: newTestRedisClient()}

	// Flush the database synchronously to ensure a clean state for each test
	_, err := streamer.Client.FlushDB(context.Background()).Result()
	if err != nil {
		log.Panicf("failed to flush redis database: %v", err)
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
