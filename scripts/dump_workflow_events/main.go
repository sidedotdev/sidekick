package main

import (
	"context"
	"fmt"
	"os"
	"sidekick"
	"sidekick/common"
	"strconv"

	"go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	zerologadapter "logur.dev/adapter/zerolog"
	"logur.dev/logur"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: %s <workflow-id> [start-event-id] [end-event-id]\n", os.Args[0])
		os.Exit(1)
	}

	workflowID := os.Args[1]
	startEvent := 1
	endEvent := 9999
	if len(os.Args) >= 3 {
		startEvent, _ = strconv.Atoi(os.Args[2])
	}
	if len(os.Args) >= 4 {
		endEvent, _ = strconv.Atoi(os.Args[3])
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

	iter := c.GetWorkflowHistory(ctx, workflowID, "", false, enums.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
	for iter.HasNext() {
		event, err := iter.Next()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error fetching event: %v\n", err)
			os.Exit(1)
		}
		eid := event.EventId
		if eid < int64(startEvent) || eid > int64(endEvent) {
			continue
		}
		fmt.Printf("Event %d: %s\n", eid, event.EventType.String())
		switch event.EventType {
		case enums.EVENT_TYPE_ACTIVITY_TASK_SCHEDULED:
			attrs := event.GetActivityTaskScheduledEventAttributes()
			if attrs != nil {
				fmt.Printf("    ActivityType: %s\n", attrs.ActivityType.GetName())
				fmt.Printf("    ActivityId: %s\n", attrs.ActivityId)
			}
		case enums.EVENT_TYPE_ACTIVITY_TASK_COMPLETED:
			attrs := event.GetActivityTaskCompletedEventAttributes()
			if attrs != nil {
				fmt.Printf("    ScheduledEventId: %d\n", attrs.ScheduledEventId)
			}
		case enums.EVENT_TYPE_MARKER_RECORDED:
			attrs := event.GetMarkerRecordedEventAttributes()
			if attrs != nil {
				fmt.Printf("    MarkerName: %s\n", attrs.MarkerName)
			}
		case enums.EVENT_TYPE_WORKFLOW_TASK_SCHEDULED,
			enums.EVENT_TYPE_WORKFLOW_TASK_STARTED,
			enums.EVENT_TYPE_WORKFLOW_TASK_COMPLETED:
			// just the event type line is enough
		default:
			// print the event type line only
		}
	}
}
