package redis

import (
	"context"

	"log"

	"github.com/redis/go-redis/v9"
)

func NewTestRedisDatabase() *Service {
	db := &Service{}
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
