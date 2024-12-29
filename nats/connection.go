package nats

import (
	"fmt"
	"sidekick/common"

	"github.com/nats-io/nats.go"
	"github.com/rs/zerolog/log"
)

func GetConnection() (*nats.Conn, error) {
	nc, err := nats.Connect(fmt.Sprintf("nats://%s:%d", common.GetNatsServerHost(), common.GetNatsServerPort()))

	if err != nil {
		log.Error().Err(err).Msg("Failed to connect to NATS")
		return nil, err
	}

	return nc, nil
}
