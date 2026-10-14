# Payload bloat findings (kv / core / Temporal), October 2026

Working notes for the investigation on branch `side/analyze-db-payload-bloat`.
Kept at the repo root so they survive chat-history truncation. Update as the
investigation progresses; delete once the fixes land and this is folded into
proper docs.

## Tooling

| tool | purpose |
|---|---|
| `go run ./scripts/payload_bloat` | Extracts kv, core and Temporal DB stats (read-only; Temporal from a copy-on-write snapshot) to CSVs plus `.side/tmp/payload_bloat/payload_bloat.duckdb`, then prints a report. `-skip-extract` re-runs only the report; `-sources kv` re-extracts one source. Full extraction takes 5–8 minutes. Tables: `size_buckets`, `large_items`, `db_files`, `table_sizes`, `executions`, `codec_refs` (codec keys referenced by surviving history), `codec_entries` (every codec entry in kv). |
| `go run ./scripts/inspect_codec_payload codec/<ksuid>...` | Decodes offloaded payloads from kv and summarizes bytes per JSON path, to see what a payload is and which workflow produced it. |
| `go run ./scripts/inspect_codec_payload -scan -from codec/<ksuid> -contains ReplayId -out x.csv [-sample-every N]` | Classifies codec entries in a key (= time, KSUIDs sort by time) range by content marker and writes key/size/sha256 per entry for joining in DuckDB. |
| `go run ./scripts/replay_codec_probe [-run-id R] [-compare k1,k2] [-dump-dir d] <workflow-id>` | Replays a real history through the production codec-wrapped DataConverter with codec writes captured in memory (kv is not modified), then matches each write by content to the history's large payloads and to given existing keys. |
| `go run ./scripts/dump_workflow_events [-activity-type T] <workflow-id> [start [end]]` | History dumps. |

Ad-hoc DuckDB queries go against `.side/tmp/payload_bloat/payload_bloat.duckdb`
(`duckdb -readonly ...`). `fmt_bytes` macro exists in `report.sql`.

## Round 1: where the bytes are (last 3 weeks, since 2026-09-16)

- **core:** fine. 296 MiB params and 156 MiB results written; 16 items >= 1 MiB,
  largest 3.9 MiB.
- **Temporal:** no huge events. Payloads over 1 MiB are offloaded to kv by
  `common.PayloadCodec`, so the largest event is 914 KiB. Running commands is
  small: ~9.7k runs, results total 31 MiB, largest 788 KiB.
- **kv:** bloated. 9.8 GiB written, 9.3 GiB of it offloaded codec payloads. Of
  5,721 codec entries written since Sep 1, 5,374 (9.3 GiB) are not referenced by
  any surviving history.
- **Why kv is 102 GiB:** 97 GiB is codec payloads, including ~1.1M entries from
  July 2026 (80 GiB, 76 GiB unreferenced; median entry 18.5 KiB, i.e. written
  under a lower codec threshold than today's 1 MiB).
- **Nothing cleans them up:** the `codec-payload-cleanup` workflow (run
  `58604c26-04aa-41ba-bca3-5a61e1b5476f`) last listed/deleted keys on
  2026-04-16. Right after a 1.5 MB `ListRunningWorkflowCodecKeys` result it hit
  `TMPRL1101 Potential deadlock` (orphan diffing over 1M+ keys runs inside
  workflow code) and has since failed 1,221 workflow tasks with
  `NonDeterministicError: lookup failed for scheduledEventID 565`.
- 8 runs reference 12,431 codec keys that no longer exist in kv (payloads
  that can no longer be decoded).
- All three DB files have `auto_vacuum=0`, so deleting rows alone will not
  shrink them (see `srv/sqlite/kv_maintenance.go` for the incremental-vacuum
  conversion).

## Round 2: why codec entries are orphaned

### Proven: replaying a history re-encodes every large command input

`replay_codec_probe` on `flow_3Ik8kxjDKAGKFwCzcyEMg6Rpdgr` run `a5cb5d11`
(102,401 events): the replay made exactly **8 codec writes** (9.4 MB). The 8
orphans written to kv at 2026-09-29 20:44:49 have the same sizes, and
`-compare` shows all 8 are **byte-identical** to the replay writes. Control:
the 4 large `ActivityTaskCompleted` results in that history were *not*
re-stored; only `ActivityTaskScheduled` / `SignalExternalWorkflowExecution`
inputs were. Temporal's Go SDK runs the DataConverter (and so the codec's
`Encode`) inside workflow code for each command it regenerates, and
`PayloadCodec.Encode` writes a fresh `codec/<ksuid>` every time.

