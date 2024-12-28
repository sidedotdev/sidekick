package sidekick

import (
	"fmt"
	"os"
	"sidekick/nats"
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
	case "sqlite", "":
		storage, err = sqlite.NewStorage()
		if err != nil {
			return nil, fmt.Errorf("failed to initialize SQLite storage: %w", err)
		}
		log.Info().Msg("Using SQLite storage")
	default:
		log.Fatal().Str("storage", storageType).Msg("Unknown storage type")
	}

	streamerType := os.Getenv("SIDE_STREAMER")
	var streamer srv.Streamer

	switch streamerType {
	case "", "redis": // TODO switch default to jetstream later on, after all interfaces are implemented
		streamer = redis.NewStreamer()
		log.Info().Msg("Using Redis streamer")
	case "jetstream":
		nc, err := nats.GetConnection()
		if err != nil {
			return nil, fmt.Errorf("failed to get NATS connection: %w", err)
		}
		// TODO uncomment later after the last interface is implemented
		/*
			streamer, err = jetstream.NewStreamer(nc)
			if err != nil {
				return nil, fmt.Errorf("failed to initialize JetStream streamer: %w", err)
			}
			log.Info().Msg("Using JetStream streamer")
		*/
		log.Fatal().Interface("nc", nc).Msg("JetStream streamer is not implemented yet")
	default:
		log.Fatal().Str("streamer", streamerType).Msg("Unknown streamer type")
	}

	return srv.NewDelegator(storage, streamer), nil
}
