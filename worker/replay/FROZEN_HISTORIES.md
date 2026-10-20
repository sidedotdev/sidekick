# Diagnosing replay failures with frozen histories

`TestFrozenWorkflowHistory` captures a workflow history with offloaded payloads
resolved, then replays it. Subsequent runs can replay that same file without
contacting Temporal or payload storage. This avoids comparing different inputs
when a running workflow advances between tests.

The diagnostic is skipped unless `SIDE_REPLAY_HISTORY_FILE` is set.

Unlike the automatic running-workflow suite, this explicit diagnostic does not
filter by worker ancestry: it must be able to reproduce incompatible histories.

The automatic suite checks the fetched, trimmed history's latest completed
workflow-task build, never the legacy start-version memo. Visibility build
metadata is advisory only: it may be missing or stale, so it does not prevent
history fetching. The history check excludes missing IDs, opaque binary hashes,
dirty builds, and commits that cannot be resolved as ancestors of the current
revision. A dirty build's base commit does not establish the ancestry of its
uncommitted code. Candidate exclusions and replay counts are logged; if no
histories are replayed because all are ineligible, the test is skipped, not
reported as successful replay coverage.

## Capture

Run from the repository root. Use an absolute output path because Go tests run
with the package directory as their working directory.

```sh
GOMAXPROCS=2 GOFLAGS=-p=1 \
SIDE_REPLAY_HISTORY_FILE="$PWD/.side/tmp/replay-history.json" \
SIDE_REPLAY_CAPTURE_WORKFLOW="<workflow-id>" \
SIDE_REPLAY_CAPTURE_RUN="<run-id>" \
go run ./scripts/affected_tests -count=1 -timeout=15m \
  -run '^TestFrozenWorkflowHistory$' ./worker/replay
```

If `SIDE_REPLAY_CAPTURE_RUN` is omitted, the diagnostic resolves the latest run
once before fetching. Capture uses the same Temporal connection selection and
resumable history cache as the running-workflow replay suite.

The file is written before replay starts, so a replay failure does not prevent
capture. Existing files are never overwritten. Choose a new path to capture
another snapshot.

**Histories contain private workflow inputs and outputs.** Decoded histories and
their diagnostic logs may contain secrets. Keep them in the git-ignored
`.side/tmp` directory; do not commit or share them without sanitization. Captured
files are created with owner-only permissions.

## Replay offline

Leave `SIDE_REPLAY_CAPTURE_WORKFLOW` unset:

```sh
GOMAXPROCS=2 GOFLAGS=-p=1 \
SIDE_REPLAY_HISTORY_FILE="$PWD/.side/tmp/replay-history.json" \
go run ./scripts/affected_tests -count=1 -timeout=15m \
  -run '^TestFrozenWorkflowHistory$' ./worker/replay
```

For a base-revision comparison, make this diagnostic available in a separate
worktree and point it at the same absolute history path. Record the revisions
and history checksum. Run capture, branch replay, base replay, suites, and
linters sequentially on memory-constrained machines; `-p=1` does not serialize
separate Go commands.

The automatic suite bounds the entire fetch/decode/replay lifetime to one
history at a time. On small machines, also set an appropriate Go GC memory
budget, for example `GOMEMLIMIT=512MiB` on a 1 GiB machine. Serial execution
bounds live histories but does not bound the heap retained between collections.
`GOMEMLIMIT` is a soft runtime limit, not a substitute for enough memory to hold
a single history. Check kernel OOM logs when a process is killed; do not infer a
replay defect from an unexplained exit alone.

A compatible frozen history provides a useful positive control alongside a
failing input. A failure on both branch and base is not evidence of a regression
introduced by the branch.

## Narrow the failure

Optional environment variables:

| Variable | Effect |
| --- | --- |
| `SIDE_REPLAY_PRINT_FROM` | First event ID to print as JSON. |
| `SIDE_REPLAY_PRINT_THROUGH` | Last event ID to print; required with `SIDE_REPLAY_PRINT_FROM`. |
| `SIDE_REPLAY_THROUGH_EVENT` | Replay only through this event ID. |
| `SIDE_REPLAY_TRIM_IN_FLIGHT=true` | Apply the running-workflow suite's in-flight workflow-task trimming. |

These options do not modify the captured file. Printed events still contain
payload data and must be treated as sensitive.

Find the first differing activity, side effect, or timer command rather than
assuming a later payload-decoding error is the cause. Check the ancestry of
commits introducing recorded version markers. A missing workflow-version memo
cannot establish compatibility, and even an ancestor start-version memo does
not prove that later workflow tasks ran on compatible workers.

Do not blacklist a workflow or weaken replay checks merely to make a suite
pass. For a production fix, retain a minimal sanitized regression history or
equivalent replay test, demonstrate its failure before the fix, and rerun the
same input afterward.