Those 8 orphans contain `"flowId": "ReplayId"`, the placeholder workflow ID the
SDK's standalone `WorkflowReplayer` uses. So they were written by a standalone
replayer built with the real kv storage, not by the production worker.
`TestReplayRunningWorkflows` (`worker/replay/replay_running_test.go`) does
exactly that: `replayerOptions.DataConverter = clientOptions.DataConverter`
over `sidekick.GetStorage()`, replaying the 20 most recent running workflows
on every integration-test run. `ReplayWorkflowLatest` in
`worker/replay/replay.go` is the same.

`api/api.go` only decodes payloads; nothing under `api/` or `srv/` encodes.

### Orphans since 2026-09-16, by content (5,156 entries, 10 GiB)

Content hash = sha256 of the deterministic protobuf encoding of the stored
`Payload` (`inspect_codec_payload -scan` writes both raw and content hashes;
in practice no payload had more than one raw encoding). "Referenced" compares
against **all** 80k codec keys surviving histories reference, including keys
older than the window (`-scan -keys-file`). An earlier version of this table
only compared against referenced keys inside the window and so mislabeled
3,412 entries as "(c) identical to other orphans only"; their referenced
originals belong to flows started before Sep 16.

| category | entries | bytes | distinct contents |
|---|---|---|---|
| (a) `"...Id":"ReplayId"`: written by a standalone `WorkflowReplayer` | 51 | 160 MiB | 16 |
| (b) content-identical to a payload a surviving history still references | 4,835 | 8.4 GiB | 102 |
| (c) content-identical to other orphans only (Sep 16–17 only) | 15 | 16 MiB | 3 |
| (d) unique content | 2 | 3.7 MiB | 2 |
| referenced | 253 | 737 MiB | 97 |

So 99% of orphan bytes are exact re-encodes of command inputs that are still
in history. The mechanism is proven for (a) (see probe above). For (b) the
provenance is inferred, not proven: the copies carry the real workflow ID, so
they were encoded with real execution info, i.e. by the production worker
re-running workflow code (replay after sticky-cache eviction / worker restart,
or any other path that regenerates commands). Supporting evidence:

- `replay_codec_probe` on `flow_3JO0oMOU1vBGYPVpdsyTBzfJqNi` (17,447 events,
  completed Oct 7): one replay makes **65 codec writes (146 MB)**. Its 3.2 MiB
  `PersistFlowAction` input (`fa_3K1VanebJLgkegKewqbP9Rgdt9A`, merge-approval
  diff + diffSinceLastReview) appears 12 times in history and 12 times per
  replay; kv holds 12 referenced copies plus **321 orphan copies** of it,
  written in bursts (121 codec writes, 348 MB, in the 5 s 20:45:19–20:45:24
  on Oct 7, i.e. ~2 full replays, while the flow was actively executing
  activities). An earlier claim that "20+ copies/sec is more than one replay
  can account for" was wrong.
- The probe's writes for this flow differ from the orphans only by
  `ReplayId` vs the real flow ID (24-byte size difference, e.g. 3,210,659 vs
  3,210,683).

Not yet established: *why* the production worker replays this often during
active execution (sticky cache size / eviction, worker restarts, task
timeouts). The history dump shows no `WorkflowTaskFailed` for that run, but
`dump_workflow_events` filters WorkflowTask* events, so that is not proof.

