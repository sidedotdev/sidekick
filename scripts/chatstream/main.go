package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"sidekick"
	"sidekick/persisted_ai"
	"time"

	"github.com/rs/zerolog/log"
)

func main() {
	var filePath string
	var jsonStr string
	var timeoutSeconds int

	flag.StringVar(&filePath, "file", "", "Path to JSON file containing ChatStreamOptions array")
	flag.StringVar(&jsonStr, "json", "", "Raw JSON string containing ChatStreamOptions array or single object")
	flag.IntVar(&timeoutSeconds, "timeout", 180, "Timeout in seconds for ChatStream execution")
	flag.Parse()

	var inputBytes []byte
	var err error

	if filePath != "" {
		inputBytes, err = os.ReadFile(filePath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reading file: %v\n", err)
			os.Exit(1)
		}
	} else if jsonStr != "" {
		inputBytes = []byte(jsonStr)
	} else {
		inputBytes, err = io.ReadAll(os.Stdin)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reading from stdin: %v\n", err)
			os.Exit(1)
		}
	}

	var options []persisted_ai.ChatStreamOptions
	err = json.Unmarshal(inputBytes, &options)
	if err != nil {
		var singleOption persisted_ai.ChatStreamOptions
		err = json.Unmarshal(inputBytes, &singleOption)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error parsing JSON: %v\n", err)
			os.Exit(1)
		}
		options = []persisted_ai.ChatStreamOptions{singleOption}
	}

	if len(options) == 0 {
		fmt.Fprintf(os.Stderr, "No ChatStreamOptions provided\n")
		os.Exit(1)
	}

	service, err := sidekick.GetService()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error getting service: %v\n", err)
		os.Exit(1)
	}

	err = service.CheckConnection(context.Background())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error checking service connection: %v\n", err)
		os.Exit(1)
	}

	la := &persisted_ai.LlmActivities{
		Streamer: service,
	}

	timeout := time.Duration(timeoutSeconds) * time.Second

	for i, opt := range options {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)

		log.Info().Int("index", i).Msg("Running ChatStream")

		response, err := la.ChatStream(ctx, opt)
		cancel()

		if err != nil {
			fmt.Fprintf(os.Stderr, "Error running ChatStream (index %d): %v\n", i, err)
			os.Exit(1)
		}

		prettyJSON, err := json.MarshalIndent(response, "", "  ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error marshaling response to JSON (index %d): %v\n", i, err)
			os.Exit(1)
		}

		fmt.Println(string(prettyJSON))
	}
}
