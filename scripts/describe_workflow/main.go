// Prints DescribeWorkflowExecution details for a workflow: status, history
// size, and pending workflow task / activity retry state. Useful for spotting
// workflows whose workflow tasks are stuck in a retry loop (e.g. replay
// exceeding the workflow task timeout, or nondeterminism), which grind the
// worker and Temporal server and surface as "buffered query cleared" floods.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"time"

	commonpb "go.temporal.io/api/common/v1"
	workflowpb "go.temporal.io/api/workflow/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"

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
	if firstRunId := info.GetFirstRunId(); firstRunId != "" {
		fmt.Printf("FirstRunId:       %s\n", firstRunId)
	}

	printSection("Versioning", versioningLines(info))
	printSection("Memo", memoLines(clientOptions.DataConverter, info.GetMemo()))
	printSection("SearchAttributes", searchAttributeLines(info.GetSearchAttributes()))

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

// printSection prints a titled, indented block, making an absent section
// explicit rather than silently printing nothing.
func printSection(title string, lines []string) {
	if len(lines) == 0 {
		fmt.Printf("%s: none\n", title)
		return
	}
	fmt.Printf("%s:\n", title)
	for _, line := range lines {
		fmt.Printf("  %s\n", line)
	}
}

// searchAttributeLines renders indexed fields, notably TemporalChangeVersion
// (the workflow.GetVersion change IDs an execution is pinned to) and BuildIds.
// Search attributes bypass the payload codec, so they decode with the default
// converter regardless of how the client is configured.
func searchAttributeLines(attributes *commonpb.SearchAttributes) []string {
	return payloadFieldLines(converter.GetDefaultDataConverter(), attributes.GetIndexedFields())
}

func memoLines(dc converter.DataConverter, memo *commonpb.Memo) []string {
	return payloadFieldLines(dc, memo.GetFields())
}

// payloadFieldLines renders named payloads sorted by name, expanding list
// values one entry per line. Decode failures are reported per field so one
// unreadable value never hides the rest of the metadata.
func payloadFieldLines(dc converter.DataConverter, fields map[string]*commonpb.Payload) []string {
	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}
	sort.Strings(names)

	lines := make([]string, 0, len(names))
	for _, name := range names {
		var value any
		if err := dc.FromPayload(fields[name], &value); err != nil {
			lines = append(lines, fmt.Sprintf("%s: <decode error: %v>", name, err))
			continue
		}
		list, isList := value.([]any)
		if !isList || len(list) == 0 {
			lines = append(lines, fmt.Sprintf("%s: %s", name, formatValue(value)))
			continue
		}
		lines = append(lines, name+":")
		for _, item := range list {
			lines = append(lines, "  "+formatValue(item))
		}
	}
	return lines
}

func formatValue(value any) string {
	if str, ok := value.(string); ok {
		return str
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprintf("<encode error: %v>", err)
	}
	return string(encoded)
}

// versioningLines summarizes worker versioning metadata, which determines
// whether a running workflow is pinned to a particular worker build.
func versioningLines(info *workflowpb.WorkflowExecutionInfo) []string {
	var lines []string
	if buildId := info.GetAssignedBuildId(); buildId != "" {
		lines = append(lines, fmt.Sprintf("AssignedBuildId: %s", buildId))
	}
	if buildId := info.GetInheritedBuildId(); buildId != "" {
		lines = append(lines, fmt.Sprintf("InheritedBuildId: %s", buildId))
	}
	if deployment := info.GetWorkerDeploymentName(); deployment != "" {
		lines = append(lines, fmt.Sprintf("WorkerDeploymentName: %s", deployment))
	}
	if stamp := info.GetMostRecentWorkerVersionStamp(); stamp != nil {
		lines = append(lines, fmt.Sprintf("MostRecentWorkerVersionStamp: buildId=%q useVersioning=%t", stamp.GetBuildId(), stamp.GetUseVersioning()))
	}
	if versioning := info.GetVersioningInfo(); versioning != nil {
		lines = append(lines, fmt.Sprintf("VersioningBehavior: %s", versioning.GetBehavior()))
		if version := versioning.GetVersion(); version != "" {
			lines = append(lines, fmt.Sprintf("VersioningVersion: %s", version))
		}
	}
	return lines
}