Other findings in passing:
- The 1.1 MB merge-approval diff above is almost entirely
  `.side/tmp/affected.log`: scratch files leak into review/merge diffs, which
  are then stored ~10 times per approval round.
- One orphan is a 2.7 MB `text_delta` stream event containing only whitespace
  (degenerate model output) sent through Temporal.

## Fixes

### Implemented: no codec writes during replay (`common/replay_aware_data_converter.go`)

Why the SDK writes on replay: history *events* are only decoded, but workflow
*code* runs again, and `workflow.ExecuteActivity` / child / signal encode their
inputs through the DataConverter to build a command *before* the SDK matches
that command against the existing history event and discards it. So every
replay of a long-lived workflow wrote a fresh `codec/<ksuid>` copy of every
large command input. Activity results are never re-encoded, which is why none
of those were duplicated.

`NewReplayAwareDataConverter` wraps the codec converter and implements the
SDK's `workflow.ContextAware` interface. The SDK hands it the workflow context
at each command-encoding site; when `workflow.IsReplaying(ctx)` is true it
encodes inline (no KV write), otherwise it offloads through the codec. The
choice is made per call, so a task that starts in replay and transitions to
live execution offloads the live part. Decoding always goes through the codec;
client- and activity-side encoding always offloads. Keys stay per-encode
KSUIDs, so the existing cleanup design (delete on completion + orphan scan) is
unchanged and nothing becomes shared or un-collectable.

An earlier content-addressed-key approach (sha256 keys, write-if-absent) was
implemented and then reverted: it stops the growth but makes keys shareable
across runs, which would have required a new reference-aware GC.

Evidence:
- `common/replay_aware_data_converter_test.go`: synthetic history replayed
  through the standalone replayer. Codec-only control writes on replay; the
  replay-aware converter writes nothing. Red/green confirmed by disabling the
  replay branch.
- `common/replay_aware_data_converter_integration_test.go` (needs
  `SIDE_INTEGRATION_TEST=true` and the local Temporal server): a real worker
  runs an oversized activity input, an oversized local-activity result, idles
  on a signal, then the worker is stopped, the sticky cache purged, and a new
  worker handles the signal (full replay, then a live second activity).
  Control: codec-only converter leaves unreferenced copies. Treatment: the set
  of stored keys equals the set of keys history references, every reference
  decodes to the expected value, workflow result correct.
- `scripts/replay_codec_probe` on real history `flow_3Ik8kxjDKAGKFwCzcyEMg6Rpdgr`
  run `a5cb5d11-f5ab-4252-beec-35ee1dee1696` (102k events): 8 codec writes
  before the change, 0 after, replay error nil.

Residual: `UpsertMemo` and other encode paths that the SDK does not route
through `WithWorkflowContext` keep their previous behaviour (we don't use
oversized memos). No paths were made worse.

### Deferred

1. Repair the legacy orphan scan (stuck since 2026-04-16): heavy diffing must
   move into activities with paged inputs/outputs, gated with
   `workflow.GetVersion`; terminate run `58604c26` and restart. Before any
   destructive run, the reference scan must (a) collect codec references
   recursively from every payload field in every event type
   (`extractPayloadsFromEvent` is a partial switch that misses failure
   details, headers, memo, updates), and (b) fail closed: a single
   history-read error must result in zero deletions. Neither is true today.
2. Standalone replayers (`TestReplayRunningWorkflows`, `worker/replay`) now
   write nothing during replay via the shared client options; the
   `replay_codec_probe` in-memory overlay remains useful for before/after
   comparisons.
3. Scratch files (`.side/tmp/affected.log`) leaking into review/merge diffs.

## Visibility repair and migration verification

### Missing visibility discovered while preparing codec cleanup

The earlier visibility rebuild dropped `executions_visibility` without rebuilding
all retained executions. The diagnostic found 1,668 executions missing from
visibility, including 281 Running runs. Listing workflows alone was therefore
not a safe inventory for codec deletion. A new codec key can belong to an old
invisible workflow; filtering by key creation time does not solve this.

