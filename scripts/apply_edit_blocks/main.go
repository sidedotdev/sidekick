package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"sidekick/coding/lsp"
	"sidekick/dev"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

func main() {
	// Configure logging
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})

	// specific flags
	jsonFile := flag.String("file", "", "Path to the JSON input file")
	jsonString := flag.String("json", "", "JSON input string")
	timeout := flag.Duration("timeout", 180*time.Second, "Timeout for the activity execution")
	flag.Parse()

	var inputBytes []byte
	var err error

	if *jsonFile != "" {
		inputBytes, err = os.ReadFile(*jsonFile)
		if err != nil {
			log.Fatal().Err(err).Msgf("Failed to read file: %s", *jsonFile)
		}
	} else if *jsonString != "" {
		inputBytes = []byte(*jsonString)
	} else {
		// Read from stdin if no flags provided
		stat, _ := os.Stdin.Stat()
		if (stat.Mode() & os.ModeCharDevice) == 0 {
			inputBytes, err = io.ReadAll(os.Stdin)
			if err != nil {
				log.Fatal().Err(err).Msg("Failed to read from stdin")
			}
		} else {
			flag.Usage()
			os.Exit(1)
		}
	}

	// Temporal passes arguments as an array.
	// We expect [ApplyEditBlockActivityInput]
	var rawInputs []json.RawMessage
	if err := json.Unmarshal(inputBytes, &rawInputs); err != nil {
		log.Fatal().Err(err).Msg("Failed to unmarshal input JSON array")
	}

	if len(rawInputs) == 0 {
		log.Fatal().Msg("Input JSON array is empty")
	}

	var activityInput dev.ApplyEditBlockActivityInput
	if err := json.Unmarshal(rawInputs[0], &activityInput); err != nil {
		log.Fatal().Err(err).Msg("Failed to unmarshal first element into ApplyEditBlockActivityInput")
	}

	// Initialize dependencies
	lspActivities := &lsp.LSPActivities{
		LSPClientProvider: func(languageName string) lsp.LSPClient {
			return &lsp.Jsonrpc2LSPClient{
				LanguageName: languageName,
			}
		},
		InitializedClients: map[string]lsp.LSPClient{},
	}

	devActivities := &dev.DevActivities{
		LSPActivities: lspActivities,
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	log.Info().Msg("Starting ApplyEditBlocks...")
	result, err := devActivities.ApplyEditBlocks(ctx, activityInput)
	if err != nil {
		log.Error().Err(err).Msg("ApplyEditBlocks failed")
		os.Exit(1)
	}

	outputBytes, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to marshal result")
	}

	fmt.Println(string(outputBytes))
}
