package sidekick

import (
	"context"
	"fmt"
	"os"
	"sidekick/common"
	"sidekick/nats"
	"sidekick/srv"
	"sidekick/srv/jetstream"
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
	case "redis":
		streamer = redis.NewStreamer()
		log.Info().Msg("Using Redis streamer")
	case "", "jetstream":
		_, err = nats.GetConnection()
		if err != nil && common.GetNatsServerHost() == common.DefaultNatsServerHost {
			natsServer, err := nats.GetOrNewServer()
			if err != nil {
				return nil, fmt.Errorf("failed to initialize NATS server: %w", err)
			}
			err = natsServer.Start(context.Background())
			if err != nil {
				return nil, fmt.Errorf("failed to start NATS server: %w", err)
			}
		}

		nc, err := nats.GetConnection()
		if err != nil {
			return nil, fmt.Errorf("failed to connect to NATS: %w", err)
		}
		streamer, err = jetstream.NewStreamer(nc)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize JetStream streamer: %w", err)
		}
		log.Info().Msg("Using JetStream streamer")
	default:
		log.Fatal().Str("streamer", streamerType).Msg("Unknown streamer type")
	}

	return srv.NewDelegator(storage, streamer), nil
}
