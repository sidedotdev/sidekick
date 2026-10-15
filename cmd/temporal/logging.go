package main

import (
	"sidekick/logger"

	"github.com/rs/zerolog/log"
)

func configureLogging() {
	log.Logger = logger.Get()
}
