package main

import (
	"os"
	"os/signal"
	"sidekick"
	"sidekick/common"
	"sidekick/temporal"
	"syscall"

	"github.com/joho/godotenv"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

func main() {
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})
	log.Info().Str("host", common.GetTemporalServerHost()).Int("port", common.GetTemporalServerPort()).Msg("Starting Temporal server")

	if err := godotenv.Load(); err != nil {
		if !os.IsNotExist(err) {
			log.Fatal().Err(err).Msg("Error loading .env file")
		}
	}

	common.StartPprofServer()

	server := temporal.Start()

	// Expose the read-only Temporal proxy alongside the server. Dev setups run
	// this standalone binary (rather than `side start`), so starting the proxy
	// here is what makes it reachable for reverse-forwarding into sandboxed
	// environments (see port_forwards in side.yml). It only allows listing
	// workflows and reading histories, so it is safe to expose.
	var payloadStorage common.KeyValueStorage
	if storage, err := sidekick.GetStorage(); err != nil {
		log.Error().Err(err).Msg("Failed to initialize storage for read-only temporal proxy; offloaded payloads won't be inlined")
	} else {
		payloadStorage = storage
	}
	readOnlyProxy, err := temporal.StartReadOnlyProxy(common.GetTemporalReadOnlyServerHostPort(), common.GetTemporalServerHostPort(), payloadStorage)
	if err != nil {
		log.Error().Err(err).Msg("Failed to start read-only temporal proxy")
	} else {
		log.Info().Str("component", "Temporal Read-Only Proxy").Msg(readOnlyProxy.Addr())
	}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	if readOnlyProxy != nil {
		readOnlyProxy.Stop()
	}
	server.Stop()
}
