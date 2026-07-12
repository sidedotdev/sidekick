package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sidekick"
	"sidekick/common"
	"sidekick/persisted_ai"
	"sort"
	"strconv"
	"strings"

	"go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	zerologadapter "logur.dev/adapter/zerolog"
	"logur.dev/logur"
)

func main() {
	verbose := flag.Bool("verbose", false, "print complete activity payloads and all workflow events")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "Usage: %s [-verbose] <workflow-id> [start-event-id] [end-event-id]\n", os.Args[0])
		flag.PrintDefaults()
	}
	flag.Parse()

	args := flag.Args()
	if len(args) < 1 {
		flag.Usage()
		os.Exit(1)
	}

	workflowID := args[0]
	startEvent := 1
	endEvent := 9999
	if len(args) >= 2 {
		startEvent, _ = strconv.Atoi(args[1])
	}
	if len(args) >= 3 {
		endEvent, _ = strconv.Atoi(args[2])
	}

	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})

	ctx := context.Background()

	service, err := sidekick.GetService()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to initialize service: %v\n", err)
		os.Exit(1)
	}
	clientOptions, err := common.NewTemporalClientOptions(service, common.GetTemporalServerHostPort())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create client options: %v\n", err)
		os.Exit(1)
	}
	clientOptions.Logger = logur.LoggerToKV(zerologadapter.New(log.Logger))
	c, err := client.Dial(clientOptions)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to dial Temporal: %v\n", err)
		os.Exit(1)
	}
	defer c.Close()

	activityTypes := make(map[int64]string)
	iter := c.GetWorkflowHistory(ctx, workflowID, "", false, enums.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
	for iter.HasNext() {
		event, err := iter.Next()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error fetching event: %v\n", err)
			os.Exit(1)
		}

		eid := event.EventId
		switch event.EventType {
		case enums.EVENT_TYPE_ACTIVITY_TASK_SCHEDULED:
			attrs := event.GetActivityTaskScheduledEventAttributes()
			if attrs == nil {
				continue
			}

			activityType := attrs.ActivityType.GetName()
			activityTypes[eid] = activityType
			if eid < int64(startEvent) || eid > int64(endEvent) {
				continue
			}

			fmt.Printf("%d ActivityTaskScheduled %s id=%s\n", eid, activityType, attrs.ActivityId)
			if attrs.Input != nil {
				for i, input := range clientOptions.DataConverter.ToStrings(attrs.Input) {
					if *verbose {
						fmt.Printf("    Input[%d]: %s\n", i, input)
					} else {
						fmt.Printf("    Input[%d]: %s\n", i, summarizePayload(ctx, service, input))
					}
				}
			}
		case enums.EVENT_TYPE_ACTIVITY_TASK_COMPLETED:
			if eid < int64(startEvent) || eid > int64(endEvent) {
				continue
			}
			attrs := event.GetActivityTaskCompletedEventAttributes()
			if attrs == nil {
				continue
			}

			activityType := activityTypes[attrs.ScheduledEventId]
			fmt.Printf("%d ActivityTaskCompleted %s scheduled=%d\n", eid, activityType, attrs.ScheduledEventId)
			if attrs.Result != nil {
				for i, result := range clientOptions.DataConverter.ToStrings(attrs.Result) {
					if *verbose {
						fmt.Printf("    Result[%d]: %s\n", i, result)
					} else {
						fmt.Printf("    Result[%d]: %s\n", i, summarizePayload(ctx, service, result))
					}
				}
			}
		case enums.EVENT_TYPE_ACTIVITY_TASK_FAILED:
			if eid < int64(startEvent) || eid > int64(endEvent) {
				continue
			}
			attrs := event.GetActivityTaskFailedEventAttributes()
			if attrs == nil {
				continue
			}

			activityType := activityTypes[attrs.ScheduledEventId]
			fmt.Printf("%d ActivityTaskFailed %s scheduled=%d started=%d\n",
				eid, activityType, attrs.ScheduledEventId, attrs.StartedEventId)
			if attrs.Failure != nil {
				fmt.Printf("    Failure: %s\n", attrs.Failure.Message)
				if attrs.Failure.Cause != nil {
					fmt.Printf("    Cause: %s\n", attrs.Failure.Cause.Message)
				}
			}
		case enums.EVENT_TYPE_MARKER_RECORDED:
			if eid < int64(startEvent) || eid > int64(endEvent) {
				continue
			}
			attrs := event.GetMarkerRecordedEventAttributes()
			if attrs == nil {
				continue
			}

			fmt.Printf("%d MarkerRecorded %s\n", eid, attrs.MarkerName)
			if *verbose {
				for name, payloads := range attrs.Details {
					for i, detail := range clientOptions.DataConverter.ToStrings(payloads) {
						fmt.Printf("    Detail[%s][%d]: %s\n", name, i, detail)
					}
				}
			}
		default:
			if *verbose && eid >= int64(startEvent) && eid <= int64(endEvent) {
				fmt.Printf("%d %s\n", eid, event.EventType.String())
			}
		}
	}
}

