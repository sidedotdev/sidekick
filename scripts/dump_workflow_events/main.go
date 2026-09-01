package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"sidekick"
	"sidekick/common"
	"sidekick/persisted_ai"
	"sort"
	"strconv"
	"strings"
	"time"

	"go.temporal.io/api/enums/v1"
	workflowservice "go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	zerologadapter "logur.dev/adapter/zerolog"
	"logur.dev/logur"
)

func main() {
	verbose := flag.Bool("verbose", false, "print complete activity payloads and all workflow events")
	subflowsOnly := flag.Bool("subflows", false, "print only the subflow hierarchy decoded from PersistSubflow activity inputs")
	runID := flag.String("run-id", "", "select a specific workflow RunID instead of the latest run")
	activityTypeFilter := flag.String("activity-type", "", "print only lifecycle events for this activity type")
	listRuns := flag.Bool("list-runs", false, "list RunIDs for the workflow without dumping history")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "Usage: %s [options] <workflow-id> [start-event-id] [end-event-id]\n", os.Args[0])
		flag.PrintDefaults()
	}
	flag.Parse()

	args := flag.Args()
	if len(args) < 1 || len(args) > 3 {
		flag.Usage()
		os.Exit(1)
	}

	workflowID := args[0]
	startEvent := 1
	endEvent := math.MaxInt
	if len(args) >= 2 {
		var err error
		startEvent, err = strconv.Atoi(args[1])
		if err != nil {
			fmt.Fprintf(os.Stderr, "Invalid start event ID %q: %v\n", args[1], err)
			os.Exit(1)
		}
	}
	if len(args) >= 3 {
		var err error
		endEvent, err = strconv.Atoi(args[2])
		if err != nil {
			fmt.Fprintf(os.Stderr, "Invalid end event ID %q: %v\n", args[2], err)
			os.Exit(1)
		}
	}
	if *subflowsOnly {
		// suppress per-event output; only the hierarchy is printed at the end
		startEvent, endEvent = 0, -1
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

	if *listRuns {
		queryWorkflowID := strings.ReplaceAll(workflowID, "'", "''")
		var nextPageToken []byte
		for {
			response, err := c.ListWorkflow(ctx, &workflowservice.ListWorkflowExecutionsRequest{
				Namespace:     clientOptions.Namespace,
				PageSize:      100,
				NextPageToken: nextPageToken,
				Query:         fmt.Sprintf("WorkflowId = '%s'", queryWorkflowID),
			})
			if err != nil {
				fmt.Fprintf(os.Stderr, "Failed to list workflow runs: %v\n", err)
				os.Exit(1)
			}
			for _, execution := range response.Executions {
				info := execution.GetExecution()
				fmt.Printf("RunId=%s StartTime=%s CloseTime=%s Status=%s\n",
					info.GetRunId(),
					execution.GetStartTime().AsTime().Format(time.RFC3339Nano),
					execution.GetCloseTime().AsTime().Format(time.RFC3339Nano),
					execution.GetStatus())
			}
			nextPageToken = response.NextPageToken
			if len(nextPageToken) == 0 {
				break
			}
		}
		return
	}

	selectedRunID := *runID
	if selectedRunID == "" {
		description, err := c.DescribeWorkflowExecution(ctx, workflowID, "")
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to resolve latest workflow RunID: %v\n", err)
			os.Exit(1)
		}
		selectedRunID = description.GetWorkflowExecutionInfo().GetExecution().GetRunId()
	}
	fmt.Printf("WorkflowId=%s RunId=%s\n", workflowID, selectedRunID)

	type scheduledActivity struct {
		activityType string
		scheduledAt  string
		startedAt    string
		identity     string
	}
	activities := make(map[int64]*scheduledActivity)
	type subflowInfo struct {
		Id              string `json:"id"`
		Name            string `json:"name"`
		Type            string `json:"type"`
		Status          string `json:"status"`
		ParentSubflowId string `json:"parentSubflowId"`
	}
	subflows := make(map[string]*subflowInfo)
	var subflowOrder []string
	eventPrefix := func(eventID int64, eventTime interface{ AsTime() time.Time }) string {
		return fmt.Sprintf("%d %s", eventID, eventTime.AsTime().Format(time.RFC3339Nano))
	}

	iter := c.GetWorkflowHistory(ctx, workflowID, selectedRunID, false, enums.HISTORY_EVENT_FILTER_TYPE_ALL_EVENT)
	for iter.HasNext() {
		event, err := iter.Next()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error fetching event: %v\n", err)
			os.Exit(1)
		}

		eid := event.EventId
		if *activityTypeFilter != "" {
			switch event.EventType {
			case enums.EVENT_TYPE_ACTIVITY_TASK_SCHEDULED,
				enums.EVENT_TYPE_ACTIVITY_TASK_STARTED,
				enums.EVENT_TYPE_ACTIVITY_TASK_COMPLETED,
				enums.EVENT_TYPE_ACTIVITY_TASK_FAILED,
				enums.EVENT_TYPE_ACTIVITY_TASK_TIMED_OUT:
			default:
				continue
			}
		}
		switch event.EventType {
		case enums.EVENT_TYPE_ACTIVITY_TASK_SCHEDULED:
			attrs := event.GetActivityTaskScheduledEventAttributes()
			if attrs == nil {
				continue
			}

			activityType := attrs.ActivityType.GetName()
			activities[eid] = &scheduledActivity{
				activityType: activityType,
				scheduledAt:  event.EventTime.AsTime().Format(time.RFC3339Nano),
			}
			if activityType == "PersistSubflow" && attrs.Input != nil {
				for _, input := range clientOptions.DataConverter.ToStrings(attrs.Input) {
					var info subflowInfo
					if err := json.Unmarshal([]byte(input), &info); err != nil || info.Id == "" {
						continue
					}
					if _, seen := subflows[info.Id]; !seen {
						subflowOrder = append(subflowOrder, info.Id)
					}
					subflows[info.Id] = &info
				}
			}
			if eid < int64(startEvent) || eid > int64(endEvent) {
				continue
			}
			if *activityTypeFilter != "" && activityType != *activityTypeFilter {
				continue
			}

			fmt.Printf("%s ActivityTaskScheduled %s id=%s scheduleToStart=%s startToClose=%s\n",
				eventPrefix(eid, event.EventTime),
				activityType,
				attrs.ActivityId,
				attrs.ScheduleToStartTimeout.AsDuration(),
				attrs.StartToCloseTimeout.AsDuration())
			if attrs.Input != nil {
				for i, input := range clientOptions.DataConverter.ToStrings(attrs.Input) {
					if *verbose {
						fmt.Printf("    Input[%d]: %s\n", i, input)
					} else {
						fmt.Printf("    Input[%d]: %s\n", i, summarizePayload(ctx, service, input))
					}
				}
			}
		case enums.EVENT_TYPE_ACTIVITY_TASK_STARTED:
			attrs := event.GetActivityTaskStartedEventAttributes()
			if attrs == nil {
				continue
			}
			activity := activities[attrs.ScheduledEventId]
			if activity != nil {
				activity.startedAt = event.EventTime.AsTime().Format(time.RFC3339Nano)
				activity.identity = attrs.Identity
			}
			if eid < int64(startEvent) || eid > int64(endEvent) {
				continue
			}
			activityType := ""
			if activity != nil {
				activityType = activity.activityType
			}
			if *activityTypeFilter != "" && activityType != *activityTypeFilter {
				continue
			}
			fmt.Printf("%s ActivityTaskStarted %s scheduled=%d identity=%q attempt=%d\n",
				eventPrefix(eid, event.EventTime),
				activityType,
				attrs.ScheduledEventId,
				attrs.Identity,
				attrs.Attempt)
		case enums.EVENT_TYPE_ACTIVITY_TASK_COMPLETED:
			if eid < int64(startEvent) || eid > int64(endEvent) {
				continue
			}
			attrs := event.GetActivityTaskCompletedEventAttributes()
			if attrs == nil {
				continue
			}

			activity := activities[attrs.ScheduledEventId]
			activityType := ""
			if activity != nil {
				activityType = activity.activityType
			}
			if *activityTypeFilter != "" && activityType != *activityTypeFilter {
				continue
			}
			fmt.Printf("%s ActivityTaskCompleted %s scheduled=%d identity=%q\n",
				eventPrefix(eid, event.EventTime),
				activityType,
				attrs.ScheduledEventId,
				activity.identity)
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

			activity := activities[attrs.ScheduledEventId]
			activityType := ""
			identity := ""
			if activity != nil {
				activityType = activity.activityType
				identity = activity.identity
			}
			if *activityTypeFilter != "" && activityType != *activityTypeFilter {
				continue
			}
			fmt.Printf("%s ActivityTaskFailed %s scheduled=%d started=%d identity=%q\n",
				eventPrefix(eid, event.EventTime),
				activityType,
				attrs.ScheduledEventId,
				attrs.StartedEventId,
				identity)
			if attrs.Failure != nil {
				fmt.Printf("    Failure: %s\n", attrs.Failure.Message)
				if attrs.Failure.Cause != nil {
					fmt.Printf("    Cause: %s\n", attrs.Failure.Cause.Message)
				}
			}
		case enums.EVENT_TYPE_ACTIVITY_TASK_TIMED_OUT:
			if eid < int64(startEvent) || eid > int64(endEvent) {
				continue
			}
			attrs := event.GetActivityTaskTimedOutEventAttributes()
			if attrs == nil {
				continue
			}
			activity := activities[attrs.ScheduledEventId]
			activityType := ""
			identity := ""
			scheduledAt := ""
			startedAt := ""
			if activity != nil {
				activityType = activity.activityType
				identity = activity.identity
				scheduledAt = activity.scheduledAt
				startedAt = activity.startedAt
			}
			if *activityTypeFilter != "" && activityType != *activityTypeFilter {
				continue
			}
			fmt.Printf("%s ActivityTaskTimedOut %s scheduled=%d started=%d identity=%q retryState=%s scheduledAt=%s startedAt=%s\n",
				eventPrefix(eid, event.EventTime),
				activityType,
				attrs.ScheduledEventId,
				attrs.StartedEventId,
				identity,
				attrs.RetryState,
				scheduledAt,
				startedAt)
			if attrs.Failure != nil {
				fmt.Printf("    Failure: %s\n", attrs.Failure.Message)
			}
		case enums.EVENT_TYPE_WORKFLOW_EXECUTION_STARTED:
			if eid < int64(startEvent) || eid > int64(endEvent) {
				continue
			}
			attrs := event.GetWorkflowExecutionStartedEventAttributes()
			if attrs == nil {
				continue
			}

			fmt.Printf("%d WorkflowExecutionStarted %s\n", eid, attrs.WorkflowType.GetName())
			if attrs.Input != nil {
				for i, input := range clientOptions.DataConverter.ToStrings(attrs.Input) {
					if *verbose {
						fmt.Printf("    Input[%d]: %s\n", i, input)
					} else {
						fmt.Printf("    Input[%d]: %s\n", i, summarizePayload(ctx, service, input))
					}
				}
			}
		case enums.EVENT_TYPE_WORKFLOW_EXECUTION_SIGNALED:
			if eid < int64(startEvent) || eid > int64(endEvent) {
				continue
			}
			attrs := event.GetWorkflowExecutionSignaledEventAttributes()
			if attrs == nil {
				continue
			}

			fmt.Printf("%d WorkflowExecutionSignaled %s\n", eid, attrs.SignalName)
			if attrs.Input != nil {
				for i, input := range clientOptions.DataConverter.ToStrings(attrs.Input) {
					if *verbose {
						fmt.Printf("    Input[%d]: %s\n", i, input)
					} else {
						fmt.Printf("    Input[%d]: %s\n", i, summarizePayload(ctx, service, input))
					}
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
		case enums.EVENT_TYPE_WORKFLOW_TASK_SCHEDULED:
			if *verbose && eid >= int64(startEvent) && eid <= int64(endEvent) {
				attrs := event.GetWorkflowTaskScheduledEventAttributes()
				fmt.Printf("%s WorkflowTaskScheduled queue=%q kind=%s attempt=%d\n",
					eventPrefix(eid, event.EventTime),
					attrs.GetTaskQueue().GetName(),
					attrs.GetTaskQueue().GetKind(),
					attrs.GetAttempt())
			}
		case enums.EVENT_TYPE_WORKFLOW_TASK_STARTED:
			if *verbose && eid >= int64(startEvent) && eid <= int64(endEvent) {
				attrs := event.GetWorkflowTaskStartedEventAttributes()
				fmt.Printf("%s WorkflowTaskStarted identity=%q historySizeBytes=%d suggestContinueAsNew=%t\n",
					eventPrefix(eid, event.EventTime),
					attrs.GetIdentity(),
					attrs.GetHistorySizeBytes(),
					attrs.GetSuggestContinueAsNew())
			}
		case enums.EVENT_TYPE_WORKFLOW_TASK_COMPLETED:
			if *verbose && eid >= int64(startEvent) && eid <= int64(endEvent) {
				attrs := event.GetWorkflowTaskCompletedEventAttributes()
				fmt.Printf("%s WorkflowTaskCompleted scheduled=%d started=%d identity=%q\n",
					eventPrefix(eid, event.EventTime),
					attrs.GetScheduledEventId(),
					attrs.GetStartedEventId(),
					attrs.GetIdentity())
			}
		case enums.EVENT_TYPE_WORKFLOW_TASK_TIMED_OUT:
			if eid >= int64(startEvent) && eid <= int64(endEvent) {
				attrs := event.GetWorkflowTaskTimedOutEventAttributes()
				fmt.Printf("%s WorkflowTaskTimedOut scheduled=%d started=%d timeoutType=%s\n",
					eventPrefix(eid, event.EventTime),
					attrs.GetScheduledEventId(),
					attrs.GetStartedEventId(),
					attrs.GetTimeoutType())
			}
		case enums.EVENT_TYPE_WORKFLOW_TASK_FAILED:
			if eid >= int64(startEvent) && eid <= int64(endEvent) {
				attrs := event.GetWorkflowTaskFailedEventAttributes()
				fmt.Printf("%s WorkflowTaskFailed scheduled=%d started=%d cause=%s identity=%q\n",
					eventPrefix(eid, event.EventTime),
					attrs.GetScheduledEventId(),
					attrs.GetStartedEventId(),
					attrs.GetCause(),
					attrs.GetIdentity())
				if attrs.GetFailure() != nil {
					fmt.Printf("    Failure: %s\n", attrs.GetFailure().GetMessage())
					if attrs.GetFailure().GetCause() != nil {
						fmt.Printf("    Cause: %s\n", attrs.GetFailure().GetCause().GetMessage())
					}
				}
			}
		default:
			if *verbose && eid >= int64(startEvent) && eid <= int64(endEvent) {
				fmt.Printf("%d %s\n", eid, event.EventType.String())
			}
		}
	}

	if *subflowsOnly {
		children := make(map[string][]string)
		var roots []string
		for _, id := range subflowOrder {
			parentId := subflows[id].ParentSubflowId
			if parentId == "" || subflows[parentId] == nil {
				roots = append(roots, id)
			} else {
				children[parentId] = append(children[parentId], id)
			}
		}
		var printTree func(id string, depth int)
		printTree = func(id string, depth int) {
			info := subflows[id]
			fmt.Printf("%s%s name=%q type=%s status=%s\n",
				strings.Repeat("  ", depth), info.Id, info.Name, info.Type, info.Status)
			for _, childId := range children[id] {
				printTree(childId, depth+1)
			}
		}
		for _, id := range roots {
			printTree(id, 0)
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
