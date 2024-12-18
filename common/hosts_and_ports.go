package common

import (
	"fmt"
	"os"
	"strconv"

	"go.temporal.io/sdk/client"
)

// TODO register/reserve default port with IANA
const defaultServerPort = 8855

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

const defaultTemporalHost = "localhost"

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