func summarizePayload(ctx context.Context, storage common.KeyValueStorage, payload string) string {
	var value any
	if err := json.Unmarshal([]byte(payload), &value); err != nil {
		return fmt.Sprintf("bytes=%d nonJSON", len(payload))
	}

	historyComplete := false
	scanValue := value
	if object, ok := value.(map[string]any); ok {
		if rawHistory, ok := object["ChatHistory"]; ok && rawHistory != nil {
			historyBytes, err := json.Marshal(rawHistory)
			if err != nil {
				return fmt.Sprintf("bytes=%d historyError=%q", len(payload), err)
			}

			var history persisted_ai.ChatHistoryContainer
			if err := json.Unmarshal(historyBytes, &history); err != nil {
				return fmt.Sprintf("bytes=%d historyError=%q", len(payload), err)
			}
			if err := history.Hydrate(ctx, storage); err != nil {
				return fmt.Sprintf("bytes=%d historyError=%q", len(payload), err)
			}

			messagesBytes, err := json.Marshal(history.Llm2Messages())
			if err != nil {
				return fmt.Sprintf("bytes=%d historyError=%q", len(payload), err)
			}
			if err := json.Unmarshal(messagesBytes, &scanValue); err != nil {
				return fmt.Sprintf("bytes=%d historyError=%q", len(payload), err)
			}
			historyComplete = true
		} else if _, ok := object["messages"]; ok {
			historyComplete = true
		}
	}

	messageCount := 0
	toolCalls := make(map[string]int)
	toolResults := make(map[string]int)

	var walk func(any)
	walk = func(current any) {
		switch current := current.(type) {
		case []any:
			for _, item := range current {
				walk(item)
			}
		case map[string]any:
			if _, hasRole := current["role"]; hasRole {
				if _, hasContent := current["content"]; hasContent {
					messageCount++
				}
			}
			if toolUse, ok := current["toolUse"].(map[string]any); ok {
				if id, ok := toolUse["id"].(string); ok && id != "" {
					toolCalls[id]++
				}
			}
			if toolResult, ok := current["toolResult"].(map[string]any); ok {
				if id, ok := toolResult["toolCallId"].(string); ok && id != "" {
					toolResults[id]++
				}
			}
			for _, child := range current {
				walk(child)
			}
		}
	}
	walk(scanValue)

	parts := []string{
		fmt.Sprintf("bytes=%d", len(payload)),
		fmt.Sprintf("messages=%d", messageCount),
		fmt.Sprintf("toolCalls=%d", len(toolCalls)),
		fmt.Sprintf("toolResults=%d", len(toolResults)),
	}
	if !historyComplete {
		return strings.Join(parts, " ")
	}

	missingResults := make([]string, 0)
	for id := range toolCalls {
		if toolResults[id] == 0 {
			missingResults = append(missingResults, id)
		}
	}
	orphanedResults := make([]string, 0)
	for id := range toolResults {
		if toolCalls[id] == 0 {
			orphanedResults = append(orphanedResults, id)
		}
	}
	sort.Strings(missingResults)
	sort.Strings(orphanedResults)

	if len(missingResults) > 0 {
		parts = append(parts, "missingResults="+strings.Join(missingResults, ","))
	}
	if len(orphanedResults) > 0 {
		parts = append(parts, "orphanedResults="+strings.Join(orphanedResults, ","))
	}
	return strings.Join(parts, " ")
}
