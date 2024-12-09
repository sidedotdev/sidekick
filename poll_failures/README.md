## PollFailuresWorkflow

### Overview
The `PollFailuresWorkflow` is designed to watch for failures in other workflows
within a workspace. It periodically checks for workflows that have failed, or
timed out and updates the status of associated tasks to reflect these failures.

### Operation
This workflow utilizes Temporal's visibility API to list workflows filtered by
status (failed, terminated, timed out) and workspace ID. For each detected
failed workflow, it checks if the parent ID of the flow record corresponds to a
task. If so, it updates the task status to "failed" using the database's
`PersistTask` method.

The `PollFailuresWorkflow` is scheduled to run at regular intervals using
Temporal's scheduled actions. A schedule is created whenever a new workspace is
created.