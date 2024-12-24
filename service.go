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
		log.Info().Msg("Using Redis storage")
	case "sqlite":
		storage, err = sqlite.NewStorage()
		if err != nil {
			return nil, fmt.Errorf("failed to initialize SQLite storage: %w", err)
		}
		log.Info().Msg("Using SQLite storage")
	default:
		storage, err = sqlite.NewStorage()
		if err != nil {
			return nil, fmt.Errorf("failed to initialize default SQLite storage: %w", err)
		}
		log.Info().Msg("Using default SQLite storage")
	}

	streamer := redis.NewStreamer()
	return srv.NewDelegator(storage, streamer), nil
}