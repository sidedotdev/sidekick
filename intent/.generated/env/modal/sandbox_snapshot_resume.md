---
intent_links:
  - intent: "#problem"
    code:
      - env/modal_watchdog.sh
      - env/modal.go
  - intent: "#seal-drain-snapshot-terminate"
    code:
      - env/modal_watchdog.sh
      - sideagent/seal.go
      - sideagent/server.go
      - sideagent/sftp.go
      - env/modal.go:modalFencedCommand
  - intent: "#admission-follows-work-not-connections"
    code:
      - sideagent/seal.go
      - sideagent/server.go:execute
      - sideagent/sftp.go:serveSFTP
      - sideagent/seal_test.go
  - intent: "#shutdown-state-survives-watchdog-restarts"
    code:
      - env/modal_watchdog.sh
      - env/modal.go:modalSSHDCommand
      - env/modal_guard_test.go:TestModalWatchdogIdleShutdown
  - intent: "#refusal-is-not-a-command-result"
    code:
      - sideagent/protocol.go
      - env/environment.go:modalExecResponse
      - env/environment.go:RunCommand
      - env/modal.go:modalExecCommand
  - intent: "#bounded-recovery-reuses-existing-retries"
    code:
      - env/modal_sealed_recovery.go
      - env/environment.go:RunCommand
      - env/environment.go:runCommandWithRestoreNotice
      - env/remote_sftp.go:withSFTPRetry
      - env/ssh_transport_native.go:WithSFTP
      - env/modal_sealed_recovery_test.go
  - intent: "#a-resume-is-not-a-rollback"
    code:
      - env/modal_guard_app.py
      - env/modal_snapshot.sh
      - env/modal_watchdog.sh
      - env/modal_guard.go:modalIncarnationLineage
      - env/modal.go:modalRestoreContext
      - env/modal.go:modalRefreshEndpoint
      - env/modal.go:modalCreateSandboxOnce
      - env/modal.go:modalCheckSandbox
      - env/modal_guard_behavior_test.go:TestModalGuardIncarnationLineage
      - env/modal_refresh_test.go:TestModalRefreshEndpoint
  - intent: "#shared-operation-admission"
    code:
      - env/modal_operation_admission.go
      - env/agent.go
      - env/managed_activity.go:BeginActivity
      - env/modal_operation_admission_test.go
  - intent: "#scope-and-tradeoffs"
    code:
      - env/environment.go:withRestoreNotice
      - env/modal_guard_test.go:TestModalFencedCommand
      - sideagent/seal_test.go
---
# Modal Sandbox Snapshot and Resume Safety

> Generated/inferred intent. Subordinate to human-authored intent.

## Problem

A sandbox restored from a snapshot contains only the state captured by that
snapshot. Accepting edits after the final capture but before termination can
therefore acknowledge work that disappears on resume. Detecting inactivity
alone does not close this race: new work can arrive during shutdown.

Idle shutdown must preserve accepted work, not merely produce a usable image.

## Seal, Drain, Snapshot, Terminate

Shutdown follows one ordering:

1. Drain admitted operations and seal the sandbox against new work.
2. Capture and confirm the shutdown snapshot.
3. Check for activity that invalidates the shutdown decision.
4. Record termination intent, then request termination without lifting the seal.

Admission and sealing share a cross-process lock. Agent commands, SFTP work,
and commands arriving through the Modal API participate in the same fence.
The seal check occurs under admission, so shutdown cannot slip between checking
and starting work. Failure to drain within the shutdown wait budget abandons
that shutdown attempt.

Activity is checked after draining and again after capture. If shutdown is
abandoned before termination is recorded, the seal is lifted so work can resume.
Snapshot failures reopen admission and retry with backoff; they never authorize
destroying unsaved work.

## Admission Follows Work, Not Connections

Commands hold admission through execution. SFTP holds it while requests are
outstanding or file/directory handles remain open, including read-only handles.
This conservatively preserves a multi-request transfer rather than allowing
shutdown between its open, read/write, and close operations.

Responses are matched to requests independently of arrival order or transport
chunking. Only a successful close releases its matching handle. Duplicate
outstanding request IDs are refused; untrackable handles retain admission until
session teardown rather than risk releasing it early.

An idle SFTP session with no outstanding work holds no admission. Closing its
transport does not prematurely release running handlers; admission is released
after workers drain and handles close, or by process exit. Lock ownership is
explicit rather than dependent on garbage collection.

This admission policy is separate from the watchdog's existing SSH-child,
terminal, activity-marker, and load heuristics. It does not guarantee that
every idle pooled agent session is recognized as idle by those heuristics.

## Shutdown State Survives Watchdog Restarts

Once termination may be in flight, the sandbox remains sealed even if the
request times out or the watchdog restarts. A delayed terminate must never
destroy newly accepted work.

A persistent regular-file marker records this state before dispatch. Failure
to write it aborts shutdown. A restarted watchdog resumes termination rather
than unsealing or taking another shutdown snapshot.

Termination retries reuse the completed checkpoint even if activity subsequently
appears. Repeated termination failures eventually trigger self-termination;
this escalation is permitted only after a successful shutdown snapshot.
The marker survives watchdog restart, but retry counters do not.

