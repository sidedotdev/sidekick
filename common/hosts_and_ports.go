package common

import (
	"fmt"
	"os"
	"strconv"

	"go.temporal.io/sdk/client"
)

// TODO register/reserve default port with IANA
const defaultServerPort = 8855
const defaultServerHost = "127.0.0.1"

func GetServerHost() string {
	host := os.Getenv("SIDE_SERVER_HOST")
	if host == "" {
		return defaultServerHost
	}
	return host
}

func GetServerPort() int {
	port := os.Getenv("SIDE_SERVER_PORT")
	if port == "" {
		return defaultServerPort
	}

	intPort, err := strconv.Atoi(port)
	if err != nil {
		panic(fmt.Sprintf("Failed to parse side api server port: %s", port))
	}
	return intPort
}

func GetTemporalNamespace() string {
	temporalNamespace := os.Getenv("SIDE_TEMPORAL_NAMESPACE")
	if temporalNamespace == "" {
		temporalNamespace = client.DefaultNamespace
	}
	return temporalNamespace
}

const defaultTemporalTaskQueue = "default"

func GetTemporalTaskQueue() string {
	temporalTaskQueue := os.Getenv("SIDE_TEMPORAL_TASK_QUEUE")
	if temporalTaskQueue == "" {
		temporalTaskQueue = defaultTemporalTaskQueue
	}
	return temporalTaskQueue
}

// NOTE: using "localhost" will not work as we bind via IP
const defaultTemporalHost = "127.0.0.1"

func GetTemporalServerHost() string {
	temporalServerHost := os.Getenv("SIDE_TEMPORAL_SERVER_HOST")
	if temporalServerHost == "" {
		temporalServerHost = defaultTemporalHost
	}
	return temporalServerHost
}

func GetTemporalServerPort() int {
	temporalServerPort := os.Getenv("SIDE_TEMPORAL_SERVER_PORT")
	if temporalServerPort == "" {
		return GetServerPort() + 10000
	}

	intPort, err := strconv.Atoi(temporalServerPort)
	if err != nil {
		panic(fmt.Sprintf("Failed to parse side api server port: %s", temporalServerPort))
	}
	return intPort
}

func GetTemporalServerHostPort() string {
	return fmt.Sprintf("%s:%d", GetTemporalServerHost(), GetTemporalServerPort())
}

const DefaultNatsServerHost = "127.0.0.1"

func GetNatsServerHost() string {
	natsServerHost := os.Getenv("SIDE_NATS_SERVER_HOST")
	if natsServerHost == "" {
		natsServerHost = DefaultNatsServerHost
	}
	return natsServerHost
}

const defaultNatsServerPort = 28855

func GetNatsServerPort() int {
	natsServerPort := os.Getenv("SIDE_NATS_SERVER_PORT")
	if natsServerPort == "" {
		return defaultNatsServerPort
	}

	intPort, err := strconv.Atoi(natsServerPort)
	if err != nil {
		panic(fmt.Sprintf("Failed to parse NATS server port: %s", natsServerPort))
	}
	return intPort
}
