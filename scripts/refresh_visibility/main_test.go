package main

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/segmentio/ksuid"
	"github.com/stretchr/testify/require"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
	"go.temporal.io/server/api/adminservice/v1"

	"sidekick/common"
)

func idleWorkflow(ctx workflow.Context) error {
	var stop string
	workflow.GetSignalChannel(ctx, "stop").Receive(ctx, &stop)
	return nil
}

// Runs against the local Temporal server: refreshing a running and a closed
// run must leave status and history untouched and keep both listable.
func TestRefresh_RunningAndClosedRunStayIntact(t *testing.T) {
	if os.Getenv("SIDE_INTEGRATION_TEST") != "true" {
		t.Skip("Skipping integration test; SIDE_INTEGRATION_TEST not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	hostPort := common.GetTemporalServerHostPort()
	c, err := client.Dial(client.Options{HostPort: hostPort})
	require.NoError(t, err)
	defer c.Close()

	taskQueue := "refresh-visibility-" + ksuid.New().String()
	w := worker.New(c, taskQueue, worker.Options{})
	w.RegisterWorkflow(idleWorkflow)
	require.NoError(t, w.Start())
	defer w.Stop()

	start := func() client.WorkflowRun {
		run, err := c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{ID: "refresh-visibility-" + ksuid.New().String(), TaskQueue: taskQueue}, idleWorkflow)
		require.NoError(t, err)
		return run
	}
	running := start()
	closed := start()
	defer func() {
		_ = c.TerminateWorkflow(context.Background(), running.GetID(), running.GetRunID(), "test done")
	}()
	require.NoError(t, c.SignalWorkflow(ctx, closed.GetID(), closed.GetRunID(), "stop", "now"))
	require.NoError(t, closed.Get(ctx, nil))

	r, err := NewRefresher(ctx, c, hostPort, client.DefaultNamespace)
	require.NoError(t, err)
	defer r.Close()

	// Visibility writes are asynchronous; wait for the baseline listing so the
	// before/after comparison is meaningful.
	for _, run := range []client.WorkflowRun{running, closed} {
		require.Eventually(t, func() bool {
			listed, err := r.isListed(ctx, run.GetID(), run.GetRunID())
			return err == nil && listed
		}, 30*time.Second, 200*time.Millisecond)
	}

	// Listed runs are never refreshed, even in apply mode.
	for _, tc := range []struct {
		name   string
		run    client.WorkflowRun
		status string
	}{
		{"running", running, "Running"},
		{"closed", closed, "Completed"},
	} {
		res, err := r.Refresh(ctx, tc.run.GetID(), tc.run.GetRunID(), true, 30*time.Second)
		require.NoError(t, err, tc.name)
		require.True(t, res.Skipped, tc.name)
		require.False(t, res.Applied, tc.name)
		require.Equal(t, tc.status, res.Status, tc.name)
	}

	// The admin RPC itself must leave a run's status and history untouched;
	// whether it restores a missing visibility row can only be observed on a
	// run that is actually missing one.
	for _, tc := range []struct {
		name string
		run  client.WorkflowRun
	}{
		{"running", running},
		{"closed", closed},
	} {
		before, err := r.snapshot(ctx, tc.run.GetID(), tc.run.GetRunID())
		require.NoError(t, err, tc.name)
		_, err = r.admin.RefreshWorkflowTasks(ctx, &adminservice.RefreshWorkflowTasksRequest{
			NamespaceId: r.namespaceID,
			Execution:   &commonpb.WorkflowExecution{WorkflowId: tc.run.GetID(), RunId: tc.run.GetRunID()},
		})
		require.NoError(t, err, tc.name)
		time.Sleep(time.Second)
		require.NoError(t, r.verifyUnchanged(ctx, tc.run.GetID(), tc.run.GetRunID(), before, "after refresh"), tc.name)
		listed, err := r.isListed(ctx, tc.run.GetID(), tc.run.GetRunID())
		require.NoError(t, err, tc.name)
		require.True(t, listed, tc.name)
	}

	// The running workflow must still be able to make progress afterwards.
	require.NoError(t, c.SignalWorkflow(ctx, running.GetID(), running.GetRunID(), "stop", "now"))
	require.NoError(t, running.Get(ctx, nil))
}