The termination marker is written after capture, so it is not inherited by
that snapshot. The sandbox startup command clears inherited shutdown fencing
before starting the watchdog and SSH service. Restarting only the watchdog or
the SSH daemon within its supervision loop does not clear it.

## Refusal Is Not a Command Result

A structured agent refusal means the command did not run. Modal classifies it
with a distinct retryable error, not a fabricated command exit status or an
SSH transport failure. The shared response adapter used by other environments
also prevents such a refusal from appearing successful.

Agent admission fails closed on lock errors or an indeterminate seal check.
The command protocol reports these admission failures through the same refusal
flag; it does not distinguish them from shutdown. SFTP instead ends the request
stream before dispatching refused work, so it provides no structured reason
that a client can safely interpret as a shutdown-specific retry instruction.

The API command path also refuses work while sealed and fails closed when its
lock cannot be acquired. Its exit-code/stderr convention is not proof of
non-execution: a real command can reproduce it. Such errors do not authorize
automatic replay.

Refreshing an endpoint does not by itself prove that a sealed sandbox has been
replaced. Recovery must not force recycling while its snapshot is pending.

## Bounded Recovery Reuses Existing Retries

A structured command refusal waits and retries that individual request with
a two-minute admission budget and caller cancellation. An admitted command
retains the caller's execution deadline, not the shorter admission budget.
If the sandbox disappears, existing transport recovery reconnects or restores
it; if shutdown is abandoned, the same sandbox can admit the retry.

Before an eligible SFTP retry, a harmless command probes readiness through
that same command recovery path. This also covers a refused initial handshake,
before any file operation runs. Native SFTP reacquires its SSH client and
configuration after the probe because recovery can select a replacement endpoint.

If the probe restores a snapshot, SFTP logs the exact restore notice and
continues the eligible retry. A restore alone does not interrupt file operations:
failing them would neither undo the restore nor recover missing work.
The notice comes from explicit recovery metadata, not command stderr.
Genuine probe failures, cancellation, and timeouts still return errors.

Waiting does not make an arbitrary SFTP EOF proof of non-execution. Existing
operation retry eligibility and replay limits still apply; the probe only
delays the retry until readiness, cancellation, or timeout. Ambiguous API
command errors remain non-retryable.

## A Resume Is Not a Rollback

Restoring the sealed shutdown snapshot of the incarnation that was last
brought up loses nothing: it is the ordinary idle hibernate/resume cycle, and
nobody is warned about it (not command stderr, not SFTP or transport recovery
logs, not the in-sandbox lineage log). Every other recreation is reported as
a possible rollback, including one that fell back to a clean image because
the checkpoint could not be used.

The proof lives with the guard, because only the guard sees both halves: the
host registers every incarnation it brings up (or finds running) before that
incarnation can accept work, and the guard records a clean shutdown only
when it terminates an incarnation under the same named seal its latest
snapshot was taken under. A checkpoint from a seal that was since lifted
proves nothing, since work may have landed in between. A successor that dies
before its first snapshot keeps the warning: its predecessor's clean shutdown
never vouches for it, and a registration that lands late never displaces its
successor. Missing, unreadable or contradictory evidence (Modal still naming
a different finished sandbox) also keeps the warning. Registration fails
closed: an incarnation the guard could not record is not handed out, by
creation, by check, or by endpoint refresh.

## Shared Operation Admission

Modal agent borrowing and environment file access acquire a managed admission
lease before dispatching work. Structured refusals of that lease use the same
bounded command recovery, including endpoint refresh and sandbox replacement.
No tool-specific or Temporal retry policy is needed, so automatic environment
operations receive the same protection.

The lease holds the shared admission lock until the protected work finishes.
Shutdown cannot seal between admission and a search, symbol-definition request,
or filesystem request on another pooled channel. This also protects filesystem
operations comprising several SFTP requests without interpreting a dropped SFTP
stream as proof that those requests never executed.

Acquiring admission may retry; the protected callback is invoked at most once.
An error returned by that callback never restarts it, even if the error resembles
an admission refusal. File operations likewise gain no additional replay policy.
Lease loss cancels the scoped operation context and is returned as an error.
Admission is released after operation cleanup rather than merely on caller
cancellation.

This adds an admission exchange around file access and scoped agent borrowing.
It also keeps a sandbox active for the lifetime of the borrowing callback, not
just each RPC inside it. The tradeoff avoids replaying arbitrary callbacks or
whole multi-operation activities while preserving the pooled-client contract.

## Scope and Tradeoffs

- Safety takes precedence over availability during shutdown. Bounded recovery
  can still fail if shutdown outlasts its budget or readiness cannot be
  established. Retrying a whole activity is not inherently safe merely because
  its final request was refused.
- Snapshots are checkpoints, not continuous replication or a transaction log.
  The admission fence coordinates participating operations, not arbitrary
  background writers. Unexpected sandbox loss can still lose work newer than
  the last checkpoint.
- Existing restore reporting remains separate from admission fencing: a
  detected restore is surfaced to callers rather than presented as an
  uninterrupted filesystem. Snapshot recovery does not imply that the local
  review worktree contains every remote edit.
- API commands retain their login-shell execution semantics while holding the
  fence. Missing locking support is an infrastructure error, not permission to
  run unfenced.

Related intent: [SSH transport and safe retries](../../ssh.md).