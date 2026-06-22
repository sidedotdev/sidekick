package utils

import (
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// StorageActivityOptions returns activity options tuned for activities that
// only interact with storage. These activities are expected to complete
// quickly in the happy path, and the only failures we expect are transient
// (e.g. due to worker restarts), so we use a short start-to-close timeout and
// retry generously.
func StorageActivityOptions() workflow.ActivityOptions {
	return workflow.ActivityOptions{
		StartToCloseTimeout: 5 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    time.Second,
			BackoffCoefficient: 2.0,
			MaximumInterval:    5 * time.Second,
			MaximumAttempts:    10,
		},
	}
}

// storageActivityOptionsChangeID gates the introduction of
// StorageActivityOptions. Old workflow histories did not configure these
// short timeouts and high retry counts, so we gate behind a workflow version
// to preserve determinism on replay of in-flight workflows.
const storageActivityOptionsChangeID = "storage-activity-options"

// WithStorageActivityOptions returns a workflow context configured with
// StorageActivityOptions. Use this for activities that only interact with
// storage and have no side effects beyond persistence/lookup. For workflows
// that started before these options were introduced, the original context is
// returned unchanged.
func WithStorageActivityOptions(ctx workflow.Context) workflow.Context {
	v := workflow.GetVersion(ctx, storageActivityOptionsChangeID, workflow.DefaultVersion, 1)
	if v == workflow.DefaultVersion {
		return ctx
	}
	return workflow.WithActivityOptions(ctx, StorageActivityOptions())
}
