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

// GetStorage constructs the configured srv.Storage backend (per SIDE_STORAGE)
// without initializing a streamer. This suits callers that only need storage,
// e.g. building a Temporal payload codec, and must avoid the side effects of
// starting an embedded NATS server.
func GetStorage() (srv.Storage, error) {
	storageType := os.Getenv("SIDE_STORAGE")
	switch storageType {
	case "redis":
		log.Info().Msg("Using Redis storage")
		return redis.NewStorage(), nil
	case "sqlite", "":
		storage, err := sqlite.NewStorage()
		if err != nil {
			return nil, fmt.Errorf("failed to initialize SQLite storage: %w", err)
		}
		log.Info().Msg("Using SQLite storage")
		return storage, nil
	default:
		return nil, fmt.Errorf("unknown storage type: %q", storageType)
	}
}

func GetService() (*srv.Delegator, error) {
	storage, err := GetStorage()
	if err != nil {
		return nil, err
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
