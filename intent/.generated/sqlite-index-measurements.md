# SQLite index selection

## Decision

Migration 018 adds three indexes, not the original five:

| Index | Decision and workload rationale |
| --- | --- |
| `flow_actions(workspace_id, flow_id)` | Keep. The supplied live profile establishes a real failure: scanning ~28,000 workspace actions to return hundreds from a flow. The composite constrains both keys even when statistics favor the wrong single-column index. |
| `worktrees(workspace_id, flow_id)` | Keep. `GetFlowHandler`, intent lookup, and evaluation extraction perform flow lookups; maintenance lists by workspace, also supported by this prefix. No production `DeleteWorktree` callers were found, so physical cleanup/hibernation should not be assumed to bound retained metadata. Writes are creation/occasional metadata persistence, unlike action/status updates. |
| `tasks(workspace_id, archived DESC, updated DESC) WHERE archived IS NOT NULL` | Keep. Archive browsing counts retained tasks and fetches an ordered page. This avoids fetching/sorting the workspace's entire archived history for a small page and supplies a covering index for its count. Gains were consistent even at 100 total tasks per workspace. |
| `flows(workspace_id, parent_id, created, id)` | Remove. The existing `parent_id` index already retrieves the few flows belonging to a task. Sorting two rows does not justify this wide, maintained-on-upsert index. Revisit if tasks routinely have many flows. |
| `tasks(workspace_id, status) WHERE archived IS NULL` | Defer. Benefits depended on statistics; no consistent benefit with bounded analysis in either order. Full analysis did improve the large active list from 1.66 to 0.88 ms, but that alone does not establish a production bottleneck. Status/archive transitions maintain this index. Revisit with actual active/archive distributions and task-list profiles. |

The first decision has production evidence; the others use synthetic measurements
and repository call patterns, **not measured production cardinalities or request
frequencies**.

## Reproduction

`BenchmarkQueryIndexes` in `srv/sqlite/query_indexes_bench_test.go` is opt-in.
It invokes production storage read/upsert methods, not copied SELECT statements.
It migrates fresh file-backed databases, removes all five candidates for the
baseline, and measures each candidate independently. Expected row counts, archive
totals, and unordered result fingerprints must agree before timing reads.
Existing unit tests remain responsible for ordered-result semantics.

Run from the repository root, with raw output retained for comparison:

```sh
TMPDIR="$PWD/.side/tmp" go test ./srv/sqlite -run '^$' \
  -bench '^BenchmarkQueryIndexes/' -benchtime=50x -count=3 -timeout=5m \
  > .side/tmp/query-indexes-forward.txt

TMPDIR="$PWD/.side/tmp" SIDE_INDEX_BENCH_REVERSE=1 go test ./srv/sqlite -run '^$' \
  -bench '^BenchmarkQueryIndexes/' -benchtime=50x -count=3 -timeout=5m \
  > .side/tmp/query-indexes-reverse.txt

TMPDIR="$PWD/.side/tmp" SIDE_INDEX_BENCH_FULL_ANALYZE=1 go test ./srv/sqlite -run '^$' \
  -bench '^BenchmarkQueryIndexes/' -benchtime=50x -count=3 -timeout=5m \
  > .side/tmp/query-indexes-full-analysis.txt

TMPDIR="$PWD/.side/tmp" go test ./srv/sqlite -run '^$' \
  -bench '^BenchmarkQueryIndexes/(small|large)/.*/(baseline|indexed)/upsert$' \
  -benchtime=200x -count=3 -timeout=5m \
  > .side/tmp/query-indexes-writes-forward.txt
```

Repeat the last command with `SIDE_INDEX_BENCH_REVERSE=1` and a separate output
file. `go test` is used here because the affected-tests wrapper suppresses
benchmark output. Add `-v` to see SQLite version and fixture metadata.

Environment: Linux/amd64, AMD EPYC 9654, actual Go SQLite driver reporting 3.51.2.
Standard storage pragmas: WAL, synchronous NORMAL, 64 MB connection cache.
One connection, no background maintenance or competing writer. Default analysis
limit is 400; the full-analysis control uses 0. These are newly generated
statistics, not a reproduction of the supplied stale STAT4 histograms.

Each fixture interleaves ten workspaces using 30-character IDs:

| Table | Small / large total rows | Requested rows / distribution |
| --- | --- | --- |
| Flow actions | 10,000 / 375,000 | 100 / 750 actions in the requested flow; 1 KiB results |
| Worktrees | 100 / 10,000 | One per flow; 10 / 1,000 per workspace |
| Flows | 1,000 / 10,000 | Two per task |
| Tasks | 1,000 / 20,000 | 95% archived; 5 / 100 active per workspace; 1 KiB descriptions; archive page size 25 |

