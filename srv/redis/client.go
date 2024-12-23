package redis

import "github.com/redis/go-redis/v9"

func NewClient(opt *Options) *redis.Client {
	return redis.NewClient(opt)
}

type Options = redis.Options
