// Command wfclient exercises a trivial Temporal workflow against a running
// Temporal server. It is used by run.sh to verify that the embedded Temporal
// server still works, and that workflows created before a server/schema upgrade
// remain accessible afterwards.
//
// Usage:
//
//	wfclient start <workflowID>            # run a workflow to completion, print result
//	wfclient describe <workflowID>         # print status of an existing workflow
//	wfclient start-blocking <workflowID>   # start a workflow parked on a signal, leave it running
//	wfclient signal-complete <workflowID>  # signal a parked workflow and wait for it to finish
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	enums "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

const taskQueue = "temporal-upgrade-test-tq"

// GreetingActivity is a trivial activity that needs no external services or API
// keys, so it can run in any environment.
func GreetingActivity(ctx context.Context, name string) (string, error) {
	return "hello, " + name, nil
}

// GreetingWorkflow runs a single activity and returns its result.
func GreetingWorkflow(ctx workflow.Context, name string) (string, error) {
	ao := workflow.ActivityOptions{StartToCloseTimeout: 10 * time.Second}
	ctx = workflow.WithActivityOptions(ctx, ao)
	var result string
	err := workflow.ExecuteActivity(ctx, GreetingActivity, name).Get(ctx, &result)
	return result, err
}

// proceedSignal unblocks BlockingWorkflow.
const proceedSignal = "proceed"

// BlockingWorkflow parks on proceedSignal before running the greeting activity.
// It is used to leave an execution in-progress across a schema migration and
// then drive it to completion, validating that mid-flight workflows survive.
func BlockingWorkflow(ctx workflow.Context, name string) (string, error) {
	workflow.GetSignalChannel(ctx, proceedSignal).Receive(ctx, nil)
	ao := workflow.ActivityOptions{StartToCloseTimeout: 10 * time.Second}
	ctx = workflow.WithActivityOptions(ctx, ao)
	var result string
	err := workflow.ExecuteActivity(ctx, GreetingActivity, name).Get(ctx, &result)
	return result, err
}

func hostPort() string {
	if hp := os.Getenv("SIDE_TEMPORAL_HOST_PORT"); hp != "" {
		return hp
	}
	return "127.0.0.1:17233"
}

func namespace() string {
	if ns := os.Getenv("SIDE_TEMPORAL_NAMESPACE"); ns != "" {
		return ns
	}
	return "default"
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: wfclient <ping|start|describe|start-blocking|signal-complete> [workflowID]")
		os.Exit(2)
	}
	cmd := os.Args[1]
	var workflowID string
	if len(os.Args) > 2 {
		workflowID = os.Args[2]
	}
	if cmd != "ping" && workflowID == "" {
		fatal("command %q requires a workflowID", cmd)
	}

	c, err := client.Dial(client.Options{HostPort: hostPort(), Namespace: namespace()})
	if err != nil {
		fatal("dial temporal: %v", err)
	}
	defer c.Close()

	ctx := context.Background()
	switch cmd {
	case "ping":
		ping(ctx, c)
	case "start":
		start(ctx, c, workflowID)
	case "describe":
		describe(ctx, c, workflowID)
	case "start-blocking":
		startBlocking(ctx, c, workflowID)
	case "signal-complete":
		signalComplete(ctx, c, workflowID)
	default:
		fatal("unknown command %q", cmd)
	}
}

func ping(ctx context.Context, c client.Client) {
	if _, err := c.CheckHealth(ctx, &client.CheckHealthRequest{}); err != nil {
		fatal("temporal not healthy: %v", err)
	}
	fmt.Println("OK")
}

func start(ctx context.Context, c client.Client, workflowID string) {
	w := worker.New(c, taskQueue, worker.Options{})
	w.RegisterWorkflow(GreetingWorkflow)
	w.RegisterActivity(GreetingActivity)
	if err := w.Start(); err != nil {
		fatal("start worker: %v", err)
	}
	defer w.Stop()

	run, err := c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:        workflowID,
		TaskQueue: taskQueue,
	}, GreetingWorkflow, "sidekick")
	if err != nil {
		fatal("execute workflow: %v", err)
	}

	var result string
	if err := run.Get(ctx, &result); err != nil {
		fatal("get workflow result: %v", err)
	}
	fmt.Printf("STARTED workflowID=%s runID=%s result=%q\n", workflowID, run.GetRunID(), result)
}

// startBlocking starts BlockingWorkflow, waits until it has parked on the
// signal, then returns while leaving the execution Running. This produces a
// genuinely in-progress workflow that can be migrated across a server upgrade.
func startBlocking(ctx context.Context, c client.Client, workflowID string) {
	w := worker.New(c, taskQueue, worker.Options{})
	w.RegisterWorkflow(BlockingWorkflow)
	w.RegisterActivity(GreetingActivity)
	if err := w.Start(); err != nil {
		fatal("start worker: %v", err)
	}
	defer w.Stop()

	run, err := c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:        workflowID,
		TaskQueue: taskQueue,
	}, BlockingWorkflow, "sidekick")
	if err != nil {
		fatal("execute workflow: %v", err)
	}

	deadline := time.Now().Add(30 * time.Second)
	for {
		resp, err := c.DescribeWorkflowExecution(ctx, workflowID, "")
		if err == nil &&
			resp.GetPendingWorkflowTask() == nil &&
			resp.GetWorkflowExecutionInfo().GetStatus() == enums.WORKFLOW_EXECUTION_STATUS_RUNNING {
			break
		}
		if time.Now().After(deadline) {
			fatal("workflow %s did not reach blocked state in time", workflowID)
		}
		time.Sleep(500 * time.Millisecond)
	}
	fmt.Printf("STARTED-BLOCKING workflowID=%s runID=%s status=Running\n", workflowID, run.GetRunID())
}

// signalComplete unblocks a workflow left parked by startBlocking and waits for
// it to finish, proving that an in-progress execution survives the migration.
func signalComplete(ctx context.Context, c client.Client, workflowID string) {
	w := worker.New(c, taskQueue, worker.Options{})
	w.RegisterWorkflow(BlockingWorkflow)
	w.RegisterActivity(GreetingActivity)
	if err := w.Start(); err != nil {
		fatal("start worker: %v", err)
	}
	defer w.Stop()

	if err := c.SignalWorkflow(ctx, workflowID, "", proceedSignal, nil); err != nil {
		fatal("signal workflow: %v", err)
	}

	var result string
	if err := c.GetWorkflow(ctx, workflowID, "").Get(ctx, &result); err != nil {
		fatal("get workflow result: %v", err)
	}
	fmt.Printf("COMPLETED workflowID=%s result=%q\n", workflowID, result)
}

func describe(ctx context.Context, c client.Client, workflowID string) {
	resp, err := c.DescribeWorkflowExecution(ctx, workflowID, "")
	if err != nil {
		fatal("describe workflow: %v", err)
	}
	info := resp.GetWorkflowExecutionInfo()
	fmt.Printf("DESCRIBE workflowID=%s runID=%s status=%s type=%s\n",
		info.GetExecution().GetWorkflowId(),
		info.GetExecution().GetRunId(),
		info.GetStatus().String(),
		info.GetType().GetName(),
	)
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "ERROR: "+format+"\n", args...)
	os.Exit(1)
}