These are explicit sizing scenarios, not a claim that all installations reach
them. The large action fixture is in the supplied profile's size range but has
smaller payloads and different skew. The task fixtures model retained history;
the worktree fixtures test both small installations and accumulated metadata.

## Read measurements

Values are **milliseconds per call**, baseline → candidate: median of three
50-operation batch averages. Ranges span independent forward/reverse runs with
bounded analysis, not per-request percentiles.

| Read | Small baseline → indexed | Large baseline → indexed |
| --- | --- | --- |
| Flow actions | 0.83–0.87 → 0.85–0.89 | 130–133 → 5.80–6.46 |
| Worktrees for flow | 0.043–0.054 → 0.031–0.033 | 1.44–1.45 → 0.031–0.040 |
| Worktrees for workspace | 0.069–0.081 → 0.065–0.073 | 4.07–4.10 → 3.15–3.22 |
| Flows for task | 0.045–0.059 → 0.045–0.055 | 0.051 → 0.049–0.063 |
| Active tasks | 0.098–0.134 → 0.099–0.127 | 1.14–1.24 → 1.17–1.42 |
| Active tasks, one status | 0.075–0.110 → 0.056–0.084 | 0.79–0.87 → 0.79–0.80 |
| Archived tasks, count + page | 7.34–7.84 → 0.37–0.38 | 13.99–14.06 → 0.58–0.59 |

Full analysis is an important counter-control: large action reads were
8.46 → 5.97 ms, not a 20x improvement. Much of the bounded-analysis win is
statistics-dependent; the composite is primarily protection against bad access
paths, not a promise of that speedup when the old plan is already good.
The archive benefit persisted with full analysis: small 1.88 → 0.41 ms;
large 14.32 → 0.60 ms. Small unrelated timing changes are not treated as evidence
for an index.

## Costs

Index bytes are the increase in occupied SQLite pages immediately around
`CREATE INDEX`, before subsequent analysis or writes. They exclude transient
sort/WAL space. Build times below are forward/reverse read-setup measurements.
No existing indexes are dropped by the migration; these costs are additive.

| Candidate | Small / large added KiB | Large build time | Large warmed upsert cost, baseline → indexed (ms) |
| --- | --- | --- | --- |
| Flow actions | 684 / 25,892 | 1.73–1.82 s | Forward 0.69 → 0.77; reverse 0.75 → 0.79 |
| Worktrees | 12 / 684 | 26–27 ms | Forward 0.31 → 0.39; reverse 0.47 → 0.52 |
| Flows | 124 / 1,224 | 40–42 ms | Forward 0.27 → 0.36; reverse 0.37 → 0.59 |
| Active tasks | 4 / 52 | 8–9 ms | Forward 0.86 → 0.96; reverse 0.86 → 0.81 |
| Archived tasks | 80 / 1,504 | 65–67 ms | Forward 0.78 → 0.85; reverse 0.96 → 0.96 |

Upserts use three 200-operation batches with deterministically distributed keys
and prewarmed, identically serialized records. They include production
serialization and autocommit, but not pure inserts, deletes, archive transitions,
or concurrent contention. Variation between orders is substantial: do not treat
a lower indexed time as evidence that maintaining an index makes writes cheaper.

The action index adds ~25.3 MiB to a ~536 MiB synthetic database (about 4.7%).
Worktrees cost ~0.67 MiB at 10,000 rows to avoid scanning the entire table for
every flow fetch. Archived tasks cost ~1.47 MiB at 20,000 rows, with only archived
records represented. At the large sizes, millisecond-level read savings exceed
the observed tens-of-microseconds incremental upsert costs; actual amortization
still depends on request frequency. At small sizes, action/worktree benefits
are much smaller. The flow index's ~1.2 MiB and consistent write penalty buy
essentially nothing for the tested two-flow task.

## Other query patterns and limits

- Subflows already have `(workspace_id, flow_id)`.
- KV's `(workspace_id, key)` primary key, workspace-config keys, globally unique
  entity IDs, and the remote-device token-hash index already cover their point
  lookups. No extra workspace/ID composites are warranted.
- Projects already have a workspace index. A wide expression index for priority
  ordering needs a demonstrated large-project-list workload before adding it.
- Existing overlapping indexes were not removed; measuring their replacement
  is separate from these additive comparisons.

This is not an endpoint load test or a tail-latency guarantee. It does not
reproduce Darwin filesystem stalls, realistic concurrent workflow activity,
all workspace skews, cold OS caches, or the supplied multi-megabyte responses.
The historical live profile remains the evidence for that incident.
Index creation can block startup/writers; the measured build times are not
production upper bounds. Archive counting and deep OFFSET pagination still
scale with retained rows despite the index. Follow-up production verification
should check plans and endpoint tails under concurrency; the separate
[query-plan proposal](sqlite-query-plans.md) describes broader regression checks.