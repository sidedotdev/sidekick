# Flow actions performance

Initially profiled 2026-10-12, Darwin/arm64; Go driver SQLite 3.51.2.
Workspace: `ws_3G9KgmkNu9O99Sf8zBthsyZQCMz`.
Flows: `flow_3KFEV6chTRqgtk6lO9vHle76tvx` and `flow_3KQRu33aaQVhY1b8geFFJKc3lre`.

## Root cause of the second-long requests

**Stale SQLite STAT4 histograms select the wrong index.** `GetFlowActions`
searches `idx_flow_actions_workspace_id`, then filters by flow: roughly
**28,000 workspace actions**, rather than the requested flow's hundreds.
Statistics describe **96,987 actions**, versus **376,998 actually present**;
the sampled histograms omit this workspace and both flows.

The resulting read amplification exposes recurring filesystem-read latency,
**not merely a one-time cold start**:

| Control / profile | Result |
| --- | --- |
| **No profiler**, alternating both URLs through Vite, 1.5 s between requests | **13/16 exceeded 1 s**. Original flow median/max **1.55/1.92 s**; second flow **1.37/1.95 s**. |
| Live Go execution trace, second flow | About **19,243 `pread` calls before the first row**, even on fast requests. These took **1.81 s of a 2.41 s request**, versus 25–65 ms on fast requests. Query mutex waiting totalled only ~8 ms across 17 requests. |
| Read-only, persistent-connection SQL control; rotating index variants, identical 750-action results verified by hash | Forced flow index: **23–49 ms** to load all rows. Workspace-index plan: **64–1,084 ms**. Slow reads also occurred without any database-version change. |
| Isolated in-memory schema/statistics copy, **both flows** | Copied statistics reproduce the workspace plan; removing only STAT4 switches to the flow index; restoring STAT4 restores the bad plan. Adding a composite index selects both keys despite old statistics. |

Thus active writes are **not required**. JSON encoding and response transfer
cannot explain the delay before the first database row. The reported **9 s**
extreme was not captured; its exact cause is not independently established.
The trace identifies slow `pread` execution, not the kernel-level reason for
each read's latency.

## Top recommendations

1. **Add `(workspace_id, flow_id)` index.** Verify selection using the actual Go
   driver and benchmark tail latency under concurrent activity. The planner
   control passed; runtime timings above use the existing flow index, not a
   deployed composite index.
2. **Use bounded statistics maintenance (implemented below).** Explicit sampling
   avoids both the missed-refresh heuristic and recurring full index scans.
   This fixes the observed plans, but a composite index remains more robust
   against skewed data and approximate estimates.
3. **Then reduce response size:** lightweight/paginated listing with details on
   demand. Responses measured **5.71 MB / 11.72 MB**. JSON/allocation work affects
   the baseline, but is not the primary cause of these second-long spikes.

## Statistics maintenance follow-up

- **Why the existing schedule missed this:** a four-hour `PRAGMA optimize` job
  already existed. Even `optimize(0x10002)` left STAT1 and STAT4 unchanged in the
  live check: growth was only 3.9×, below SQLite's current roughly 10× heuristic.
- **Full analysis confirmed the repair:** `ANALYZE flow_actions` took 2.12 s,
  updated the estimate to 377,222 rows, and switched **both** queries to the flow
  index. Across eight runs per query, median SQL load times fell **101 → 24 ms**
  and **87 → 35 ms**, with unchanged results verified by hash.
- **Bounded analysis also works:** `analysis_limit=400; ANALYZE flow_actions`
  removed the old STAT4 samples, refreshed approximate STAT1 estimates, and
  retained the flow-index plan for both queries. Subsequent median loads were
  14/29 ms; the second flow had grown, so its comparison used a read snapshot
  to verify identical results between default and forced-index queries.
- **Cost:** a repeated full refresh used 0.80 CPU-seconds / 16.4 MiB peak RSS;
  bounded refresh used 0.24 CPU-seconds / 16.7 MiB. These are standalone Go
  diagnostic process totals, excluding compilation, using SQLite's default
  cache—not incremental server memory. Analysis elapsed times were 4.21/1.70 s:
  sampling limits work, not I/O latency or writer contention.
- **Automatic maintenance:** bounded action analysis now runs in the background
  at startup and every four hours, with a 30 s pass timeout. General optimize
  remains; connection-time optimize uses `0x10012` to enable its temporary
  analysis limit. Core connections expire after four hours so they eventually
  reload statistics cached per connection. Restart the application to activate
  this maintenance. No recurring full analysis or index changes were added.

SQLite [recommends approximate analysis for large databases](https://www.sqlite.org/lang_analyze.html#approximate_analyze_for_large_databases).
There is no incremental STAT4 refresh: bounded analysis deliberately trades
histograms for cheaper STAT1 estimates. The running REST service still showed
some slow responses after the full refresh; this is a verified SQL-plan repair,
not proof that every endpoint latency spike is resolved.

## Local evidence / reproduction

Under `.side/tmp/`: `actions-tail-active.{profile.pprof,trace,http.json}`,
`actions-tail-active.syscalls.jsonl`, `actions-tail-unprofiled-proxy.http.json`,
`actions-tail-final-query.jsonl`, and `actions-tail-planner-control.txt`.
Use `go tool pprof -top -cum` on the CPU profile or `go tool trace` on the trace.
`probe_actions_query.go` and `check_actions_planner.go` accept `-db`, `-workspace`,
`-flow`; both open the source database read-only. Initial schema/statistics
experiments were isolated. The follow-up refreshed live statistics only.
Evidence: `actions-stats-{refresh,resource,resource-time,bounded,bounded-time}.txt`
and `actions-stats-*.{jsonl,plan.txt}`. `refresh_actions_stats.go -db PATH -apply
-analysis-limit 400` repeats bounded refresh; omit `-apply` to inspect only.
Use `probe_actions_query.go -snapshot` when flows are actively changing.