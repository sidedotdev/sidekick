package sidekick

import (
	"sidekick/srv"
	"sidekick/srv/redis"
)

func GetService() srv.Service {
	return srv.NewDelegator(redis.NewStorage(), redis.NewStreamer())
}