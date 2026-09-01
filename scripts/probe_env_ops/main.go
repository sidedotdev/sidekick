// Command probe_env_ops benchmarks environment operations against the exact
// environment recorded in a workflow activity's input, to attribute slow
// activities to transport latency vs connection reuse vs serialization of
// operations. It reports a Stat round-trip baseline, sequential ReadFile cost,
// and concurrent ReadFile cost over the same files.
//
// Usage:
//
//	go run ./scripts/probe_env_ops [-run-id id] [-files n] [-concurrency n] <flow-id> <scheduled-event-id> [paths...]
//
// When no explicit paths are given, the first -files entries of `git ls-files
// '*.go'` in the env's working directory are read.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"sidekick"
	"sidekick/common"
	"sidekick/env"

	"go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
	"golang.org/x/sync/errgroup"
)

func must(err error, what string) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", what, err)
		os.Exit(1)
	}
}

func loadEnvContainer(ctx context.Context, flowID, runID string, eventID int64) (env.EnvContainer, string, error) {
	service, err := sidekick.GetService()
	if err != nil {
		return env.EnvContainer{}, "", fmt.Errorf("error initializing storage: %w", err)
	}

	clientOptions, err := common.NewTemporalClientOptions(service, common.GetTemporalServerHostPort())
	if err != nil {
		return env.EnvContainer{}, "", fmt.Errorf("error creating Temporal client options: %w", err)
	}

	temporalClient, err := client.Dial(clientOptions)
	if err != nil {
		return env.EnvContainer{}, "", fmt.Errorf("error connecting to Temporal: %w", err)
	}
	defer temporalClient.Close()

	dc := clientOptions.DataConverter
	if dc == nil {
		dc = converter.GetDefaultDataConverter()
	}

	iter := temporalClient.GetWorkflowHistory(ctx, flowID, runID, false, enums.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
	for iter.HasNext() {
		event, err := iter.Next()
		if err != nil {
			return env.EnvContainer{}, "", fmt.Errorf("error fetching workflow history: %w", err)
		}
		if event.EventId != eventID || event.EventType != enums.EVENT_TYPE_ACTIVITY_TASK_SCHEDULED {
			continue
		}
		attrs := event.GetActivityTaskScheduledEventAttributes()
		if attrs == nil || attrs.Input == nil || len(attrs.Input.Payloads) == 0 {
			return env.EnvContainer{}, "", fmt.Errorf("event %d has no input payloads", eventID)
		}
		var raw json.RawMessage
		if err := dc.FromPayload(attrs.Input.Payloads[0], &raw); err != nil {
			return env.EnvContainer{}, "", fmt.Errorf("decode input payload: %w", err)
		}
		var input struct {
			EnvContainerLower *env.EnvContainer `json:"envContainer"`
			EnvContainerUpper *env.EnvContainer `json:"EnvContainer"`
		}
		if err := json.Unmarshal(raw, &input); err != nil {
			return env.EnvContainer{}, "", fmt.Errorf("unmarshal activity input: %w", err)
		}
		ec := input.EnvContainerUpper
		if ec == nil {
			ec = input.EnvContainerLower
		}
		if ec == nil || ec.Env == nil {
			return env.EnvContainer{}, "", fmt.Errorf("no envContainer found in input of event %d", eventID)
		}
		return *ec, attrs.ActivityType.GetName(), nil
	}
	return env.EnvContainer{}, "", fmt.Errorf("ActivityTaskScheduled event %d not found (pass the scheduled event id, not started/completed)", eventID)
}

func main() {
	runID := flag.String("run-id", "", "select a specific workflow RunID")
	numFiles := flag.Int("files", 20, "number of files to read when no paths are given")
	concurrency := flag.Int("concurrency", 15, "concurrency for the parallel read phase")
	flag.Parse()
	args := flag.Args()
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: probe_env_ops [-run-id id] [-files n] [-concurrency n] <flow-id> <scheduled-event-id> [paths...]")
		os.Exit(2)
	}
	eventID, err := strconv.ParseInt(args[1], 10, 64)
	must(err, "parse event id")

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	ec, activityName, err := loadEnvContainer(ctx, args[0], *runID, eventID)
	must(err, "load env container")
	e := ec.Env
	fmt.Printf("env: type=%s workingDir=%s (from %s input)\n", e.GetType(), e.GetWorkingDirectory(), activityName)

	// Warm the transport twice: the first op pays any dial cost, the second
	// shows the steady-state command round trip.
	for i := range 2 {
		start := time.Now()
		_, err := e.RunCommand(ctx, env.EnvRunCommandInput{Command: "true"})
		must(err, "warmup command")
		fmt.Printf("RunCommand(true) #%d: %s\n", i+1, time.Since(start).Round(time.Millisecond))
	}

	const statOps = 5
	statStart := time.Now()
	for range statOps {
		_, err := e.Stat(ctx, ".")
		must(err, "stat")
	}
	statAvg := time.Since(statStart) / statOps
	fmt.Printf("Stat avg (1 SFTP round trip baseline): %s\n", statAvg.Round(time.Millisecond))

	paths := args[2:]
	if len(paths) == 0 {
		out, err := e.RunCommand(ctx, env.EnvRunCommandInput{Command: "git", Args: []string{"ls-files", "*.go"}})
		must(err, "git ls-files")
		for _, line := range strings.Split(strings.TrimSpace(out.Stdout), "\n") {
			if line != "" {
				paths = append(paths, line)
			}
			if len(paths) >= *numFiles {
				break
			}
		}
	}
	if len(paths) == 0 {
		fmt.Fprintln(os.Stderr, "no files to read")
		os.Exit(1)
	}

	fmt.Printf("\nsequential ReadFile of %d files:\n", len(paths))
	var totalBytes int
	var minD, maxD time.Duration
	seqStart := time.Now()
	for _, p := range paths {
		opStart := time.Now()
		data, err := e.ReadFile(ctx, p)
		must(err, "read "+p)
		d := time.Since(opStart)
		totalBytes += len(data)
		if minD == 0 || d < minD {
			minD = d
		}
		if d > maxD {
			maxD = d
		}
	}
	seqTotal := time.Since(seqStart)
	fmt.Printf("total=%s avg=%s min=%s max=%s bytes=%d (~%.1f round trips/read vs Stat)\n",
		seqTotal.Round(time.Millisecond), (seqTotal / time.Duration(len(paths))).Round(time.Millisecond),
		minD.Round(time.Millisecond), maxD.Round(time.Millisecond), totalBytes,
		float64(seqTotal/time.Duration(len(paths)))/float64(statAvg))

	fmt.Printf("\nconcurrent ReadFile of %d files (limit %d):\n", len(paths), *concurrency)
	concStart := time.Now()
	g, groupCtx := errgroup.WithContext(ctx)
	g.SetLimit(*concurrency)
	for _, p := range paths {
		g.Go(func() error {
			_, err := e.ReadFile(groupCtx, p)
			return err
		})
	}
	must(g.Wait(), "concurrent reads")
	concTotal := time.Since(concStart)
	fmt.Printf("total=%s (%.1fx speedup over sequential)\n", concTotal.Round(time.Millisecond), float64(seqTotal)/float64(concTotal))
}
