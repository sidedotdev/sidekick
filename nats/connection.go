package nats

import (
	"fmt"
	"sidekick/common"

	"github.com/nats-io/nats.go"
)

func GetConnection() (*nats.Conn, error) {
	nc, err := nats.Connect(fmt.Sprintf("nats://%s:%d", common.GetNatsServerHost(), common.GetNatsServerPort()), nats.MaxReconnects(-1))

	if err != nil {
		return nil, err
	}

	return nc, nil
}
