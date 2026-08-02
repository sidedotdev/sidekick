// Prints DescribeWorkflowExecution details for a workflow: status, history
// size, and pending workflow task / activity retry state. Useful for spotting
// workflows whose workflow tasks are stuck in a retry loop (e.g. replay
// exceeding the workflow task timeout, or nondeterminism), which grind the
// worker and Temporal server and surface as "buffered query cleared" floods.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"go.temporal.io/sdk/client"

	"sidekick"
	"sidekick/common"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: %s <workflow-id> [run-id]\n", os.Args[0])
		os.Exit(1)
	}
	workflowId := os.Args[1]
	runId := ""
	if len(os.Args) >= 3 {
		runId = os.Args[2]
	}

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
	c, err := client.Dial(clientOptions)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to dial Temporal: %v\n", err)
		os.Exit(1)
	}
	defer c.Close()

	desc, err := c.DescribeWorkflowExecution(context.Background(), workflowId, runId)
	if err != nil {
		fmt.Fprintf(os.Stderr, "DescribeWorkflowExecution failed: %v\n", err)
		os.Exit(1)
	}

	info := desc.GetWorkflowExecutionInfo()
	fmt.Printf("WorkflowId:       %s\n", info.GetExecution().GetWorkflowId())
	fmt.Printf("RunId:            %s\n", info.GetExecution().GetRunId())
	fmt.Printf("Type:             %s\n", info.GetType().GetName())
	fmt.Printf("Status:           %s\n", info.GetStatus())
	fmt.Printf("StartTime:        %s\n", info.GetStartTime().AsTime().Format(time.RFC3339))
	fmt.Printf("HistoryLength:    %d\n", info.GetHistoryLength())
	fmt.Printf("HistorySizeBytes: %d\n", info.GetHistorySizeBytes())
	fmt.Printf("TaskQueue:        %s\n", info.GetTaskQueue())

	if pending := desc.GetPendingWorkflowTask(); pending != nil {
		fmt.Printf("PendingWorkflowTask:\n")
		fmt.Printf("  State:                 %s\n", pending.GetState())
		fmt.Printf("  Attempt:               %d\n", pending.GetAttempt())
		scheduled := pending.GetScheduledTime().AsTime()
		fmt.Printf("  ScheduledTime:         %s (age %s)\n", scheduled.Format(time.RFC3339), time.Since(scheduled).Round(time.Second))
		if original := pending.GetOriginalScheduledTime().AsTime(); original.Unix() != 0 {
			fmt.Printf("  OriginalScheduledTime: %s (age %s)\n", original.Format(time.RFC3339), time.Since(original).Round(time.Second))
		}
		if started := pending.GetStartedTime().AsTime(); started.Unix() != 0 {
			fmt.Printf("  StartedTime:           %s\n", started.Format(time.RFC3339))
		}
	} else {
		fmt.Println("PendingWorkflowTask: none")
	}

	for _, pa := range desc.GetPendingActivities() {
		fmt.Printf("PendingActivity %s:\n", pa.GetActivityType().GetName())
		fmt.Printf("  State:   %s\n", pa.GetState())
		fmt.Printf("  Attempt: %d\n", pa.GetAttempt())
		if hb := pa.GetLastHeartbeatTime().AsTime(); hb.Unix() != 0 {
			fmt.Printf("  LastHeartbeat: %s (age %s)\n", hb.Format(time.RFC3339), time.Since(hb).Round(time.Second))
		}
		if failure := pa.GetLastFailure(); failure != nil {
			fmt.Printf("  LastFailure: %s\n", failure.GetMessage())
		}
	}
}
