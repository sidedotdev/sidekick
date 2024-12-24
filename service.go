package sidekick

import (
	"fmt"
	"os"
	"sidekick/srv"
	"sidekick/srv/redis"
	"sidekick/srv/sqlite"

	"github.com/rs/zerolog/log"
)

func GetService() (srv.Service, error) {
	storageType := os.Getenv("SIDE_STORAGE")
	var storage srv.Storage
	var err error

	switch storageType {
	case "redis":
		storage = redis.NewStorage()
		log.Debug().Msg("Using Redis storage")
	case "sqlite", "":
		storage, err = sqlite.NewStorage()
		if err != nil {
			return nil, fmt.Errorf("failed to initialize SQLite storage: %w", err)
		}
		log.Debug().Msg("Using SQLite storage")
	default:
		log.Fatal().Str("storage", storageType).Msg("Unknown storage type")
	}

	streamer := redis.NewStreamer()
	return srv.NewDelegator(storage, streamer), nil
}