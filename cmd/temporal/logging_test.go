package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sidekick/common"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/stretchr/testify/require"
	"go.temporal.io/server/common/log/tag"
)

func TestTemporalServerLogsSurviveRestart(t *testing.T) {
	if message := os.Getenv("SIDE_TEMPORAL_LOG_TEST_MESSAGE"); message != "" {
		configureLogging()
		serverLogger := common.NewZerologLogger(log.Logger.Level(zerolog.WarnLevel))
		serverLogger.Error(message, tag.Error(errors.New("database read failed")))

		// The application logger writes asynchronously.
		require.Eventually(t, func() bool {
			files, err := filepath.Glob(filepath.Join(os.Getenv("SIDE_STATE_HOME"), "sidekick-*.log"))
			if err != nil {
				return false
			}
			for _, file := range files {
				data, err := os.ReadFile(file)
				if err == nil && bytes.Contains(data, []byte(message)) {
					return true
				}
			}
			return false
		}, 5*time.Second, 10*time.Millisecond, "Temporal server error was not persisted")
		return
	}

	t.Parallel()
	stateHome := t.TempDir()
	messages := []string{
		"GetWorkflowExecution: failed to get CHASM nodes before restart",
		"GetWorkflowExecution: failed to get CHASM nodes after restart",
	}
	for _, message := range messages {
		// Separate processes exercise logger initialization and append behavior
		// without sharing the singleton logger between simulated server starts.
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestTemporalServerLogsSurviveRestart$")
		cmd.Env = append(os.Environ(),
			"SIDE_STATE_HOME="+stateHome,
			"SIDE_LOG_LEVEL=0",
			"SIDE_TEMPORAL_LOG_TEST_MESSAGE="+message,
		)
		output, err := cmd.CombinedOutput()
		cancel()
		require.NoError(t, err, "%s", output)
		require.Contains(t, string(output), message, "server errors must remain visible in the console")
	}

	files, err := filepath.Glob(filepath.Join(stateHome, "sidekick-*.log"))
	require.NoError(t, err)
	require.NotEmpty(t, files)

	entries := make(map[string]map[string]any)
	for _, file := range files {
		data, err := os.ReadFile(file)
		require.NoError(t, err)
		scanner := bufio.NewScanner(bytes.NewReader(data))
		for scanner.Scan() {
			var entry map[string]any
			require.NoError(t, json.Unmarshal(scanner.Bytes(), &entry))
			message, ok := entry["message"].(string)
			require.True(t, ok)
			entries[message] = entry
		}
		require.NoError(t, scanner.Err())
	}
	for _, message := range messages {
		entry, ok := entries[message]
		require.True(t, ok, "missing persisted server error: %s", message)
		require.Equal(t, "error", entry["level"])
		require.Equal(t, "database read failed", entry["error"])
		timestamp, ok := entry["time"].(string)
		require.True(t, ok)
		_, err := time.Parse(time.RFC3339Nano, timestamp)
		require.NoError(t, err)
	}
}
