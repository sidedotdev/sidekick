package redis

import (
	"os"

	"github.com/redis/go-redis/v9"
	zlog "github.com/rs/zerolog/log"
)

func NewClient(opt *Options) *redis.Client {
	return redis.NewClient(opt)
}

type Options = redis.Options

func setupClient() *redis.Client {
	redisAddr := os.Getenv("REDIS_ADDRESS")

	if redisAddr == "" {
		zlog.Info().Msg("Redis address defaulting to localhost:6379")
		redisAddr = "localhost:6379" // Default address
	}

	return redis.NewClient(&redis.Options{
		Addr: redisAddr,
	})
}
