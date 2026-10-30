package temporal

import (
	"sidekick/common"
	"sidekick/temporal/readonlyproxy"
)

type ReadOnlyProxy = readonlyproxy.ReadOnlyProxy

func StartReadOnlyProxy(listenHostPort, temporalHostPort string, payloadStorage common.KeyValueStorage) (*ReadOnlyProxy, error) {
	return readonlyproxy.Start(listenHostPort, temporalHostPort, payloadStorage)
}