The approved live repair used Temporal's `RefreshWorkflowTasks` admin API:
1,642 runs refreshed, 17 already listed, and 9 NotFound during the apply pass.
The subsequent verification reported 1,615 listed, zero missing, and 53 NotFound.
Those 53 were old closed executions eligible for retention deletion; refreshing
tasks also regenerates retention tasks. These are observations from that repair,
not a guarantee that visibility will always remain complete.

Tools:
- `scripts/visibility_gap`: compares the extracted execution inventory with
  visibility and checks missing executions through the API.
- `scripts/refresh_visibility`: dry-run by default; `-apply` refreshes tasks and
  verifies visibility. Refreshing tasks can also regenerate pending timers,
  activity tasks, and retention tasks.

### Automatic repair after future visibility rebuilds

The rebuild transaction now records namespace/workflow/run IDs in
`sidekick_temporal_visibility_repair` before dropping the old visibility index.
A failed inventory write aborts the rebuild.

Before starting Temporal, the inventory is copied to
`<temporal.db path>.visibility-repair.db`. The copy commits before the source
is cleared; startup stops if export fails. An interrupted export can be repeated
without losing IDs. Completed repairs are not reintroduced on a later startup.

After the embedded server starts and its API is ready, repair reads only that
separate checklist and calls the existing admin API. It never opens the live
Temporal database. An entry is acknowledged only after it is listed or its
execution is confirmed NotFound. Other errors preserve the entry for the next
startup. Each run has a 20-second timeout; the startup repair pass has a
two-minute budget. A large or failing backlog can require further startups.
This does not retroactively detect a rebuild whose inventory was never recorded.

The separate file is necessary for compatibility with the running embedded
server: the initial implementation's first schema read failed with SQLITE_BUSY
despite a five-second busy timeout, and external read-only probes stayed blocked
throughout a 24-second observation. The precise source of that prolonged lock
was not established; no Temporal locking settings were changed.

### Real old-checkout test: red and green

`sh scripts/test_temporal_upgrade/visibility.sh` builds an old checkout
(default `5d986e50`) in a temporary worktree under `.side/tmp`, creates a fresh
database with two workflows parked on a signal and one completed workflow,
then upgrades it using the current build. Isolated ports and a separate data
home keep it away from the user's server.

A critical control: the harness waits for the old server's visibility queue
to drain naturally before stopping it. Without that wait, outstanding indexing
tasks can repopulate visibility after restart and falsely make a broken
migration appear successful. No task rows are manually deleted.

Observed results:
- Negative control without repair, after natural queue drain: the original
  parked run is not visible after the rebuild; the test fails.
- Fixed build, after natural queue drain: all three exact run IDs are visible,
  their statuses and history lengths are unchanged, and both parked workflows
  subsequently complete with the expected activity result.
- Another restart: all three remain visible and no refresh is repeated.
- File-backed regression tests cover export failure, retry, preserving failed
  repairs, skipping already-listed runs, and repairing while the main database
  is locked. Temporarily restoring main-DB access or clearing before copying
  makes the respective regression tests fail.

Representative logs:
- `.side/tmp/visibility-red-drained.log`
- `.side/tmp/visibility-green-sidecar.log`
- `.side/tmp/visibility-red-live-db-access.log`
- `.side/tmp/visibility-red-clear-before-copy.log`

### Remaining scope

The codec cleanup workflow has not yet been repaired or restarted. Visibility
repair is a prerequisite, not permission to delete every unmarked key: reference
enumeration completeness, retained/reset histories, scan races, bounded deletion,
and fail-closed behavior still need to be addressed.

The original size findings remain about retained payload data observed in the
extraction, grouped by available timestamps—not a time series of database file
sizes. The initial DuckDB artifact is
`.side/tmp/payload_bloat/payload_bloat.duckdb`; rerunning reports does not by itself
measure new physical file growth.